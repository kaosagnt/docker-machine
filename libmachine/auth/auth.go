package auth

type Options struct {
	CertDir              string
	CaCertPath           string
	CaPrivateKeyPath     string
	CaCertRemotePath     string
	ServerCertPath       string
	ServerKeyPath        string
	ClientKeyPath        string
	ServerCertRemotePath string
	ServerKeyRemotePath  string
	ClientCertPath       string
	ServerCertSANs       []string
	// StorePath is left in for historical reasons, but not really meant to
	// be used directly.
	StorePath string
	// BootstrapLock, when true, causes cert.BootstrapCertificates to
	// serialise its work behind an exclusive file lock in CertDir. This
	// defends against the TOCTOU race that occurs when many concurrent
	// `docker-machine create` invocations on a fresh host all find the
	// cert directory empty and race to generate CA / client material,
	// producing files that aren't mutually consistent. Off by default to
	// preserve existing behaviour for all callers.
	BootstrapLock bool
}
