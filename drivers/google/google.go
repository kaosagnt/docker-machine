package google

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/log"
	"github.com/docker/machine/libmachine/mcnflag"
	"github.com/docker/machine/libmachine/ssh"
	"github.com/docker/machine/libmachine/state"
)

type metadataMap map[string]string

type backoffFactory struct {
	InitialInterval     time.Duration
	RandomizationFactor float64
	Multiplier          float64
	MaxInterval         time.Duration
	MaxElapsedTime      time.Duration
}

func (bf *backoffFactory) create() *backoff.ExponentialBackOff {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = bf.InitialInterval
	b.RandomizationFactor = bf.RandomizationFactor
	b.Multiplier = bf.Multiplier
	b.MaxInterval = bf.MaxInterval
	b.MaxElapsedTime = bf.MaxElapsedTime

	return b
}

// Driver is a struct compatible with the docker.hosts.drivers.Driver interface.
type Driver struct {
	*drivers.BaseDriver
	Zone                  string
	MachineType           string
	MinCPUPlatform        string
	MachineImage          string
	DiskType              string
	Address               string
	Network               string
	Subnetwork            string
	Preemptible           bool
	UseInternalIP         bool
	UseInternalIPOnly     bool
	ServiceAccount        string
	Scopes                string
	DiskSize              int
	ProvisionedIops       int
	ProvisionedThroughput int
	Project               string
	Tags                  string
	UseExisting           bool
	OpenPorts             []string
	Labels                []string
	Metadata              metadataMap
	MetadataFromFile      metadataMap
	Accelerator           string
	MaintenancePolicy     string
	SkipFirewall          bool

	// BulkInsert is the explicit opt-in for bulkInsert mode. Separate
	// boolean rather than inferred from Region: keeps the provisioning
	// channel grep-able in config and on-disk Driver state.
	BulkInsert bool

	// Region is required when BulkInsert is true; ignored otherwise.
	// The actual zone is chosen by GCP at create time from LocationZones
	// and written back to ResolvedZone after placement.
	Region string

	// FlexMachineTypes is the ranked list of GCE machine types for
	// InstanceFlexibilityPolicy.InstanceSelections. First entry =
	// rank 0; GCP falls back to the next on capacity errors. Bare
	// machine-type names ("n2-standard-2"), no URLs.
	FlexMachineTypes []string

	// LocationZones constrains zone selection. Each entry is
	// "zone[:PREFERENCE]" (ALLOW / PREFERRED / DENY); empty means GCP
	// picks any zone in Region.
	LocationZones []string

	// ResolvedZone and ResolvedMachineType carry the values GCP actually
	// picked, populated by syncDriverStateFromInstance after a successful
	// create. Empty in direct mode (Zone / MachineType are reality there).
	// ComputeUtil construction prefers ResolvedZone over Zone so per-
	// instance operations target the right zone after reload from disk;
	// gitlab-runner!6740 reads ResolvedMachineType for the target_*
	// metric labels.
	ResolvedZone        string
	ResolvedMachineType string

	OperationBackoffFactory *backoffFactory
}

const (
	defaultZone              = "us-central1-a"
	defaultUser              = "ubuntu"
	defaultMachineType       = "n1-standard-1"
	defaultImageName         = "ubuntu-os-cloud/global/images/ubuntu-2204-jammy-v20250815"
	defaultServiceAccount    = "default"
	defaultScopes            = "https://www.googleapis.com/auth/devstorage.read_only,https://www.googleapis.com/auth/logging.write,https://www.googleapis.com/auth/monitoring.write"
	defaultDiskType          = "pd-standard"
	defaultDiskSize          = 10
	defaultNetwork           = "default"
	defaultSubnetwork        = ""
	defaultMinCPUPlatform    = ""
	defaultAccelerator       = ""
	defaultMaintenancePolicy = ""

	defaultGoogleOperationBackoffInitialInterval     = 1
	defaultGoogleOperationBackoffRandomizationFactor = "0.5"
	defaultGoogleOperationBackoffMultipler           = "2"
	defaultGoogleOperationBackoffMaxInterval         = 30
	defaultGoogleOperationBackoffMaxElapsedTime      = 300
)

