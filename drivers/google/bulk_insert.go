package google

// bulkInsert provisioning path.
//
// When --google-bulk-insert is set, the usual zonal Instances.Insert
// is replaced by a regional RegionInstances.BulkInsert. The single
// request carries LocationPolicy (zone preference, from
// --google-location-zone) and InstanceFlexibilityPolicy (ranked
// machine-type fallback, from --google-flex-selection) so GCP
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
	"strconv"
	"strings"

	"github.com/docker/machine/libmachine/log"
	raw "google.golang.org/api/compute/v1"
)

var zonePathSegment = regexp.MustCompile(`/zones/([^/]+)`)
var machineTypePathSegment = regexp.MustCompile(`/machineTypes/([^/]+)$`)

// bootDeviceName is the deviceName we set on the template boot disk so
// per-selection disk overrides can merge against it (GCP merges by
// deviceName).
const bootDeviceName = "boot"

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
	flex, err := c.buildInstanceFlexibilityPolicy(d)
	if err != nil {
		return err
	}
	if flex != nil {
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

	// DeviceName is set so flex selections can override the boot disk
	// by matching the same key (GCP merges by deviceName).
	props := &raw.InstanceProperties{
		Description:    "docker host vm",
		MinCpuPlatform: c.minCPUPlatform,
		Disks: []*raw.AttachedDisk{
			{
				Boot:       true,
				AutoDelete: true,
				DeviceName: bootDeviceName,
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

// flexSelection is one --google-flex-selection entry after parsing.
type flexSelection struct {
	MachineType    string
	DiskType       string
	DiskIops       int64
	DiskThroughput int64
}

// parseFlexSelectionEntry parses a comma-separated key=value list.
// machine-type is required; unknown keys are warned and ignored.
func parseFlexSelectionEntry(entry string) (flexSelection, error) {
	var sel flexSelection
	seen := map[string]bool{}
	for _, kv := range strings.Split(entry, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			return sel, fmt.Errorf("element %q is not key=value", kv)
		}
		k := strings.TrimSpace(parts[0])
		v := strings.TrimSpace(parts[1])
		if seen[k] {
			return sel, fmt.Errorf("duplicate key %q", k)
		}
		seen[k] = true
		switch k {
		case "machine-type":
			if v == "" {
				return sel, fmt.Errorf("empty value for key %q", k)
			}
			sel.MachineType = v
		case "disk-type":
			if v == "" {
				return sel, fmt.Errorf("empty value for key %q", k)
			}
			sel.DiskType = v
		case "disk-iops":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				return sel, fmt.Errorf("disk-iops %q is not a non-negative integer", v)
			}
			sel.DiskIops = n
		case "disk-throughput":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				return sel, fmt.Errorf("disk-throughput %q is not a non-negative integer", v)
			}
			sel.DiskThroughput = n
		default:
			log.Warnf("--google-flex-selection: ignoring unknown key %q in %q", k, entry)
		}
	}
	if sel.MachineType == "" {
		return sel, fmt.Errorf("missing required key machine-type")
	}
	if sel.DiskType == "" && (seen["disk-iops"] || seen["disk-throughput"]) {
		return sel, fmt.Errorf("disk-iops/disk-throughput require disk-type")
	}
	return sel, nil
}

// buildInstanceFlexibilityPolicy returns nil when no flex selections
// are configured. Each --google-flex-selection becomes one
// InstanceSelection with rank = occurrence order. Selection keys are
// "rank-N" so GCP error messages refer back to the operator's input
// order. Entries with a "disk-type" key attach a disk override.
func (c *ComputeUtil) buildInstanceFlexibilityPolicy(d *Driver) (*raw.InstanceFlexibilityPolicy, error) {
	if len(c.flexSelections) == 0 {
		return nil, nil
	}

	selections := make(map[string]raw.InstanceFlexibilityPolicyInstanceSelection, len(c.flexSelections))
	for i, entry := range c.flexSelections {
		sel, err := parseFlexSelectionEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("--google-flex-selection %q: %w", entry, err)
		}
		key := fmt.Sprintf("rank-%d", i)
		instSel := raw.InstanceFlexibilityPolicyInstanceSelection{
			MachineTypes: []string{sel.MachineType},
			Rank:         int64(i),
		}
		if sel.DiskType != "" {
			instSel.Disks = []*raw.AttachedDisk{buildSelectionDiskOverride(d, sel)}
		}
		selections[key] = instSel
	}
	return &raw.InstanceFlexibilityPolicy{
		InstanceSelections: selections,
	}, nil
}

// buildSelectionDiskOverride mirrors the template's boot disk (image,
// size, labels) with disk-type, IOPS, and throughput from the parsed
// selection. Merged with the template by DeviceName.
func buildSelectionDiskOverride(d *Driver, sel flexSelection) *raw.AttachedDisk {
	disk := &raw.AttachedDisk{
		Boot:       true,
		AutoDelete: true,
		DeviceName: bootDeviceName,
		Type:       "PERSISTENT",
		Mode:       "READ_WRITE",
		InitializeParams: &raw.AttachedDiskInitializeParams{
			SourceImage: "https://www.googleapis.com/compute/v1/projects/" + d.MachineImage,
			DiskSizeGb:  int64(d.DiskSize),
			DiskType:    sel.DiskType,
			Labels:      parseLabels(d),
		},
	}
	if sel.DiskIops > 0 {
		disk.InitializeParams.ProvisionedIops = sel.DiskIops
	}
	if sel.DiskThroughput > 0 {
		disk.InitializeParams.ProvisionedThroughput = sel.DiskThroughput
	}
	return disk
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
	log.Infof("bulkInsert placed as %s in %s", d.ResolvedMachineType, d.ResolvedZone)

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

// warnAboutMachineTypeInBulkInsertMode flags --google-machine-type as
// shadowed in bulkInsert mode (machine-type comes from each
// --google-flex-selection entry).
func (c *ComputeUtil) warnAboutMachineTypeInBulkInsertMode(d *Driver) {
	if d.MachineType != "" && d.MachineType != defaultMachineType {
		log.Warnf("--google-machine-type=%q is ignored in bulkInsert mode; only --google-flex-selection is honoured.", d.MachineType)
	}
}
