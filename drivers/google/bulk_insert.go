package google

// bulkInsert provisioning path.
//
// When --google-bulk-insert is set, the usual zonal Instances.Insert
// is replaced by a regional RegionInstances.BulkInsert. The single
// request carries LocationPolicy (zone preference, from
// --google-location-zone) and InstanceFlexibilityPolicy (ranked
// machine-type fallback, from --google-flex-machine-type) so GCP
// picks zone × machine-type per call.
//
// The Operation returned by BulkInsert is region-scoped and tells us
// when GCP settled placement, but not where; we discover the chosen
// zone via Instances.AggregatedList before any per-instance follow-up
// (firewall tag, SSH key).

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/docker/machine/libmachine/log"
	raw "google.golang.org/api/compute/v1"
)

var zonePathSegment = regexp.MustCompile(`/zones/([^/]+)`)
var machineTypePathSegment = regexp.MustCompile(`/machineTypes/([^/]+)$`)

// createInstanceViaBulkInsert provisions a single VM via
// RegionInstances.BulkInsert with the configured location and flex
// policies, waits for the operation to complete, discovers the chosen
// zone, and hands off to the standard post-create configuration.
func (c *ComputeUtil) createInstanceViaBulkInsert(d *Driver) error {
	c.warnAboutMachineTypeInBulkInsertMode(d)

	log.Infof("Creating instance via bulkInsert in %q", c.region())

	props, err := c.buildBulkInsertInstanceProperties(d)
	if err != nil {
		return fmt.Errorf("building instance properties for bulkInsert: %w", err)
	}

	req := &raw.BulkInsertInstanceResource{
		Count:    1,
		MinCount: 1,
		PerInstanceProperties: map[string]raw.BulkInsertInstanceResourcePerInstanceProperties{
			c.instanceName: {},
		},
		InstanceProperties: props,
	}
	if policy := c.buildLocationPolicy(); policy != nil {
		req.LocationPolicy = policy
	}
	if flex := c.buildInstanceFlexibilityPolicy(); flex != nil {
		req.InstanceFlexibilityPolicy = flex
	}

	op, err := c.service.RegionInstances.BulkInsert(c.project, c.region(), req).Do()
	if err != nil {
		return fmt.Errorf("bulkInsert rejected create for %q in %q: %w", c.instanceName, c.region(), err)
	}

	log.Infof("Waiting for bulkInsert operation %s", op.Name)
	if err := c.waitForRegionOp(op.Name); err != nil {
		return fmt.Errorf("bulkInsert for %q did not complete: %w", c.instanceName, err)
	}

	// BulkInsert's Operation TargetLink doesn't carry placement; look
	// the instance up by name to find the zone for subsequent calls.
	zone, err := c.discoverInstanceZone()
	if err != nil {
		return fmt.Errorf("discovering zone for bulkInsert-placed instance %q: %w", c.instanceName, err)
	}
	c.zone = zone
	d.ResolvedZone = zone
	c.zoneURL = apiURL + c.project + "/zones/" + zone

	return c.finishPostCreate(d)
}

