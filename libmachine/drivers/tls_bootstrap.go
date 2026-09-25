package drivers

import "errors"

var ErrTLSBootstrapNotSupported = errors.New("driver does not support delivering the TLS bootstrap at create time")

// TLSBootstrap is what dockerd on the machine needs to serve TLS: the CA,
// the server keypair, and the systemd drop-in that adds the TLS listener.
type TLSBootstrap struct {
	CACert       []byte
	ServerCert   []byte
	ServerKey    []byte
	DaemonDropin []byte
}

// TLSBootstrapper is implemented by drivers that can deliver a TLSBootstrap to
// the machine as part of Create, so nothing has to be copied over SSH after
// the machine is up.
type TLSBootstrapper interface {
	// TLSBootstrapRequested reports whether the driver is configured to
	// deliver the TLS bootstrap at create time.
	TLSBootstrapRequested() (bool, error)

	// SetTLSBootstrap hands the material to the driver. Must be called
	// before Create.
	SetTLSBootstrap(b TLSBootstrap) error
}
