package cert

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/docker/machine/libmachine/auth"
)

func newTestAuthOptions(t *testing.T) *auth.Options {
	t.Helper()
	dir, err := os.MkdirTemp("", "machine-bootstrap-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	certDir := filepath.Join(dir, "certs")
	return &auth.Options{
		CertDir:          certDir,
		CaCertPath:       filepath.Join(certDir, "ca.pem"),
		CaPrivateKeyPath: filepath.Join(certDir, "ca-key.pem"),
		ClientCertPath:   filepath.Join(certDir, "cert.pem"),
		ClientKeyPath:    filepath.Join(certDir, "key.pem"),
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
	if err != nil {
		t.Fatalf("read ca cert: %v", err)
	}
	caBlock, _ := pem.Decode(caPEM)
	if caBlock == nil {
		t.Fatalf("ca cert: failed to decode PEM")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatalf("parse ca cert: %v", err)
	}

	clientPEM, err := os.ReadFile(a.ClientCertPath)
	if err != nil {
		t.Fatalf("read client cert: %v", err)
	}
	clientBlock, _ := pem.Decode(clientPEM)
	if clientBlock == nil {
		t.Fatalf("client cert: failed to decode PEM")
	}
	clientCert, err := x509.ParseCertificate(clientBlock.Bytes)
	if err != nil {
		t.Fatalf("parse client cert: %v", err)
	}

	if err := clientCert.CheckSignatureFrom(caCert); err != nil {
		t.Fatalf("client cert is not signed by CA on disk: %v "+
			"(this is the symptom of the concurrent-bootstrap race)", err)
	}
}

func TestBootstrapCertificates_Idempotent(t *testing.T) {
	a := newTestAuthOptions(t)

	if err := BootstrapCertificates(a); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Record CA cert contents so we can confirm the second call does not
	// regenerate them.
	caBefore, err := os.ReadFile(a.CaCertPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := BootstrapCertificates(a); err != nil {
		t.Fatalf("second call: %v", err)
	}

	caAfter, err := os.ReadFile(a.CaCertPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(caBefore) != string(caAfter) {
		t.Fatalf("CA certificate was regenerated on the second call; " +
			"BootstrapCertificates should be a no-op when certs are valid")
	}

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
// that after N concurrent callers finish, the client cert still verifies
// against the CA cert on disk.
func TestBootstrapCertificates_ConcurrentDoesNotRace(t *testing.T) {
	const parallelism = 20

	a := newTestAuthOptions(t)

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, parallelism)

	for i := 0; i < parallelism; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all goroutines at once to maximise contention
			if err := BootstrapCertificates(a); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("BootstrapCertificates returned error under concurrency: %v", err)
	}

	assertConsistentTLSMaterial(t, a)
}
