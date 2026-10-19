package google

// InstanceFlexibilityPolicy is not used: at count=1 GCP commits to rank
// 0 and reports VM_MIN_COUNT_NOT_REACHED without trying the lower ranks,
// so the driver tries the selections itself.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/docker/machine/libmachine/log"
	raw "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

var zonePathSegment = regexp.MustCompile(`/zones/([^/]+)`)

var gceZoneName = regexp.MustCompile(`^[a-z]+-[a-z]*[0-9]+-[a-z]$`)
var machineTypePathSegment = regexp.MustCompile(`/machineTypes/([^/]+)$`)

const bootDeviceName = "boot"

type failureClass string

const (
	failureStockout failureClass = "stockout"
	failureQuota    failureClass = "quota"
	failureBackend  failureClass = "backend"
	failureUnknown  failureClass = "unknown"
	// failureUnresolved is an accepted operation whose outcome could not
	// be read. The VM may exist, so it cannot be opted in.
	failureUnresolved failureClass = "unresolved"
)

var failureClasses = []failureClass{failureStockout, failureQuota, failureBackend, failureUnknown}

func parseFailureClasses(entries []string) (map[failureClass]bool, error) {
	out, unknown := splitFailureClasses(entries)
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown class %q (known: stockout, quota, backend, unknown)", unknown[0])
	}
	return out, nil
}

func splitFailureClasses(entries []string) (known map[failureClass]bool, unknown []string) {
	known = map[failureClass]bool{}
	for _, entry := range entries {
		for _, raw := range strings.Split(entry, ",") {
			name := failureClass(strings.ToLower(strings.TrimSpace(raw)))
			if name == "" {
				continue
			}
			isKnown := false
			for _, c := range failureClasses {
				isKnown = isKnown || c == name
			}
			if !isKnown {
				unknown = append(unknown, raw)
				continue
			}
			known[name] = true
		}
	}
	return known, unknown
}

var opErrorClasses = map[string]failureClass{
	"VM_MIN_COUNT_NOT_REACHED":                  failureStockout,
	"ZONE_RESOURCE_POOL_EXHAUSTED":              failureStockout,
	"ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS": failureStockout,
	"QUOTA_EXCEEDED":                            failureQuota,
	"INTERNAL_ERROR":                            failureBackend,
}

// insufficientCapacity is undocumented but is what GCE sends on a 503
// for a region-wide shortage.
var apiReasonClasses = map[string]failureClass{
	"insufficientCapacity":                      failureStockout,
	"ZONE_RESOURCE_POOL_EXHAUSTED":              failureStockout,
	"ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS": failureStockout,
	"RESOURCE_POOL_EXHAUSTED":                   failureStockout,
	"RESOURCE_AVAILABILITY":                     failureStockout,
	"quotaExceeded":                             failureQuota,
	"backendError":                              failureBackend,
	"internalError":                             failureBackend,
}

// 504 is DEADLINE_EXCEEDED, which Google documents as possibly returned
// after the operation completed, so it is unresolved rather than backend.
var apiClassStatusCodes = map[failureClass]map[int]bool{
	failureStockout: {http.StatusServiceUnavailable: true, http.StatusTooManyRequests: true},
	failureQuota:    {http.StatusForbidden: true, http.StatusTooManyRequests: true},
	failureBackend:  {http.StatusInternalServerError: true, http.StatusBadGateway: true, http.StatusServiceUnavailable: true},
}

const errorInfoTypeURL = "type.googleapis.com/google.rpc.ErrorInfo"

func (c *ComputeUtil) createInstanceViaBulkInsert(d *Driver) error {
	selections, err := c.effectiveFlexSelections(d)
	if err != nil {
		return err
	}

	health := newPlacementHealth(d, time.Now)
	selections = health.order(d, selections)

	log.WithFields(log.Fields{"region": c.region(), "selections": len(selections)}).Info("Creating instance via bulkInsert")

	advanceOn := d.flexAdvanceOn()
	failures := make([]error, 0, len(selections))
	for i, sel := range selections {
		log.WithFields(log.Fields{
			"attempt":      i + 1,
			"attempts":     len(selections),
			"machine_type": sel.MachineType,
			"disk_type":    sel.DiskType,
			"region":       c.selectionRegion(sel),
		}).Info("bulkInsert attempt")
		class, attemptErr := c.attemptBulkInsertForSelection(d, sel)
		observedAt := time.Now()
		if attemptErr == nil {
			if err := health.recordPlacementAt(d, sel, observedAt); err != nil {
				log.Warnf("Could not record successful bulkInsert placement health: %v", err)
			}
			return c.finishPostCreate(d)
		}
		// A VM that may exist must not be followed by another create under
		// the same name. Remove deletes it through ResolvedZone.
		if !advanceOn[class] || d.ResolvedZone != "" {
			if err := health.releaseProbe(d, sel); err != nil {
				log.Warnf("Could not release bulkInsert placement probe: %v", err)
			}
			return attemptErr
		}
		log.WithFields(log.Fields{
			"attempt":      i + 1,
			"attempts":     len(selections),
			"machine_type": sel.MachineType,
			"region":       c.selectionRegion(sel),
			"class":        string(class),
			"error":        attemptErr.Error(),
		}).Warn("bulkInsert selection hit stockout-class failure, falling through")
		if err := health.recordStockoutAt(d, sel, observedAt); err != nil {
			log.Warnf("Could not record bulkInsert stockout health: %v", err)
		}
		failures = append(failures, fmt.Errorf("selection %d/%d (machine-type=%s, %s): %w", i+1, len(selections), sel.MachineType, class, attemptErr))
	}

	return fmt.Errorf("all %d bulkInsert selections failed with stockout-class errors: %w", len(selections), errors.Join(failures...))
}

