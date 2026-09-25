package provision

import (
	"fmt"
	"os"

	"github.com/docker/machine/libmachine/auth"
	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/engine"
	"github.com/docker/machine/libmachine/swarm"
)

// NewTLSBootstrap generates the machine's server certificate and the
// systemd drop-in that starts dockerd with it, for a driver to deliver at
// create time. The machine has no address yet, so the certificate is only
// valid for the machine name and clients have to verify against that
// (auth.Options.ServerName).
//
// The drop-in is the Google COS one. COS is the only image with dockerd
// preinstalled, which is what makes provisioning without SSH possible.
func NewTLSBootstrap(d drivers.Driver, authOptions auth.Options, engineOptions engine.Options, swarmOptions swarm.Options) (drivers.TLSBootstrap, error) {
	var b drivers.TLSBootstrap

	if err := copyCertsToMachineDir(authOptions); err != nil {
		return b, err
	}

	if err := generateServerCert(authOptions, d.GetMachineName(), swarmOptions.Master); err != nil {
		return b, err
	}

	p := &GoogleCOSProvisioner{NewSystemdProvisioner("cos", d)}
	p.AuthOptions = authOptions
	p.AuthOptions = setRemoteAuthOptions(p)
	p.EngineOptions = engineOptions
	p.SwarmOptions = swarmOptions
	dockerOptions, err := p.GenerateDockerOptions(engine.DefaultPort)
	if err != nil {
		return b, fmt.Errorf("generating the Docker daemon drop-in: %w", err)
	}

	for _, f := range []struct {
		path string
		dst  *[]byte
	}{
		{authOptions.CaCertPath, &b.CACert},
		{authOptions.ServerCertPath, &b.ServerCert},
		{authOptions.ServerKeyPath, &b.ServerKey},
	} {
		data, err := os.ReadFile(f.path)
		if err != nil {
			return b, err
		}
		*f.dst = data
	}
	b.DaemonDropin = []byte(dockerOptions.EngineOptions)

	return b, nil
}
