package provision

import (
	"github.com/docker/machine/libmachine/drivers"
)

func init() {
	Register("Amazon Linux 2", &RegisteredProvisioner{
		New: NewAmazonLinuxProvisioner,
	})
}

func NewAmazonLinuxProvisioner(d drivers.Driver) Provisioner {
	return &AmazonLinuxProvisioner{
		NewRedHatProvisioner("amzn", d),
	}
}

type AmazonLinuxProvisioner struct {
	*RedHatProvisioner
}

func (provisioner *AmazonLinuxProvisioner) String() string {
	return "amzn"
}