// The flag is validated at SetConfigFromFlags. A saved config.json from
// another driver version may still hold a class this one does not know.
func (d *Driver) flexAdvanceOn() map[failureClass]bool {
	classes, unknown := splitFailureClasses(d.FlexAdvanceOn)
	if len(unknown) > 0 {
		log.Warnf("--google-flex-advance-on: ignoring unknown classes %q", unknown)
	}
	if len(classes) == 0 {
		return map[failureClass]bool{failureStockout: true}
	}
	return classes
}

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

func (c *ComputeUtil) selectionRegion(sel flexSelection) string {
	return sel.effectiveRegion(c.regionExplicit)
}

func (c *ComputeUtil) attemptBulkInsertForSelection(d *Driver, sel flexSelection) (failureClass, error) {
	props, err := c.buildBulkInsertInstanceProperties(d, sel)
	if err != nil {
		return failureUnknown, fmt.Errorf("building instance properties for bulkInsert: %w", err)
	}

	region := c.selectionRegion(sel)
	req := &raw.BulkInsertInstanceResource{
		Count:    1,
		MinCount: 1,
		PerInstanceProperties: map[string]raw.BulkInsertInstanceResourcePerInstanceProperties{
			c.instanceName: {},
		},
		InstanceProperties: props,
		LocationPolicy:     c.buildLocationPolicy(sel),
	}

	reqDumpBuf := new(bytes.Buffer)
	reqDumpEnc := json.NewEncoder(reqDumpBuf)
	reqDumpEnc.SetIndent("", "  ")
	if err = reqDumpEnc.Encode(req); err != nil {
		log.Debugf("Failed to encode dump of bulkInsert request: %v", err)
	} else {
		log.Debugf("bulkInsert request: %s", reqDumpBuf)
	}

	c.placedZone = ""

	op, err := c.service.RegionInstances.BulkInsert(c.project, region, req).Do()
	if err != nil {
		return classifyAPIError(err), fmt.Errorf("bulkInsert rejected create for %q in %q: %w", c.instanceName, region, err)
	}
	if op == nil || op.Name == "" {
		return failureUnresolved, fmt.Errorf("bulkInsert for %q in %q returned no operation name", c.instanceName, region)
	}

	log.WithField("operation", op.Name).Info("Waiting for bulkInsert operation")
	doneOp, waitErr := c.waitForRegionOpResult(region, op.Name)
	if waitErr != nil {
		// A failed operation may still have placed the VM.
		if zone, ok := zoneFromBulkInsertOp(doneOp); ok {
			d.ResolvedZone = zone
		}
		wrapped := fmt.Errorf("bulkInsert for %q did not complete: %w", c.instanceName, waitErr)
		if doneOp == nil {
			return failureUnresolved, wrapped
		}
		return classifyOpError(waitErr), wrapped
	}

	if zone, ok := zoneFromBulkInsertOp(doneOp); ok {
		c.placedZone = zone
	}

	return "", nil
}

