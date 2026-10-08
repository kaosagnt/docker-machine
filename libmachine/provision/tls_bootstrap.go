package provision

import (
	"fmt"
	"os"

	"github.com/docker/machine/libmachine/auth"
	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/engine"
	"github.com/docker/machine/libmachine/swarm"
)

// NewGoogleCOSTLSBootstrap generates the server certificate and the Google
// COS dockerd drop-in for a driver to deliver at create time. The machine
// has no address yet, so the certificate is issued for the machine name and
// clients verify against that (auth.Options.ServerName).
//
// COS ships dockerd, so it is the only image that can be provisioned
// without SSH. A driver for another image needs its own drop-in.
func NewGoogleCOSTLSBootstrap(d drivers.Driver, authOptions auth.Options, engineOptions engine.Options, swarmOptions swarm.Options) (drivers.TLSBootstrap, error) {
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