// GetCreateFlags registers the flags this driver adds to
// "docker hosts create"
func (d *Driver) GetCreateFlags() []mcnflag.Flag {
	return []mcnflag.Flag{
		mcnflag.StringFlag{
			Name:   "google-zone",
			Usage:  "GCE Zone",
			Value:  defaultZone,
			EnvVar: "GOOGLE_ZONE",
		},
		mcnflag.StringFlag{
			Name:   "google-machine-type",
			Usage:  "GCE Machine Type",
			Value:  defaultMachineType,
			EnvVar: "GOOGLE_MACHINE_TYPE",
		},
		mcnflag.StringFlag{
			Name:   "google-min-cpu-platform",
			Usage:  "Minimal CPU Platform for created VM (use friendly name e.g. 'Intel Sandy Bridge')",
			EnvVar: "GOOGLE_MIN_CPU_PLATFORM",
			Value:  defaultMinCPUPlatform,
		},
		mcnflag.StringFlag{
			Name:   "google-machine-image",
			Usage:  "GCE Machine Image Absolute URL",
			Value:  defaultImageName,
			EnvVar: "GOOGLE_MACHINE_IMAGE",
		},
		mcnflag.StringFlag{
			Name:   "google-username",
			Usage:  "GCE User Name",
			Value:  defaultUser,
			EnvVar: "GOOGLE_USERNAME",
		},
		mcnflag.StringFlag{
			Name:   "google-project",
			Usage:  "GCE Project",
			EnvVar: "GOOGLE_PROJECT",
		},
		mcnflag.StringFlag{
			Name:   "google-service-account",
			Usage:  "GCE Service Account for the VM (email address)",
			Value:  defaultServiceAccount,
			EnvVar: "GOOGLE_SERVICE_ACCOUNT",
		},
		mcnflag.StringFlag{
			Name:   "google-scopes",
			Usage:  "GCE Scopes (comma-separated if multiple scopes)",
			Value:  defaultScopes,
			EnvVar: "GOOGLE_SCOPES",
		},
		mcnflag.IntFlag{
			Name:   "google-disk-size",
			Usage:  "GCE Instance Disk Size (in GB)",
			Value:  defaultDiskSize,
			EnvVar: "GOOGLE_DISK_SIZE",
		},
		mcnflag.StringFlag{
			Name:   "google-disk-type",
			Usage:  "GCE Instance Disk type",
			Value:  defaultDiskType,
			EnvVar: "GOOGLE_DISK_TYPE",
		},
		mcnflag.IntFlag{
			Name:   "google-provisioned-iops",
			Usage:  "GCE Hyperdisk provisioned IOPS (applies to Hyperdisk disk types; not consumed by non-Hyperdisk types)",
			EnvVar: "GOOGLE_PROVISIONED_IOPS",
		},
		mcnflag.IntFlag{
			Name:   "google-provisioned-throughput",
			Usage:  "GCE Hyperdisk provisioned throughput in MiB/s (applies to Hyperdisk disk types; not consumed by non-Hyperdisk types)",
			EnvVar: "GOOGLE_PROVISIONED_THROUGHPUT",
		},
		mcnflag.StringFlag{
			Name:   "google-network",
			Usage:  "Specify network in which to provision vm",
			Value:  defaultNetwork,
			EnvVar: "GOOGLE_NETWORK",
		},
		mcnflag.StringFlag{
			Name:   "google-subnetwork",
			Usage:  "Specify subnetwork in which to provision vm",
			Value:  defaultSubnetwork,
			EnvVar: "GOOGLE_SUBNETWORK",
		},
		mcnflag.StringFlag{
			Name:   "google-address",
			Usage:  "GCE Instance External IP",
			EnvVar: "GOOGLE_ADDRESS",
		},
		mcnflag.BoolFlag{
			Name:   "google-preemptible",
			Usage:  "GCE Instance Preemptibility",
			EnvVar: "GOOGLE_PREEMPTIBLE",
		},
		mcnflag.StringFlag{
			Name:   "google-tags",
			Usage:  "GCE Instance Tags (comma-separated)",
			EnvVar: "GOOGLE_TAGS",
			Value:  "",
		},
		mcnflag.BoolFlag{
			Name:   "google-use-internal-ip",
			Usage:  "Use internal GCE Instance IP rather than public one",
			EnvVar: "GOOGLE_USE_INTERNAL_IP",
		},
		mcnflag.BoolFlag{
			Name:   "google-use-internal-ip-only",
			Usage:  "Configure GCE instance to not have an external IP address",
			EnvVar: "GOOGLE_USE_INTERNAL_IP_ONLY",
		},
		mcnflag.BoolFlag{
			Name:   "google-use-existing",
			Usage:  "Don't create a new VM, use an existing one",
			EnvVar: "GOOGLE_USE_EXISTING",
		},
		mcnflag.StringSliceFlag{
			Name:  "google-open-port",
			Usage: "Make the specified port number accessible from the Internet, e.g, 8080/tcp",
		},
		mcnflag.IntFlag{
			Name:  "google-operation-backoff-initial-interval",
			Usage: "Initial interval for GCP Operation check exponential backoff",
			Value: defaultGoogleOperationBackoffInitialInterval,
		},
		mcnflag.StringFlag{
			Name:  "google-operation-backoff-randomization-factor",
			Usage: "Randomization factor for GCP Operation check exponential backoff",
			Value: defaultGoogleOperationBackoffRandomizationFactor,
		},
		mcnflag.StringFlag{
			Name:  "google-operation-backoff-multipler",
			Usage: "Multipler factor for GCP Operation check exponential backoff",
			Value: defaultGoogleOperationBackoffMultipler,
		},
		mcnflag.IntFlag{
			Name:  "google-operation-backoff-max-interval",
			Usage: "Maximum interval for GCP Operation check exponential backoff",
			Value: defaultGoogleOperationBackoffMaxInterval,
		},
		mcnflag.IntFlag{
			Name:  "google-operation-backoff-max-elapsed-time",
			Usage: "Maximum elapsed time for GCP Operation check exponential backoff",
			Value: defaultGoogleOperationBackoffMaxElapsedTime,
		},
		mcnflag.StringSliceFlag{
			Name:  "google-label",
			Usage: "Label to set on the VM and its disks (format: key:value). Repeat the flag to set more labels",
		},
		mcnflag.StringSliceFlag{
			Name:  "google-metadata",
			Usage: "Custom metadata value passed in key=value form. Use multiple times for multiple settings",
		},
		mcnflag.StringSliceFlag{
			Name:  "google-metadata-from-file",
			Usage: "Path to a file containing the metadata value inform of key=path/to/file. Use multiple times for multiple settings",
		},
		mcnflag.StringFlag{
			Name:   "google-accelerator",
			Usage:  "Count and specific type of GPU accelerators (format: count=N,type=type) to attach to the instance, e.g. count=1,type=nvidia-tesla-p100",
			EnvVar: "GOOGLE_ACCELERATOR",
			Value:  defaultAccelerator,
		},
		mcnflag.StringFlag{
			Name:   "google-maintenance-policy",
			Usage:  "Defines the maintenance behavior for this instance, e.g, MIGRATE or TERMINATE",
			EnvVar: "GOOGLE_MAINTENANCE_POLICY",
			Value:  defaultMaintenancePolicy,
		},
		mcnflag.BoolFlag{
			Name:   "google-skip-firewall-create",
			Usage:  "Skip firewall setup",
			EnvVar: "GOOGLE_SKIP_FIREWALL_CREATE",
		},
		mcnflag.BoolFlag{
			Name:   "google-bulk-insert",
			Usage:  "(Experimental) Provision via RegionInstances.BulkInsert with LocationPolicy / InstanceFlexibilityPolicy rather than zonal Instances.Insert. Requires --google-region; mutually exclusive with --google-zone.",
			EnvVar: "GOOGLE_BULK_INSERT",
		},
		mcnflag.StringFlag{
			Name:   "google-region",
			Usage:  "(Experimental) GCP region for bulkInsert mode. Required when --google-bulk-insert is set.",
			EnvVar: "GOOGLE_REGION",
		},
		mcnflag.StringSliceFlag{
			Name:   "google-flex-machine-type",
			Usage:  "(Experimental) GCE machine type for bulkInsert flex policy. Repeat in preference order: first occurrence is rank 0 (most preferred). Bare machine-type names (e.g. n2-standard-2), no URLs. Requires --google-region.",
			EnvVar: "GOOGLE_FLEX_MACHINE_TYPE",
		},
		mcnflag.StringSliceFlag{
			Name:   "google-location-zone",
			Usage:  "(Experimental) Zone constraint for bulkInsert. Format: zone[:PREFERENCE] where preference is ALLOW (default), PREFERRED, or DENY. Repeat per zone. Empty = any zone in --google-region.",
			EnvVar: "GOOGLE_LOCATION_ZONE",
		},
	}
}