// bulkInsert rejects zonal URLs for machine, disk and accelerator types.
func (c *ComputeUtil) buildBulkInsertInstanceProperties(d *Driver, sel flexSelection) (*raw.InstanceProperties, error) {
	var net string
	if strings.Contains(d.Network, "/networks/") {
		net = d.Network
	} else {
		net = c.globalURL + "/networks/" + d.Network
	}

	metadata, err := c.prepareMetadata(d)
	if err != nil {
		return nil, err
	}

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
		ServiceAccounts: serviceAccounts(d),
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

	if subnetwork := sel.effectiveSubnetwork(c.subnetwork); strings.Contains(subnetwork, "/subnetworks/") {
		props.NetworkInterfaces[0].Subnetwork = subnetwork
	} else if subnetwork != "" {
		props.NetworkInterfaces[0].Subnetwork = "projects/" + c.networkProject + "/regions/" + c.selectionRegion(sel) + "/subnetworks/" + subnetwork
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

// The default TargetShape, ANY_SINGLE_ZONE, does not try another zone
// on stockout.
func (c *ComputeUtil) buildLocationPolicy(sel flexSelection) *raw.LocationPolicy {
	zones := sel.effectiveZones(c.regionExplicit, c.locationZones)
	policy := &raw.LocationPolicy{
		TargetShape: "ANY",
		Locations:   make(map[string]raw.LocationPolicyLocation, len(zones)),
	}
	for _, entry := range zones {
		zone, pref := parseLocationZoneEntry(entry)
		policy.Locations["zones/"+zone] = raw.LocationPolicyLocation{
			Preference: pref,
		}
	}
	return policy
}

// bulkInsert does not reliably reject preferences other than ALLOW and
// DENY.
func parseLocationZoneEntry(entry string) (zone, preference string) {
	zone, preference = splitLocationZoneEntry(entry)
	if preference != "ALLOW" && preference != "DENY" {
		log.Warnf("zone %q: preference %q is not valid for bulkInsert (only ALLOW or DENY); coercing to ALLOW.", entry, preference)
		preference = "ALLOW"
	}
	return zone, preference
}

func splitLocationZoneEntry(entry string) (zone, preference string) {
	zone = entry
	preference = "ALLOW"
	if i := strings.IndexByte(entry, ':'); i >= 0 {
		zone = entry[:i]
		preference = strings.ToUpper(entry[i+1:])
	}
	return zone, preference
}

type flexSelection struct {
	MachineType    string
	DiskType       string
	DiskIops       int64
	DiskThroughput int64
	Region         string
	Zones          []string
	Subnetwork     string
}

func (s flexSelection) effectiveRegion(driverRegion string) string {
	if s.Region != "" {
		return s.Region
	}
	return driverRegion
}

// Driver-level zones are in the driver's region.
func (s flexSelection) effectiveZones(driverRegion string, driverZones []string) []string {
	if len(s.Zones) > 0 {
		return s.Zones
	}
	if s.Region != "" && s.Region != driverRegion {
		return nil
	}
	return driverZones
}

func (s flexSelection) effectiveSubnetwork(driverSubnetwork string) string {
	if s.Subnetwork != "" {
		return s.Subnetwork
	}
	return driverSubnetwork
}

func regionOfZone(zone string) string {
	if i := strings.LastIndexByte(zone, '-'); i > 0 {
		return zone[:i]
	}
	return ""
}

var subnetworkRegionPath = regexp.MustCompile(`(?:^|/)regions/([^/]+)/subnetworks/`)

func regionOfSubnetworkPath(subnetwork string) string {
	if m := subnetworkRegionPath.FindStringSubmatch(subnetwork); m != nil {
		return m[1]
	}
	return ""
}

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
		if seen[k] && k != "zone" {
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
		case "region":
			if v == "" {
				return sel, fmt.Errorf("empty value for key %q", k)
			}
			sel.Region = v
		case "zone":
			if zone, _ := splitLocationZoneEntry(v); strings.TrimSpace(zone) == "" {
				return sel, fmt.Errorf("zone %q has an empty zone (expected zone[:PREFERENCE])", v)
			}
			sel.Zones = append(sel.Zones, v)
		case "subnetwork":
			if v == "" {
				return sel, fmt.Errorf("empty value for key %q", k)
			}
			sel.Subnetwork = v
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
	if sel.Region != "" {
		for _, entry := range sel.Zones {
			zone, _ := splitLocationZoneEntry(entry)
			if regionOfZone(zone) != sel.Region {
				return sel, fmt.Errorf("zone %q is not in region %q", zone, sel.Region)
			}
		}
	}
	return sel, nil
}

func (d *Driver) validateFlexSelectionPlacement(sel flexSelection) error {
	region := sel.effectiveRegion(d.Region)
	if sel.Region == "" {
		for _, entry := range sel.Zones {
			zone, _ := splitLocationZoneEntry(entry)
			if regionOfZone(zone) != region {
				return fmt.Errorf("zone %q is not in --google-region %q", zone, region)
			}
		}
	}
	if sel.Subnetwork != "" {
		if r := regionOfSubnetworkPath(sel.Subnetwork); r != "" && r != region {
			return fmt.Errorf("subnetwork %q is not in region %q", sel.Subnetwork, region)
		}
		return nil
	}
	if sel.Region != "" && sel.Region != d.Region && regionOfSubnetworkPath(d.Subnetwork) != "" {
		return fmt.Errorf("region %q differs from --google-region %q; set subnetwork, --google-subnetwork %q is in another region", sel.Region, d.Region, d.Subnetwork)
	}
	return nil
}

func classifyOpError(err error) failureClass {
	var opErr *operationError
	if !errors.As(err, &opErr) || opErr == nil {
		return failureUnknown
	}
	if class, ok := opErrorClasses[opErr.Code]; ok {
		return class
	}
	return failureUnknown
}

// The REST client leaves details[] entries as untyped maps.
// An error without a response from GCE (connection dropped after the
// POST) is unresolved: the request may have been accepted. Among the
// reasons GCE did send, stockout wins, as on main before the classes.
func classifyAPIError(err error) failureClass {
	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) || apiErr == nil {
		return failureUnresolved
	}
	if apiErr.Code == http.StatusGatewayTimeout {
		return failureUnresolved
	}
	classOf := func(reason string) failureClass {
		class := apiReasonClasses[reason]
		if class == "" || !apiClassStatusCodes[class][apiErr.Code] {
			return ""
		}
		return class
	}
	found := failureUnknown
	consider := func(reason string) {
		class := classOf(reason)
		if class == failureStockout || found == failureUnknown && class != "" {
			found = class
		}
	}
	for _, e := range apiErr.Errors {
		consider(e.Reason)
	}
	for _, d := range apiErr.Details {
		m, ok := d.(map[string]interface{})
		if !ok {
			continue
		}
		if rawType, present := m["@type"]; present {
			if t, ok := rawType.(string); !ok || t != errorInfoTypeURL {
				continue
			}
		}
		if reason, ok := m["reason"].(string); ok {
			consider(reason)
		}
	}
	return found
}

func zoneFromBulkInsertOp(op *raw.Operation) (zone string, ok bool) {
	if op == nil || op.InstancesBulkInsertOperationMetadata == nil {
		return "", false
	}
	found := ""
	for location, status := range op.InstancesBulkInsertOperationMetadata.PerLocationStatus {
		if status.CreatedVmCount < 1 {
			continue
		}
		zone, ok := strings.CutPrefix(location, "zones/")
		if !ok || !gceZoneName.MatchString(zone) {
			return "", false
		}
		if found != "" {
			return "", false
		}
		found = zone
	}
	if found == "" {
		return "", false
	}
	return found, true
}

func (c *ComputeUtil) discoverInstanceZone() (string, error) {
	resp, err := c.service.Instances.AggregatedList(c.project).
		Filter(fmt.Sprintf("name eq %q", c.instanceName)).
		Do()
	if err != nil {
		return "", fmt.Errorf("aggregatedList lookup for %q: %w", c.instanceName, err)
	}
	if resp == nil {
		return "", fmt.Errorf("aggregatedList lookup for %q returned a nil response", c.instanceName)
	}

	for _, scope := range resp.Items {
		for _, inst := range scope.Instances {
			if inst.Name != c.instanceName {
				continue
			}
			zone, err := zoneFromInstanceURL(inst.Zone)
			if err != nil {
				return "", fmt.Errorf("parsing zone from instance %q self-link: %w", c.instanceName, err)
			}
			return zone, nil
		}
	}
	return "", fmt.Errorf("instance %q not found in any zone after bulkInsert (operation completed but aggregatedList did not return it): %w", c.instanceName, errInstanceNotPlaced)
}

var errInstanceNotPlaced = errors.New("instance not placed")

// AggregatedList uses the per-region filtered list quota, which runs out
// under stockout load and then orphans an already placed VM.
func (c *ComputeUtil) resolvePlacedZone() (string, error) {
	if c.placedZone != "" {
		return c.placedZone, nil
	}
	zone, err := c.discoverInstanceZone()
	if err != nil {
		return "", fmt.Errorf("discovering zone for bulkInsert-placed instance %q: %w", c.instanceName, err)
	}
	return zone, nil
}

func (c *ComputeUtil) finishPostCreate(d *Driver) error {
	zone, err := c.resolvePlacedZone()
	if err != nil {
		return err
	}
	c.zone = zone
	d.ResolvedZone = zone
	c.zoneURL = apiURL + c.project + "/zones/" + zone

	instance, err := c.instance()
	if err != nil {
		return fmt.Errorf("looking up just-created instance %q: %w", c.instanceName, err)
	}

	c.syncResolvedMachineType(d, instance)
	log.WithFields(log.Fields{"machine_type": d.ResolvedMachineType, "zone": d.ResolvedZone, "region": c.region()}).Info("bulkInsert placed")

	if err := c.addFirewallTag(instance); err != nil {
		return fmt.Errorf("adding firewall tag to bulkInsert instance %q: %w", c.instanceName, err)
	}

	return nil
}

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
