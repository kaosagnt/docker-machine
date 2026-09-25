package provision

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/machine/drivers/fakedriver"
	"github.com/docker/machine/libmachine/auth"
	"github.com/docker/machine/libmachine/cert"
	"github.com/docker/machine/libmachine/engine"
	"github.com/docker/machine/libmachine/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bootstrappedAuthOptions creates a CA and client certificate in a temp
// store, the way `docker-machine create` does before the driver runs.
func bootstrappedAuthOptions(t *testing.T, machineName string) auth.Options {
	root := t.TempDir()
	certDir := filepath.Join(root, "certs")
	machineDir := filepath.Join(root, "machines", machineName)
	require.NoError(t, os.MkdirAll(machineDir, 0o700))

	authOptions := auth.Options{
		CertDir:          certDir,
		CaCertPath:       filepath.Join(certDir, "ca.pem"),
		CaPrivateKeyPath: filepath.Join(certDir, "ca-key.pem"),
		ClientCertPath:   filepath.Join(certDir, "cert.pem"),
		ClientKeyPath:    filepath.Join(certDir, "key.pem"),
		ServerCertPath:   filepath.Join(machineDir, "server.pem"),
		ServerKeyPath:    filepath.Join(machineDir, "server-key.pem"),
		StorePath:        machineDir,
	}
	require.NoError(t, cert.BootstrapCertificates(&authOptions))
	return authOptions
}

func parseCert(t *testing.T, pemBytes []byte) *x509.Certificate {
	block, _ := pem.Decode(pemBytes)
	require.NotNil(t, block)
	c, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return c
}

func TestNewTLSBootstrap(t *testing.T) {
	const machineName = "runner-abc-123"
	authOptions := bootstrappedAuthOptions(t, machineName)
	authOptions.ServerCertSANs = []string{"docker.example.test"}
	driver := &fakedriver.Driver{MockName: machineName}
	engineOptions := engine.Options{
		RegistryMirror: []string{"https://mirror.gcr.io"},
		Labels:         []string{"shard=x"},
	}

	b, err := NewTLSBootstrap(driver, authOptions, engineOptions, swarm.Options{})
	require.NoError(t, err)

	caCert, err := os.ReadFile(authOptions.CaCertPath)
	require.NoError(t, err)
	assert.Equal(t, caCert, b.CACert)

	serverCert := parseCert(t, b.ServerCert)
	assert.ElementsMatch(t, []string{machineName, "localhost", "docker.example.test"}, serverCert.DNSNames)
	assert.Empty(t, serverCert.IPAddresses, "no address is known before the machine exists")
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(b.CACert))
	_, err = serverCert.Verify(x509.VerifyOptions{Roots: roots, DNSName: machineName})
	require.NoError(t, err)
	_, err = serverCert.Verify(x509.VerifyOptions{Roots: roots, DNSName: "runner-other"})
	require.Error(t, err)

	serverKey, err := os.ReadFile(authOptions.ServerKeyPath)
	require.NoError(t, err)
	assert.Equal(t, serverKey, b.ServerKey)

	dropin := string(b.DaemonDropin)
	assert.Contains(t, dropin, "[Service]\nExecStart=\nExecStart=/usr/bin/dockerd -H tcp://0.0.0.0:2376 -H unix:///var/run/docker.sock --tlsverify --tlscacert /etc/docker/ca.pem --tlscert /etc/docker/server.pem --tlskey /etc/docker/server-key.pem")
	assert.Contains(t, dropin, "--label shard=x")
	assert.Contains(t, dropin, "--label provider=Driver")
	assert.Contains(t, dropin, "--registry-mirror https://mirror.gcr.io")

	// The client material is copied next to the server certificate, where
	// `docker-machine env` and the runner look for it.
	for _, name := range []string{"ca.pem", "cert.pem", "key.pem"} {
		_, err := os.Stat(filepath.Join(authOptions.StorePath, name))
		assert.NoError(t, err, name)
	}
}

func TestGenerateServerCertIncludesMachineName(t *testing.T) {
	const machineName = "runner-abc-123"
	authOptions := bootstrappedAuthOptions(t, machineName)

	require.NoError(t, generateServerCert(authOptions, machineName, false, "10.0.0.7"))

	serverCertPEM, err := os.ReadFile(authOptions.ServerCertPath)
	require.NoError(t, err)
	serverCert := parseCert(t, serverCertPEM)
	assert.ElementsMatch(t, []string{machineName, "localhost"}, serverCert.DNSNames)
	require.Len(t, serverCert.IPAddresses, 1)
	assert.Equal(t, "10.0.0.7", serverCert.IPAddresses[0].String())
}