// NewDriver creates a Driver with the specified storePath.
func NewDriver(machineName string, storePath string) *Driver {
	return &Driver{
		Zone:           defaultZone,
		DiskType:       defaultDiskType,
		DiskSize:       defaultDiskSize,
		MachineType:    defaultMachineType,
		MachineImage:   defaultImageName,
		Network:        defaultNetwork,
		Subnetwork:     defaultSubnetwork,
		ServiceAccount: defaultServiceAccount,
		MinCPUPlatform: defaultMinCPUPlatform,
		Scopes:         defaultScopes,
		BaseDriver: &drivers.BaseDriver{
			SSHUser:     defaultUser,
			MachineName: machineName,
			StorePath:   storePath,
		},
	}
}

// GetSSHHostname returns hostname for use with ssh
func (d *Driver) GetSSHHostname() (string, error) {
	return d.GetIP()
}

// GetSSHUsername returns username for use with ssh
func (d *Driver) GetSSHUsername() string {
	if d.SSHUser == "" {
		d.SSHUser = "docker-user"
	}
	return d.SSHUser
}

// DriverName returns the name of the driver
func (d *Driver) DriverName() string {
	return "google"
}

// SetConfigFromFlags initializes the driver based on the command line flags.
func (d *Driver) SetConfigFromFlags(flags drivers.DriverOptions) error {
	d.Project = flags.String("google-project")
	if d.Project == "" {
		return errors.New("no Google Cloud Project name specified (--google-project)")
	}

	d.Zone = flags.String("google-zone")
	d.UseExisting = flags.Bool("google-use-existing")
	if !d.UseExisting {
		d.MachineType = flags.String("google-machine-type")
		d.MinCPUPlatform = flags.String("google-min-cpu-platform")
		d.MachineImage = flags.String("google-machine-image")
		d.MachineImage = strings.TrimPrefix(d.MachineImage, "https://www.googleapis.com/compute/v1/projects/")
		d.DiskSize = flags.Int("google-disk-size")
		d.DiskType = flags.String("google-disk-type")
		provisionedIops := flags.Int("google-provisioned-iops")
		if provisionedIops < 0 {
			return fmt.Errorf("google-provisioned-iops must be >= 0, got %d", provisionedIops)
		}
		provisionedThroughput := flags.Int("google-provisioned-throughput")
		if provisionedThroughput < 0 {
			return fmt.Errorf("google-provisioned-throughput must be >= 0, got %d", provisionedThroughput)
		}
		d.ProvisionedIops = provisionedIops
		d.ProvisionedThroughput = provisionedThroughput
		d.Address = flags.String("google-address")
		d.Network = flags.String("google-network")
		d.Subnetwork = flags.String("google-subnetwork")
		d.Preemptible = flags.Bool("google-preemptible")
		d.UseInternalIP = flags.Bool("google-use-internal-ip") || flags.Bool("google-use-internal-ip-only")
		d.UseInternalIPOnly = flags.Bool("google-use-internal-ip-only")
		d.ServiceAccount = flags.String("google-service-account")
		d.Scopes = flags.String("google-scopes")
		d.Tags = flags.String("google-tags")
		d.OpenPorts = flags.StringSlice("google-open-port")
		d.Labels = flags.StringSlice("google-label")
		d.Metadata = metadataMapFromStringSlice(flags.StringSlice("google-metadata"))
		d.MetadataFromFile = metadataMapFromStringSlice(flags.StringSlice("google-metadata-from-file"))
		d.Accelerator = flags.String("google-accelerator")
		d.MaintenancePolicy = flags.String("google-maintenance-policy")
		d.SkipFirewall = flags.Bool("google-skip-firewall-create")
	}
	d.SSHUser = flags.String("google-username")
	d.SSHPort = 22
	d.SetSwarmConfigFromFlags(flags)

	d.BulkInsert = flags.Bool("google-bulk-insert")
	d.Region = flags.String("google-region")
	d.FlexMachineTypes = flags.StringSlice("google-flex-machine-type")
	d.LocationZones = flags.StringSlice("google-location-zone")

	if d.BulkInsert {
		if d.UseExisting {
			return errors.New("--google-bulk-insert and --google-use-existing are mutually exclusive: bulkInsert provisions a new VM")
		}
		if d.Region == "" {
			return errors.New("--google-bulk-insert requires --google-region")
		}
		if d.Zone != "" && d.Zone != defaultZone {
			return errors.New("--google-bulk-insert and --google-zone are mutually exclusive: bulkInsert picks the zone from --google-location-zone")
		}
		if d.Address != "" {
			return errors.New("--google-address is not supported with --google-bulk-insert: bulkInsert does not accept custom external IPs")
		}
		for _, entry := range d.FlexMachineTypes {
			if strings.TrimSpace(entry) == "" {
				return errors.New("--google-flex-machine-type entries must be non-empty machine-type names")
			}
		}
		for _, entry := range d.LocationZones {
			zone, _ := parseLocationZoneEntry(entry)
			if strings.TrimSpace(zone) == "" {
				return fmt.Errorf("--google-location-zone entry %q has an empty zone (expected zone[:PREFERENCE])", entry)
			}
		}
	} else {
		if len(d.FlexMachineTypes) > 0 {
			return errors.New("--google-flex-machine-type requires --google-bulk-insert")
		}
		if len(d.LocationZones) > 0 {
			return errors.New("--google-location-zone requires --google-bulk-insert")
		}
	}

	backoffRandomizationFactor, err := strconv.ParseFloat(flags.String("google-operation-backoff-randomization-factor"), 64)
	if err != nil {
		return fmt.Errorf("error while parsing google-operation-backoff-randomization-factor value: %v", err)
	}

	backoffMultipler, err := strconv.ParseFloat(flags.String("google-operation-backoff-multipler"), 64)
	if err != nil {
		return fmt.Errorf("error while parsing google-operation-backoff-multipler value: %v", err)
	}

	d.OperationBackoffFactory = &backoffFactory{
		InitialInterval:     time.Duration(flags.Int("google-operation-backoff-initial-interval")) * time.Second,
		RandomizationFactor: backoffRandomizationFactor,
		Multiplier:          backoffMultipler,
		MaxInterval:         time.Duration(flags.Int("google-operation-backoff-max-interval")) * time.Second,
		MaxElapsedTime:      time.Duration(flags.Int("google-operation-backoff-max-elapsed-time")) * time.Second,
	}

	return nil
}

