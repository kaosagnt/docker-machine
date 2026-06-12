package google

// bulkInsert provisioning path.
//
// When --google-bulk-insert is set, the usual zonal Instances.Insert
// is replaced by a regional RegionInstances.BulkInsert. The driver
// issues ONE BulkInsert call per flex selection, in operator-given
// preference order, each with count=1/minCount=1 and a LocationPolicy
// that lets GCP pick the zone across --google-location-zone.
//
// We do NOT use InstanceFlexibilityPolicy. At count=1 its rank-fallback
// is dead code: GCP commits to rank-0 up front and surfaces a generic
// VM_MIN_COUNT_NOT_REACHED on stockout without trying the lower ranks.
// We therefore implement selection fallback driver-side: each call
// sets the machine type and disk spec directly in InstanceProperties,
// and a stockout-class error from one call triggers the next.
//
// Two layers of fallback:
//   - GCP picks the zone within a single selection (LocationPolicy
//     with TargetShape=ANY across the configured zones).
//   - The driver picks the next selection on stockout, until one
//     succeeds or every selection has been tried.
//
// The Operation returned by BulkInsert is region-scoped and tells us
// when GCP settled placement, but not where; we discover the chosen
// zone via Instances.AggregatedList before any per-instance follow-up
// (firewall tag, SSH key).

import (
	"bytes"
	"encoding/json"
	"errors"
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

// bootDeviceName is the deviceName we set on the template boot disk.
// Kept as a stable identifier so any tooling that inspects the disk
// from outside the driver can rely on a known name.
const bootDeviceName = "boot"

// stockoutErrorCodes are GCE operation error codes we treat as
// "try the next selection". Anything not in this set is treated as
// fatal: retrying a misconfiguration, auth failure or quota wall on
// a different machine type wastes calls without changing the outcome.
//
//   - VM_MIN_COUNT_NOT_REACHED: the bulkInsert wrapper error GCE
//     emits when it could not place the requested VM (typical
//     stockout symptom at count=1/minCount=1).
//   - ZONE_RESOURCE_POOL_EXHAUSTED / ..._WITH_DETAILS: direct
//     stockout codes; appear when the placement chose a zone that
//     ran out of the requested machine type during scheduling.
var stockoutErrorCodes = map[string]struct{}{
	"VM_MIN_COUNT_NOT_REACHED":                  {},
	"ZONE_RESOURCE_POOL_EXHAUSTED":              {},
	"ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS": {},
}

// createInstanceViaBulkInsert provisions a single VM by looping over
// the configured flex selections (or a synthetic single-entry list
// derived from --google-machine-type when no --google-flex-selection
// is configured), issuing one bulkInsert per selection. On the first
// success it runs the standard post-create configuration; on a
// stockout-class failure it advances to the next selection; on any
// other failure it returns immediately. If every selection fails
// with a stockout-class error, returns an aggregated error.
func (c *ComputeUtil) createInstanceViaBulkInsert(d *Driver) error {
	selections, err := c.effectiveFlexSelections(d)
	if err != nil {
		return err
	}

	log.Infof("Creating instance via bulkInsert in %q across %d selection(s)", c.region(), len(selections))

	stockoutErrs := make([]error, 0, len(selections))
	for i, sel := range selections {
		log.Infof("bulkInsert attempt %d/%d: machine-type=%q disk-type=%q", i+1, len(selections), sel.MachineType, sel.DiskType)
		retryable, attemptErr := c.attemptBulkInsertForSelection(d, sel)
		if attemptErr == nil {
			return c.finishPostCreate(d)
		}
		if !retryable {
			return attemptErr
		}
		log.Warnf("bulkInsert selection %d (%s) hit stockout-class failure, falling through: %v", i, sel.MachineType, attemptErr)
		stockoutErrs = append(stockoutErrs, fmt.Errorf("selection %d (machine-type=%s): %w", i, sel.MachineType, attemptErr))
	}

	return fmt.Errorf("all %d bulkInsert selections failed with stockout-class errors: %w", len(selections), errors.Join(stockoutErrs...))
}

// effectiveFlexSelections returns the parsed selection list the loop
// iterates over. When --google-flex-selection is configured it
// parses each entry; when empty (and --google-bulk-insert is set) it
// synthesises a single selection from --google-machine-type and the
// per-driver disk defaults. This gives operators cross-zone fallback
// via LocationPolicy even without configuring an explicit selection
// ladder.
func (c *ComputeUtil) effectiveFlexSelections(d *Driver) ([]flexSelection, error) {
	if len(c.flexSelections) == 0 {
		sel := flexSelection{
			MachineType:    d.MachineType,
			DiskType:       c.diskTypeURL,
			DiskIops:       int64(d.ProvisionedIops),
			DiskThroughput: int64(d.ProvisionedThroughput),
		}
		if sel.MachineType == "" {
			return nil, errors.New("bulkInsert requires either --google-flex-selection or --google-machine-type")
		}
		return []flexSelection{sel}, nil
	}

	selections := make([]flexSelection, 0, len(c.flexSelections))
	for _, entry := range c.flexSelections {
		sel, err := parseFlexSelectionEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("--google-flex-selection %q: %w", entry, err)
		}
		selections = append(selections, sel)
	}
	return selections, nil
}

