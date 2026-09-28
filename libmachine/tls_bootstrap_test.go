package libmachine

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/machine/drivers/fakedriver"
	"github.com/docker/machine/libmachine/auth"
	"github.com/docker/machine/libmachine/cert"
	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/engine"
	"github.com/docker/machine/libmachine/host"
	"github.com/docker/machine/libmachine/state"
	"github.com/docker/machine/libmachine/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tlsBootstrapDriver records what libmachine hands over and points at the
// test server.
type tlsBootstrapDriver struct {
	*fakedriver.Driver
	requested bool
	setErr    error
	bootstrap *drivers.TLSBootstrap
}

func (d *tlsBootstrapDriver) TLSBootstrapRequested() (bool, error) { return d.requested, nil }
func (d *tlsBootstrapDriver) GetURL() (string, error)              { return "tcp://" + d.MockIP, nil }
func (d *tlsBootstrapDriver) SetTLSBootstrap(b drivers.TLSBootstrap) error {
	if d.setErr != nil {
		return d.setErr
	}
	d.bootstrap = &b
	return nil
}

func newBootstrappedHost(t *testing.T, machineName string) (*host.Host, *tlsBootstrapDriver) {
	root := t.TempDir()
	certDir := filepath.Join(root, "certs")
	machineDir := filepath.Join(root, "machines", machineName)
	require.NoError(t, os.MkdirAll(machineDir, 0o700))

	authOptions := &auth.Options{
		CertDir:          certDir,
		CaCertPath:       filepath.Join(certDir, "ca.pem"),
		CaPrivateKeyPath: filepath.Join(certDir, "ca-key.pem"),
		ClientCertPath:   filepath.Join(certDir, "cert.pem"),
		ClientKeyPath:    filepath.Join(certDir, "key.pem"),
		ServerCertPath:   filepath.Join(machineDir, "server.pem"),
		ServerKeyPath:    filepath.Join(machineDir, "server-key.pem"),
		StorePath:        machineDir,
	}
	require.NoError(t, cert.BootstrapCertificates(authOptions))

	driver := &tlsBootstrapDriver{
		Driver:    &fakedriver.Driver{MockName: machineName, MockState: state.Running, MockIP: "127.0.0.1"},
		requested: true,
	}
	h := &host.Host{
		Name:   machineName,
		Driver: driver,
		HostOptions: &host.Options{
			AuthOptions:   authOptions,
			EngineOptions: &engine.Options{},
			SwarmOptions:  &swarm.Options{},
		},
	}
	return h, driver
}

// serveTLS answers /_ping on a loopback port with the given server
// certificate, accepting clients signed by clientCA, and points the driver
// at it.
func serveTLS(t *testing.T, driver *tlsBootstrapDriver, b *drivers.TLSBootstrap, clientCA []byte) {
	keypair, err := tls.X509KeyPair(b.ServerCert, b.ServerKey)
	require.NoError(t, err)
	clientCAs := x509.NewCertPool()
	require.True(t, clientCAs.AppendCertsFromPEM(clientCA))

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{keypair},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
	})
	require.NoError(t, err)

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_ping" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, "OK")
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	driver.MockIP = ln.Addr().String()
}

func TestPrepareTLSBootstrap(t *testing.T) {
	t.Run("driver without support", func(t *testing.T) {
		h, _ := newBootstrappedHost(t, "m")
		h.Driver = &fakedriver.Driver{MockName: "m"}
		enabled, err := prepareTLSBootstrap(h)
		require.NoError(t, err)
		assert.False(t, enabled)
		assert.Empty(t, h.HostOptions.AuthOptions.ServerName)
	})

	t.Run("driver not requesting it", func(t *testing.T) {
		h, driver := newBootstrappedHost(t, "m")
		driver.requested = false
		enabled, err := prepareTLSBootstrap(h)
		require.NoError(t, err)
		assert.False(t, enabled)
		assert.Nil(t, driver.bootstrap)
		assert.Empty(t, h.HostOptions.AuthOptions.ServerName)
	})

	t.Run("driver rejecting it", func(t *testing.T) {
		h, driver := newBootstrappedHost(t, "m")
		driver.setErr = errors.New("no")
		_, err := prepareTLSBootstrap(h)
		require.ErrorContains(t, err, "no")
		assert.Empty(t, h.HostOptions.AuthOptions.ServerName)
	})

	t.Run("driver requesting it", func(t *testing.T) {
		h, driver := newBootstrappedHost(t, "m")
		enabled, err := prepareTLSBootstrap(h)
		require.NoError(t, err)
		assert.True(t, enabled)
		require.NotNil(t, driver.bootstrap)
		assert.NotEmpty(t, driver.bootstrap.ServerCert)
		assert.Equal(t, "m", h.HostOptions.AuthOptions.ServerName)
	})
}

func TestWaitForTLS(t *testing.T) {
	t.Run("handshake with the machine's certificate", func(t *testing.T) {
		h, driver := newBootstrappedHost(t, "m")
		_, err := prepareTLSBootstrap(h)
		require.NoError(t, err)
		serveTLS(t, driver, driver.bootstrap, driver.bootstrap.CACert)

		require.NoError(t, waitForTLS(h, 5*time.Second))
	})

	t.Run("client certificate rejected fails without waiting", func(t *testing.T) {
		other, _ := newBootstrappedHost(t, "other")
		otherCA, err := os.ReadFile(other.HostOptions.AuthOptions.CaCertPath)
		require.NoError(t, err)

		h, driver := newBootstrappedHost(t, "m")
		_, err = prepareTLSBootstrap(h)
		require.NoError(t, err)
		serveTLS(t, driver, driver.bootstrap, otherCA)

		start := time.Now()
		err = waitForTLS(h, time.Minute)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rejected the connection")
		assert.Less(t, time.Since(start), 10*time.Second)
	})

	t.Run("certificate for another machine fails without waiting", func(t *testing.T) {
		other, otherDriver := newBootstrappedHost(t, "other")
		_, err := prepareTLSBootstrap(other)
		require.NoError(t, err)

		// Same CA, but the server presents the certificate issued for "other".
		h, driver := newBootstrappedHost(t, "m")
		h.HostOptions.AuthOptions = other.HostOptions.AuthOptions
		h.HostOptions.AuthOptions.ServerName = "m"
		serveTLS(t, driver, otherDriver.bootstrap, otherDriver.bootstrap.CACert)

		start := time.Now()
		err = waitForTLS(h, time.Minute)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `does not verify for "m"`)
		assert.Less(t, time.Since(start), 10*time.Second)
	})

	t.Run("nothing listening times out", func(t *testing.T) {
		h, driver := newBootstrappedHost(t, "m")
		_, err := prepareTLSBootstrap(h)
		require.NoError(t, err)

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		driver.MockIP = ln.Addr().String()
		ln.Close()

		err = waitForTLS(h, 2*time.Second)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no TLS handshake")
	})
}