func metadataMapFromStringSlice(slice []string) metadataMap {
	result := make(metadataMap, 0)
	for _, element := range slice {
		parts := strings.SplitN(element, "=", 2)
		if len(parts) != 2 {
			continue
		}
		result[parts[0]] = parts[1]
	}

	return result
}

// PreCreateCheck is called to enforce pre-creation steps
func (d *Driver) PreCreateCheck() error {
	c, err := newComputeUtil(d)
	if err != nil {
		return err
	}

	// Check that the project exists. It will also check the credentials
	// at the same time.
	log.Infof("Check that the project exists")

	if _, err = c.service.Projects.Get(d.Project).Do(); err != nil {
		return fmt.Errorf("Project with ID %q not found. %v", d.Project, err)
	}

	// Check if the instance already exists. There will be an error if the instance
	// doesn't exist, so just check instance for nil.
	log.Infof("Check if the instance already exists")

	instance, _ := c.instance()
	if d.UseExisting {
		if instance == nil {
			return fmt.Errorf("unable to find instance %q in zone %q", d.MachineName, d.Zone)
		}
	} else {
		if instance != nil {
			return fmt.Errorf("instance %q already exists in zone %q", d.MachineName, d.Zone)
		}
	}

	return nil
}

func (d *Driver) usesBulkInsert() bool {
	return d.BulkInsert
}

