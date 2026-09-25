package libmachine

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/docker/machine/libmachine/cert"
	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/host"
	"github.com/docker/machine/libmachine/log"
	"github.com/docker/machine/libmachine/provision"
)

const (
	tlsWaitTimeout  = 5 * time.Minute
	tlsWaitInterval = time.Second
	// tlsDialTimeout is short because a booting machine drops the SYN
	// until its firewall opens the port, and a long dial would hide when
	// the port does open.
	tlsDialTimeout = 2 * time.Second
)

// prepareTLSBootstrap asks the driver whether it delivers the TLS material
// at create time and, if so, generates it and hands it over. Returns whether
// the machine is bootstrapped that way, so the caller knows to skip the SSH
// provisioner.
func prepareTLSBootstrap(h *host.Host) (bool, error) {
	bootstrapper, ok := h.Driver.(drivers.TLSBootstrapper)
	if !ok {
		return false, nil
	}
	requested, err := bootstrapper.TLSBootstrapRequested()
	if err != nil || !requested {
		return false, err
	}

	log.WithField("phase", "tls_bootstrap").Info("Generating the server certificate and Docker configuration...")

	h.HostOptions.AuthOptions.ServerName = h.Name
	b, err := provision.NewTLSBootstrap(h.Driver, *h.HostOptions.AuthOptions, *h.HostOptions.EngineOptions, *h.HostOptions.SwarmOptions)
	if err != nil {
		return false, err
	}

	return true, bootstrapper.SetTLSBootstrap(b)
}

// waitForTLS retries a TLS handshake against the machine's Docker port
// until the server certificate verifies, the timeout passes, or the server
// presents a certificate that fails verification (which no retry fixes).
func waitForTLS(h *host.Host, timeout time.Duration) error {
	dockerURL, err := h.Driver.GetURL()
	if err != nil {
		return err
	}
	u, err := url.Parse(dockerURL)
	if err != nil {
		return err
	}

	tlsConfig, err := cert.ReadTLSConfig(u.Host, h.AuthOptions())
	if err != nil {
		return err
	}

	dialer := &net.Dialer{Timeout: tlsDialTimeout}
	deadline := time.Now().Add(timeout)
	for {
		conn, err := tls.DialWithDialer(dialer, "tcp", u.Host, tlsConfig)
		if err == nil {
			conn.Close()
			return nil
		}

		var certErr *tls.CertificateVerificationError
		if errors.As(err, &certErr) {
			return fmt.Errorf("the certificate presented on %s does not verify for %q: %w", u.Host, tlsConfig.ServerName, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no TLS handshake on %s after %s: %w", u.Host, timeout, err)
		}

		log.Debugf("TLS handshake on %s not up yet: %s", u.Host, err)
		time.Sleep(tlsWaitInterval)
	}
}
