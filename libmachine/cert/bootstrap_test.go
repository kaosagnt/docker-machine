package cert

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/docker/machine/libmachine/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestAuthOptions(t *testing.T) *auth.Options {
	t.Helper()
	dir := t.TempDir()

	certDir := filepath.Join(dir, "certs")
	return &auth.Options{
		CertDir:          certDir,
		CaCertPath:       filepath.Join(certDir, "ca.pem"),
		CaPrivateKeyPath: filepath.Join(certDir, "ca-key.pem"),
		ClientCertPath:   filepath.Join(certDir, "cert.pem"),
		ClientKeyPath:    filepath.Join(certDir, "key.pem"),
		// Exercise the lock path by default in tests. Tests that explicitly
		// want to assert unlocked behaviour override this to false.
		BootstrapLock: true,
	}
}

// assertConsistentTLSMaterial parses ca.pem and cert.pem from authOptions and
// verifies that cert.pem is signed by ca.pem. This is exactly the check
// Docker Machine relies on when validating TLS to a created VM: if the CA
// file and the client certificate file got their inputs from different
// concurrent bootstrap runs, this signature verification fails.
func assertConsistentTLSMaterial(t *testing.T, a *auth.Options) {
	t.Helper()

	caPEM, err := os.ReadFile(a.CaCertPath)
	require.NoError(t, err, "read ca cert")
	caBlock, _ := pem.Decode(caPEM)
	require.NotNil(t, caBlock, "ca cert: failed to decode PEM")
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	require.NoError(t, err, "parse ca cert")

	clientPEM, err := os.ReadFile(a.ClientCertPath)
	require.NoError(t, err, "read client cert")
	clientBlock, _ := pem.Decode(clientPEM)
	require.NotNil(t, clientBlock, "client cert: failed to decode PEM")
	clientCert, err := x509.ParseCertificate(clientBlock.Bytes)
	require.NoError(t, err, "parse client cert")

	assert.NoError(t, clientCert.CheckSignatureFrom(caCert),
		"client cert is not signed by CA on disk "+
			"(this is the symptom of the concurrent-bootstrap race)")
}

func TestBootstrapCertificates_Idempotent(t *testing.T) {
	a := newTestAuthOptions(t)

	require.NoError(t, BootstrapCertificates(a), "first call")

	// Record CA cert contents so we can confirm the second call does not
	// regenerate them.
	caBefore, err := os.ReadFile(a.CaCertPath)
	require.NoError(t, err)

	require.NoError(t, BootstrapCertificates(a), "second call")

	caAfter, err := os.ReadFile(a.CaCertPath)
	require.NoError(t, err)
	assert.Equal(t, string(caBefore), string(caAfter),
		"CA certificate was regenerated on the second call; "+
			"BootstrapCertificates should be a no-op when certs are valid")

	assertConsistentTLSMaterial(t, a)
}

// TestBootstrapCertificates_NoLockWhenDisabled asserts that when
// BootstrapLock is left at its zero value, BootstrapCertificates takes
// no lock and creates no lock file in the cert directory. This is the
// existing, opt-in-only contract for all callers that don't set the
// `--tls-bootstrap-lock` flag or MACHINE_TLS_BOOTSTRAP_LOCK env var.
func TestBootstrapCertificates_NoLockWhenDisabled(t *testing.T) {
	a := newTestAuthOptions(t)
	a.BootstrapLock = false

	require.NoError(t, BootstrapCertificates(a))

	_, err := os.Stat(filepath.Join(a.CertDir, bootstrapLockFile))
	assert.True(t, os.IsNotExist(err),
		"expected no bootstrap lock file to be created when BootstrapLock is off, got err=%v", err)

	assertConsistentTLSMaterial(t, a)
}

// TestBootstrapCertificates_ConcurrentDoesNotRace exercises the race that
// manifested in production when GitLab Runner's docker+machine autoscaler
// spawned many `docker-machine create` subprocesses simultaneously on a
// freshly-provisioned runner manager. Without the file lock around
// BootstrapCertificates, multiple goroutines stat the empty cert dir, all
// decide the CA is missing, and race to write ca.pem / ca-key.pem /
// cert.pem / key.pem. The files end up inconsistent: the client certificate
// on disk is signed by a CA key that is no longer on disk. This test asserts
// that after N concurrent callers finish with BootstrapLock enabled, the
// client cert still verifies against the CA cert on disk.
func TestBootstrapCertificates_ConcurrentDoesNotRace(t *testing.T) {
	const parallelism = 20

	a := newTestAuthOptions(t)

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, parallelism)

	for i := 0; i < parallelism; i++ {
		wg.Go(func() {
			<-start // release all goroutines at once to maximise contention
			if err := BootstrapCertificates(a); err != nil {
				errs <- err
			}
		})
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("BootstrapCertificates returned error under concurrency: %v", err)
	}

	assertConsistentTLSMaterial(t, a)
}

// TestBootstrapCertificates_StaleLock asserts that when the cert-dir lock
// is already held (e.g. by a stuck or ptrace-frozen process),
// BootstrapCertificates does not block indefinitely but instead returns
// errFileLockAcquiring once its deadline elapses. Without this bound, a
// stale holder could wedge every concurrent `docker-machine create` until
// gitlab-runner's own (1-hour) subprocess timeout fires.
func TestBootstrapCertificates_StaleLock(t *testing.T) {
	a := newTestAuthOptions(t)

	require.NoError(t, os.MkdirAll(a.CertDir, 0700))
	lock, err := newFileLockWithTimeout(filepath.Join(a.CertDir, bootstrapLockFile), 1*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, lock.Unlock())
	})

	err = bootstrapCertificatesWithLockTimeout(a, 100*time.Millisecond)
	assert.ErrorIs(t, err, errFileLockAcquiring)
}