// Create creates a GCE VM instance acting as a docker host.
func (d *Driver) Create() error {
	log.Infof("Generating SSH Key")

	if err := ssh.GenerateSSHKey(d.GetSSHKeyPath()); err != nil {
		return err
	}

	log.Infof("Creating host...")

	c, err := newComputeUtil(d)
	if err != nil {
		return err
	}

	if err := c.openFirewallPorts(d); err != nil {
		return err
	}

	if d.UseExisting {
		return c.configureInstance(d)
	}
	return c.createInstance(d)
}

// GetURL returns the URL of the remote docker daemon.
func (d *Driver) GetURL() (string, error) {
	ip, err := d.GetIP()
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("tcp://%s", net.JoinHostPort(ip, "2376")), nil
}

// GetIP returns the IP address of the GCE instance.
func (d *Driver) GetIP() (string, error) {
	if d.IPAddress != "" {
		return d.IPAddress, nil
	}

	c, err := newComputeUtil(d)
	if err != nil {
		return "", err
	}

	ip, err := c.ip()
	if err != nil {
		return "", err
	}
	if ip == "" {
		return "", drivers.ErrHostIsNotRunning
	}

	d.IPAddress = ip

	return ip, nil
}

// GetState returns a docker.hosts.state.State value representing the current state of the host.
func (d *Driver) GetState() (state.State, error) {
	c, err := newComputeUtil(d)
	if err != nil {
		return state.None, err
	}

	// All we care about is whether the disk exists, so we just check disk for a nil value.
	// There will be no error if disk is not nil.
	instance, _ := c.instance()
	if instance == nil {
		disk, _ := c.disk()
		if disk == nil {
			return state.None, nil
		}
		return state.Stopped, nil
	}

	switch instance.Status {
	case "PROVISIONING", "STAGING":
		return state.Starting, nil
	case "RUNNING":
		return state.Running, nil
	case "STOPPING", "STOPPED", "TERMINATED":
		return state.Stopped, nil
	}
	return state.None, nil
}