// buildBulkInsertInstanceProperties mirrors the direct-path Instances.Insert
// body as an InstanceProperties (no Name / Zone — bulkInsert envelope
// owns those).
func (c *ComputeUtil) buildBulkInsertInstanceProperties(d *Driver) (*raw.InstanceProperties, error) {
	var net string
	if strings.Contains(d.Network, "/networks/") {
		net = d.Network
	} else {
		net = c.globalURL + "/networks/" + d.Network
	}

	metadata, err := prepareMetadata(d)
	if err != nil {
		return nil, err
	}

	// MachineType is a bare name (no zonal URL); bulkInsert resolves it
	// against the placement zone, and flex selections override it.
	props := &raw.InstanceProperties{
		Description:    "docker host vm",
		MachineType:    d.MachineType,
		MinCpuPlatform: c.minCPUPlatform,
		Disks: []*raw.AttachedDisk{
			{
				Boot:       true,
				AutoDelete: true,
				Type:       "PERSISTENT",
				Mode:       "READ_WRITE",
				InitializeParams: &raw.AttachedDiskInitializeParams{
					SourceImage: "https://www.googleapis.com/compute/v1/projects/" + d.MachineImage,
					DiskSizeGb:  int64(d.DiskSize),
					// Bare disk-type name (zonal URL would over-constrain
					// or be rejected by bulkInsert).
					DiskType: c.diskTypeURL,
					Labels:   parseLabels(d),
				},
			},
		},
		NetworkInterfaces: []*raw.NetworkInterface{
			{Network: net},
		},
		Tags: &raw.Tags{
			Items: parseTags(d),
		},
		ServiceAccounts: []*raw.ServiceAccount{
			{
				Email:  d.ServiceAccount,
				Scopes: strings.Split(d.Scopes, ","),
			},
		},
		Scheduling: &raw.Scheduling{
			Preemptible: c.preemptible,
		},
		Labels:   parseLabels(d),
		Metadata: metadata,
	}

	if d.ProvisionedIops > 0 {
		props.Disks[0].InitializeParams.ProvisionedIops = int64(d.ProvisionedIops)
	}
	if d.ProvisionedThroughput > 0 {
		props.Disks[0].InitializeParams.ProvisionedThroughput = int64(d.ProvisionedThroughput)
	}

	if c.maintenancePolicy != "" {
		props.Scheduling.OnHostMaintenance = c.maintenancePolicy
	}

	if strings.Contains(c.subnetwork, "/subnetworks/") {
		props.NetworkInterfaces[0].Subnetwork = c.subnetwork
	} else if c.subnetwork != "" {
		props.NetworkInterfaces[0].Subnetwork = "projects/" + c.networkProject + "/regions/" + c.region() + "/subnetworks/" + c.subnetwork
	}

	if !c.useInternalIPOnly {
		props.NetworkInterfaces[0].AccessConfigs = append(props.NetworkInterfaces[0].AccessConfigs, &raw.AccessConfig{
			Type: "ONE_TO_ONE_NAT",
		})
	}

	if count, accelType := c.acceleratorCountAndType(); count > 0 && accelType != "" {
		props.GuestAccelerators = []*raw.AcceleratorConfig{
			{
				AcceleratorCount: int64(count),
				AcceleratorType:  accelType,
			},
		}
	}

	return props, nil
}

// buildLocationPolicy returns nil when no zones are configured (GCP
// picks freely within the region). TargetShape is intentionally not
// exposed: we always send count=1, where every shape collapses to
// "pick one zone".
func (c *ComputeUtil) buildLocationPolicy() *raw.LocationPolicy {
	if len(c.locationZones) == 0 {
		return nil
	}

	policy := &raw.LocationPolicy{
		Locations: make(map[string]raw.LocationPolicyLocation, len(c.locationZones)),
	}
	for _, entry := range c.locationZones {
		zone, pref := parseLocationZoneEntry(entry)
		policy.Locations["zones/"+zone] = raw.LocationPolicyLocation{
			Preference: pref,
		}
	}
	return policy
}

// parseLocationZoneEntry splits "zone[:preference]"; defaults to ALLOW.
// Unknown preferences pass through verbatim — GCP rejects them with a
// clear error message rather than us silently coercing.
func parseLocationZoneEntry(entry string) (zone, preference string) {
	if i := strings.IndexByte(entry, ':'); i >= 0 {
		return entry[:i], strings.ToUpper(entry[i+1:])
	}
	return entry, "ALLOW"
}

// buildInstanceFlexibilityPolicy returns nil when no flex types are
// configured. Each --google-flex-machine-type becomes one InstanceSelection
// with rank = occurrence order. Selection keys are "rank-N" so GCP error
// messages refer back to the operator's input order.
func (c *ComputeUtil) buildInstanceFlexibilityPolicy() *raw.InstanceFlexibilityPolicy {
	if len(c.flexMachineTypes) == 0 {
		return nil
	}

	selections := make(map[string]raw.InstanceFlexibilityPolicyInstanceSelection, len(c.flexMachineTypes))
	for i, mt := range c.flexMachineTypes {
		key := fmt.Sprintf("rank-%d", i)
		selections[key] = raw.InstanceFlexibilityPolicyInstanceSelection{
			MachineTypes: []string{mt},
			Rank:         int64(i),
		}
	}
	return &raw.InstanceFlexibilityPolicy{
		InstanceSelections: selections,
	}
}