// attemptBulkInsertForSelection issues a single bulkInsert call for
// one selection. Returns:
//   - (false, nil) on success;
//   - (true, err)  on stockout-class failure (caller should advance
//     to the next selection);
//   - (false, err) on any other failure (caller should fail fast).
func (c *ComputeUtil) attemptBulkInsertForSelection(d *Driver, sel flexSelection) (retryable bool, err error) {
	props, err := c.buildBulkInsertInstanceProperties(d, sel)
	if err != nil {
		return false, fmt.Errorf("building instance properties for bulkInsert: %w", err)
	}

	req := &raw.BulkInsertInstanceResource{
		Count:    1,
		MinCount: 1,
		PerInstanceProperties: map[string]raw.BulkInsertInstanceResourcePerInstanceProperties{
			c.instanceName: {},
		},
		InstanceProperties: props,
		LocationPolicy:     c.buildLocationPolicy(),
	}

	reqDumpBuf := new(bytes.Buffer)
	reqDumpEnc := json.NewEncoder(reqDumpBuf)
	reqDumpEnc.SetIndent("", "  ")
	if err = reqDumpEnc.Encode(req); err != nil {
		log.Debugf("Failed to encode dump of bulkInsert request: %v", err)
	} else {
		log.Debugf("bulkInsert request: %s", reqDumpBuf)
	}

	op, err := c.service.RegionInstances.BulkInsert(c.project, c.region(), req).Do()
	if err != nil {
		// Synchronous API rejections (auth, malformed request, etc.)
		// are never stockout: fail fast.
		return false, fmt.Errorf("bulkInsert rejected create for %q in %q: %w", c.instanceName, c.region(), err)
	}

	log.Infof("Waiting for bulkInsert operation %s", op.Name)
	if waitErr := c.waitForRegionOp(op.Name); waitErr != nil {
		wrapped := fmt.Errorf("bulkInsert for %q did not complete: %w", c.instanceName, waitErr)
		if isStockoutError(waitErr) {
			return true, wrapped
		}
		return false, wrapped
	}

	return false, nil
}