// Start starts an existing GCE instance or create an instance with an existing disk.
func (d *Driver) Start() error {
	c, err := newComputeUtil(d)
	if err != nil {
		return err
	}

	instance, err := c.instance()
	if err != nil {
		if !isNotFound(err) {
			return err
		}
	}

	if instance == nil {
		// bulkInsert can't reuse the existing disk: a fresh BulkInsert
		// picks a new zone and builds a new disk, orphaning the old one.
		if d.usesBulkInsert() {
			return fmt.Errorf("instance %q not found and --google-bulk-insert mode does not support resurrecting from an existing disk; re-create the machine", d.MachineName)
		}
		if err = c.createInstance(d); err != nil {
			return err
		}
	} else {
		if err := c.startInstance(); err != nil {
			return err
		}
	}

	d.IPAddress, err = d.GetIP()
	return err
}

// Stop stops an existing GCE instance.
func (d *Driver) Stop() error {
	c, err := newComputeUtil(d)
	if err != nil {
		return err
	}

	if err := c.stopInstance(); err != nil {
		return err
	}

	d.IPAddress = ""
	return nil
}

// Restart restarts a machine which is known to be running.
func (d *Driver) Restart() error {
	if err := d.Stop(); err != nil {
		return err
	}

	return d.Start()
}

// Kill stops an existing GCE instance.
func (d *Driver) Kill() error {
	return d.Stop()
}

// Remove deletes the GCE instance and the disk.
func (d *Driver) Remove() error {
	c, err := newComputeUtil(d)
	if err != nil {
		return err
	}

	if err := c.deleteInstance(); err != nil {
		if isNotFound(err) {
			log.Warn("Remote instance does not exist, proceeding with removing local reference")
		} else {
			return err
		}
	}

	if err := c.deleteDisk(); err != nil {
		if isNotFound(err) {
			log.Warn("Remote disk does not exist, proceeding")
		} else {
			return err
		}
	}

	return nil
}
