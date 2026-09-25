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
	// A booting COS machine drops packets to the port until its iptables
	// rule is in, so dials time out rather than get refused.
	tlsDialTimeout = 2 * time.Second
)

// prepareTLSBootstrap generates the TLS material and hands it to a driver
// that delivers it at create time. Returns whether it did, in which case the
// SSH provisioner is skipped.
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
// until it succeeds or the timeout passes. A certificate that fails
// verification ends the wait at once.
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