// discoverInstanceZone finds the zone GCP placed our just-created
// instance in by name. Uses AggregatedList scoped to the project with
// a server-side name filter, so the response is bounded to (at most)
// one entry per zone matching our instance name.
//
// AggregatedList returns the instance via per-zone scopes; we extract
// the zone from the instance's Zone self-link (".../zones/<zone>")
// rather than relying on the response's scope key to avoid coupling
// to the map's structure.
func (c *ComputeUtil) discoverInstanceZone() (string, error) {
	resp, err := c.service.Instances.AggregatedList(c.project).
		Filter(fmt.Sprintf("name eq %q", c.instanceName)).
		Do()
	if err != nil {
		return "", fmt.Errorf("aggregatedList lookup for %q: %w", c.instanceName, err)
	}

	for _, scope := range resp.Items {
		for _, inst := range scope.Instances {
			if inst.Name != c.instanceName {
				// Defensive: filter is server-side, but a name eq filter
				// can match by prefix in some legacy API responses.
				continue
			}
			zone, err := zoneFromInstanceURL(inst.Zone)
			if err != nil {
				return "", fmt.Errorf("parsing zone from instance %q self-link: %w", c.instanceName, err)
			}
			return zone, nil
		}
	}
	return "", fmt.Errorf("instance %q not found in any zone after bulkInsert (operation completed but aggregatedList did not return it)", c.instanceName)
}

// finishPostCreate runs the post-bulkInsert work: fetch the instance,
// record the flex-picked machine type, add the firewall tag, push the
// SSH key. Zone is already set by discoverInstanceZone upstream.
func (c *ComputeUtil) finishPostCreate(d *Driver) error {
	instance, err := c.instance()
	if err != nil {
		return fmt.Errorf("looking up just-created instance %q: %w", c.instanceName, err)
	}

	c.syncResolvedMachineType(d, instance)

	if err := c.addFirewallTag(instance); err != nil {
		return fmt.Errorf("adding firewall tag to bulkInsert instance %q: %w", c.instanceName, err)
	}

	return c.uploadSSHKey(instance, d.GetSSHKeyPath())
}

// syncResolvedMachineType records the machine type GCP actually placed.
// Best-effort: a parse failure logs and leaves the field empty.
func (c *ComputeUtil) syncResolvedMachineType(d *Driver, instance *raw.Instance) {
	if instance == nil {
		return
	}
	if mt, err := machineTypeFromInstanceURL(instance.MachineType); err == nil {
		d.ResolvedMachineType = mt
	} else {
		log.Warnf("could not parse machine type from instance URL %q: %s", instance.MachineType, err)
	}
}

// zoneFromInstanceURL parses the zone from ".../zones/<zone>/instances/<name>"
// (or ".../zones/<zone>"). net/url validates the input first, then a
// path regex extracts the segment.
func zoneFromInstanceURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("instance URL %q: %w", rawURL, err)
	}
	m := zonePathSegment.FindStringSubmatch(u.Path)
	if m == nil {
		return "", fmt.Errorf("instance URL %q has no /zones/<zone>/ segment in its path", rawURL)
	}
	return m[1], nil
}

// machineTypeFromInstanceURL parses the machine-type segment from a
// compute self-link of the form ".../machineTypes/<type>".
func machineTypeFromInstanceURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("machine-type URL %q: %w", rawURL, err)
	}
	m := machineTypePathSegment.FindStringSubmatch(u.Path)
	if m == nil {
		return "", fmt.Errorf("machine-type URL %q has no /machineTypes/<type> segment", rawURL)
	}
	return m[1], nil
}

// warnAboutMachineTypeInBulkInsertMode surfaces the interaction between
// --google-machine-type and --google-flex-machine-type. Flags that
// bulkInsert outright rejects (--google-address) are blocked at
// flag-validation time, not warned about here.
func (c *ComputeUtil) warnAboutMachineTypeInBulkInsertMode(d *Driver) {
	if len(d.FlexMachineTypes) == 0 {
		log.Warnf("bulkInsert without --google-flex-machine-type: falling back to --google-machine-type=%q. No machine-type fallback on stockout.", d.MachineType)
		return
	}
	if d.MachineType != "" && d.MachineType != defaultMachineType {
		log.Warnf("--google-machine-type=%q is ignored in bulkInsert mode when --google-flex-machine-type is set; only the flex list is honoured.", d.MachineType)
	}
}