// buildBulkInsertInstanceProperties mirrors the direct-path
// Instances.Insert body as an InstanceProperties for the given
// selection. Machine type and disk spec come from the selection;
// network, metadata, scheduling, service accounts, tags, labels and
// accelerators come from the driver as in the direct path.
//
// Name and Zone are owned by the bulkInsert envelope (PerInstance
// Properties keys and LocationPolicy respectively) and are not set
// here. MachineType, DiskType and AcceleratorType are bare names —
// zonal URLs would over-constrain or be rejected by bulkInsert.
func (c *ComputeUtil) buildBulkInsertInstanceProperties(d *Driver, sel flexSelection) (*raw.InstanceProperties, error) {
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

	// Resolve effective disk parameters: per-selection override when
	// the selection carries a disk-type, driver defaults otherwise.
	diskType := sel.DiskType
	if diskType == "" {
		diskType = c.diskTypeURL
	}
	diskIops := sel.DiskIops
	if diskIops == 0 {
		diskIops = int64(d.ProvisionedIops)
	}
	diskThroughput := sel.DiskThroughput
	if diskThroughput == 0 {
		diskThroughput = int64(d.ProvisionedThroughput)
	}

	props := &raw.InstanceProperties{
		Description:    "docker host vm",
		MachineType:    sel.MachineType,
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
					DiskType:    diskType,
					Labels:      parseLabels(d),
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

	if diskIops > 0 {
		props.Disks[0].InitializeParams.ProvisionedIops = diskIops
	}
	if diskThroughput > 0 {
		props.Disks[0].InitializeParams.ProvisionedThroughput = diskThroughput
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

// buildLocationPolicy always returns a policy with TargetShape=ANY so
// GCP places the VM in whichever zone has capacity, even at count=1.
//
// This is load-bearing for ZONE fallback within a single selection:
// the bulkInsert default is ANY_SINGLE_ZONE, which commits to one
// zone up front and returns VM_MIN_COUNT_NOT_REACHED on stockout
// there without trying any other zone, silently neutering a multi-
// zone LocationPolicy. ANY restores the cross-zone behaviour the
// policy is meant to provide (and also maximises unused zonal
// reservation utilisation).
//
// Note: this only rescues us across ZONES for the currently-attempted
// machine type / disk spec. Cross-SELECTION fallback (e.g. n4d
// stocked out → try n2d) is handled driver-side by the
// createInstanceViaBulkInsert loop, because InstanceFlexibilityPolicy
// does not provide selection fallback at count=1.
//
// When no zones are configured we still send the policy (with an
// empty Locations map) purely to carry TargetShape=ANY; GCP then
// considers every zone in the region. Without this, omitting
// LocationPolicy lets GCP fall back to the ANY_SINGLE_ZONE default
// region-wide.
func (c *ComputeUtil) buildLocationPolicy() *raw.LocationPolicy {
	policy := &raw.LocationPolicy{
		TargetShape: "ANY",
		Locations:   make(map[string]raw.LocationPolicyLocation, len(c.locationZones)),
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
//
// bulkInsert's locationPolicy.locations[].preference only accepts ALLOW
// or DENY (PREFERRED is a MIG distributionPolicy concept that does not
// exist for bulkInsert). Any other value — including PREFERRED — is
// coerced to ALLOW with a warning rather than passed through: GCE does
// not reliably reject an invalid preference, so passing it verbatim
// yields undefined placement behaviour rather than a clear error.
func parseLocationZoneEntry(entry string) (zone, preference string) {
	zone = entry
	preference = "ALLOW"
	if i := strings.IndexByte(entry, ':'); i >= 0 {
		zone = entry[:i]
		preference = strings.ToUpper(entry[i+1:])
	}
	if preference != "ALLOW" && preference != "DENY" {
		log.Warnf("--google-location-zone %q: preference %q is not valid for bulkInsert (only ALLOW or DENY); coercing to ALLOW.", entry, preference)
		preference = "ALLOW"
	}
	return zone, preference
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

// isStockoutError reports whether err carries a GCE operation error
// whose code is in stockoutErrorCodes. Walks the error chain via
// errors.As so wrappers added by attemptBulkInsertForSelection /
// waitForOp don't hide the underlying operation error.
//
// Conservative: an err that doesn't unwrap to a recognised operation
// error type, or that carries a code we don't list as stockout-class,
// is treated as not retryable. Better to bail than to retry uselessly
// on a config or quota failure.
func isStockoutError(err error) bool {
	if err == nil {
		return false
	}
	var opErr *operationError
	if !errors.As(err, &opErr) {
		return false
	}
	if opErr == nil || opErr.Code == "" {
		return false
	}
	_, ok := stockoutErrorCodes[opErr.Code]
	return ok
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

// finishPostCreate runs the post-bulkInsert work: discover the zone
// GCP placed the VM in, set the driver / compute-util zone fields,
// fetch the instance, record the flex-picked machine type, add the
// firewall tag, push the SSH key.
func (c *ComputeUtil) finishPostCreate(d *Driver) error {
	zone, err := c.discoverInstanceZone()
	if err != nil {
		return fmt.Errorf("discovering zone for bulkInsert-placed instance %q: %w", c.instanceName, err)
	}
	c.zone = zone
	d.ResolvedZone = zone
	c.zoneURL = apiURL + c.project + "/zones/" + zone

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
