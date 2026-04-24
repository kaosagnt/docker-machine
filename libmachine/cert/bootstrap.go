package cert

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/docker/machine/libmachine/auth"
	"github.com/docker/machine/libmachine/log"
	"github.com/docker/machine/libmachine/mcnutils"
)

// bootstrapLockDefaultTimeout bounds how long BootstrapCertificates will wait
// to acquire the cert-dir file lock before failing loudly. The goal is not to
// cover the cost of legitimate work under the lock (a few hundred ms of RSA
// keygen at worst) but to make sure a stale holder — e.g. a SIGSTOP'd or
// ptrace-frozen process — cannot wedge unrelated `docker-machine create`
// invocations for the full gitlab-runner subprocess timeout (currently 1h).
const bootstrapLockDefaultTimeout = 15 * time.Second

// errFileLockAcquiring is returned by newFileLockWithTimeout when the deadline
// elapses before the lock can be acquired.
var errFileLockAcquiring = errors.New("timed out awaiting for file lock acquire")

// bootstrapLockFile is the filename used inside authOptions.CertDir to serialise
// concurrent certificate bootstrap operations across processes. See
// BootstrapCertificates for the race it defends against.
const bootstrapLockFile = ".bootstrap.lock"

// fileLock is an exclusive, blocking, cross-process advisory lock backed by a
// file. It is used to serialise concurrent invocations of
// BootstrapCertificates so that simultaneous `docker-machine create`
// subprocesses do not race on CA/client certificate generation. The
// platform-specific acquisition and release primitives live in
// flock_unix.go and flock_windows.go.
type fileLock struct {
	f *os.File
}

// Unlock releases the lock. Closing the underlying file descriptor is
// sufficient on every supported platform: the kernel releases any lock
// held on the last close of the open file description / handle.
func (l *fileLock) Unlock() error {
	return l.f.Close()
}

func createCACert(authOptions *auth.Options, caOrg string, bits int) error {
	caCertPath := authOptions.CaCertPath
	caPrivateKeyPath := authOptions.CaPrivateKeyPath

	log.Infof("Creating CA: %s", caCertPath)

	// check if the key path exists; if so, error
	if _, err := os.Stat(caPrivateKeyPath); err == nil {
		return errors.New("certificate authority key already exists")
	}

	if err := GenerateCACertificate(caCertPath, caPrivateKeyPath, caOrg, bits); err != nil {
		return fmt.Errorf("generating CA certificate failed: %s", err)
	}

	return nil
}

func createCert(authOptions *auth.Options, org string, bits int) error {
	certDir := authOptions.CertDir
	caCertPath := authOptions.CaCertPath
	caPrivateKeyPath := authOptions.CaPrivateKeyPath
	clientCertPath := authOptions.ClientCertPath
	clientKeyPath := authOptions.ClientKeyPath

	log.Infof("Creating client certificate: %s", clientCertPath)

	if _, err := os.Stat(certDir); err != nil {
		if os.IsNotExist(err) {
			if err := os.Mkdir(certDir, 0700); err != nil {
				return fmt.Errorf("failure creating machine client cert dir: %s", err)
			}
		} else {
			return err
		}
	}

	// check if the key path exists; if so, error
	if _, err := os.Stat(clientKeyPath); err == nil {
		return errors.New("client key already exists")
	}

	// Used to generate the client certificate.
	certOptions := &Options{
		Hosts:       []string{""},
		CertFile:    clientCertPath,
		KeyFile:     clientKeyPath,
		CAFile:      caCertPath,
		CAKeyFile:   caPrivateKeyPath,
		Org:         org,
		Bits:        bits,
		SwarmMaster: false,
	}

	if err := GenerateCert(certOptions); err != nil {
		return fmt.Errorf("failure generating client certificate: %s", err)
	}

	return nil
}

func BootstrapCertificates(authOptions *auth.Options) error {
	return bootstrapCertificatesWithLockTimeout(authOptions, bootstrapLockDefaultTimeout)
}

func bootstrapCertificatesWithLockTimeout(authOptions *auth.Options, lockTimeout time.Duration) error {
	certDir := authOptions.CertDir
	caCertPath := authOptions.CaCertPath
	clientCertPath := authOptions.ClientCertPath
	clientKeyPath := authOptions.ClientKeyPath
	caPrivateKeyPath := authOptions.CaPrivateKeyPath

	// TODO: I'm not super happy about this use of "org", the user should
	// have to specify it explicitly instead of implicitly basing it on
	// $USER.
	caOrg := mcnutils.GetUsername()
	org := caOrg + ".<bootstrap>"

	bits := 2048

	if _, err := os.Stat(certDir); err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(certDir, 0700); err != nil {
				return fmt.Errorf("creating machine certificate dir failed: %s", err)
			}
		} else {
			return err
		}
	}

	// Opt-in: serialise bootstrap across processes. Without this lock,
	// parallel `docker-machine create` invocations (e.g. from GitLab
	// Runner's docker+machine autoscaler provisioning N VMs at once on a
	// fresh host) each see no CA/client files and race to generate them,
	// overwriting each other's output. The resulting CA, host certificate,
	// and VM certificate no longer match, TLS auth to the created VM
	// fails, and Docker Machine deletes the cert dir — re-triggering the
	// loop.
	//
	// Off by default to preserve existing behaviour for every caller;
	// enable via `--tls-bootstrap-lock` on `docker-machine create` or via
	// the MACHINE_TLS_BOOTSTRAP_LOCK environment variable.
	if authOptions.BootstrapLock {
		lock, err := newFileLockWithTimeout(filepath.Join(certDir, bootstrapLockFile), lockTimeout)
		if err != nil {
			return fmt.Errorf("acquiring cert bootstrap lock: %w", err)
		}
		defer func() {
			if err := lock.Unlock(); err != nil {
				log.Warnf("releasing cert bootstrap lock: %s", err)
			}
		}()
	}

	if _, err := os.Stat(caCertPath); os.IsNotExist(err) {
		if err := createCACert(authOptions, caOrg, bits); err != nil {
			return err
		}
	} else {
		current, err := CheckCertificateDate(caCertPath)
		if err != nil {
			return err
		}
		if !current {
			log.Info("CA certificate is outdated and needs to be regenerated")
			os.Remove(caPrivateKeyPath)
			if err := createCACert(authOptions, caOrg, bits); err != nil {
				return err
			}
		}
	}

	if _, err := os.Stat(clientCertPath); os.IsNotExist(err) {
		if err := createCert(authOptions, org, bits); err != nil {
			return err
		}
	} else {
		current, err := CheckCertificateDate(clientCertPath)
		if err != nil {
			return err
		}
		if !current {
			log.Info("Client certificate is outdated and needs to be regenerated")
			os.Remove(clientKeyPath)
			if err := createCert(authOptions, org, bits); err != nil {
				return err
			}
		}
	}

	return nil
}
