package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	raw "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

func TestParseLocationZoneEntry(t *testing.T) {
	tests := []struct {
		entry    string
		wantZone string
		wantPref string
	}{
		{"us-east1-b", "us-east1-b", "ALLOW"},
		{"us-east1-b:ALLOW", "us-east1-b", "ALLOW"},
		{"us-east1-b:allow", "us-east1-b", "ALLOW"},
		{"us-east1-d:DENY", "us-east1-d", "DENY"},
		// PREFERRED is not a valid bulkInsert preference (MIG-only); it
		// is coerced to ALLOW with a warning rather than passed through,
		// because GCE does not reliably reject it server-side.
		{"us-east1-c:PREFERRED", "us-east1-c", "ALLOW"},
		{"us-east1-d:preferred", "us-east1-d", "ALLOW"},
		// Any other unknown preference is likewise coerced to ALLOW.
		{"us-east1-d:nonsense", "us-east1-d", "ALLOW"},
	}
	for _, tc := range tests {
		t.Run(tc.entry, func(t *testing.T) {
			gotZone, gotPref := parseLocationZoneEntry(tc.entry)
			assert.Equal(t, tc.wantZone, gotZone)
			assert.Equal(t, tc.wantPref, gotPref)
		})
	}
}

func TestBuildLocationPolicy_NoZones(t *testing.T) {
	// No zones → still emit a policy carrying TargetShape=ANY with an
	// empty Locations map, so GCP considers every zone in the region
	// rather than falling back to the ANY_SINGLE_ZONE default.
	c := &ComputeUtil{}
	p := c.buildLocationPolicy()
	require.NotNil(t, p)
	assert.Equal(t, "ANY", p.TargetShape)
	assert.Empty(t, p.Locations)
}

func TestBuildLocationPolicy_Zones(t *testing.T) {
	c := &ComputeUtil{
		locationZones: []string{"us-east1-b", "us-east1-c:PREFERRED", "us-east1-d:DENY"},
	}
	p := c.buildLocationPolicy()
	require.NotNil(t, p)
	// TargetShape=ANY is load-bearing: the bulkInsert default
	// (ANY_SINGLE_ZONE) pins placement to one zone and fails on
	// stockout without trying the other allowed zones.
	assert.Equal(t, "ANY", p.TargetShape)
	require.Len(t, p.Locations, 3)
	assert.Equal(t, "ALLOW", p.Locations["zones/us-east1-b"].Preference)
	// PREFERRED is coerced to ALLOW (not a valid bulkInsert preference).
	assert.Equal(t, "ALLOW", p.Locations["zones/us-east1-c"].Preference)
	assert.Equal(t, "DENY", p.Locations["zones/us-east1-d"].Preference)
}

func TestParseFlexSelectionEntry(t *testing.T) {
	cases := map[string]struct {
		entry        string
		want         flexSelection
		wantErrSubst string
	}{
		"machine-type only": {
			entry: "machine-type=n2-standard-2",
			want:  flexSelection{MachineType: "n2-standard-2"},
		},
		"with disk-type": {
			entry: "machine-type=n4-standard-2,disk-type=hyperdisk-balanced",
			want:  flexSelection{MachineType: "n4-standard-2", DiskType: "hyperdisk-balanced"},
		},
		"full override": {
			entry: "machine-type=n4-standard-2,disk-type=hyperdisk-balanced,disk-iops=3000,disk-throughput=140",
			want:  flexSelection{MachineType: "n4-standard-2", DiskType: "hyperdisk-balanced", DiskIops: 3000, DiskThroughput: 140},
		},
		"whitespace around equals": {
			entry: "machine-type = n4-standard-2 , disk-type = hyperdisk-balanced",
			want:  flexSelection{MachineType: "n4-standard-2", DiskType: "hyperdisk-balanced"},
		},
		"empty entry between commas": {
			entry: "machine-type=n4-standard-2,,disk-type=hyperdisk-balanced",
			want:  flexSelection{MachineType: "n4-standard-2", DiskType: "hyperdisk-balanced"},
		},
		"unknown key ignored": {
			entry: "machine-type=n4-standard-2,foo=bar",
			want:  flexSelection{MachineType: "n4-standard-2"},
		},
		"missing machine-type":   {entry: "disk-type=hyperdisk-balanced", wantErrSubst: "missing required key machine-type"},
		"missing equals":         {entry: "n4-standard-2", wantErrSubst: "is not key=value"},
		"empty value":            {entry: "machine-type=n4-standard-2,disk-type=", wantErrSubst: `empty value for key "disk-type"`},
		"empty machine-type":     {entry: "machine-type=", wantErrSubst: `empty value for key "machine-type"`},
		"duplicate key":          {entry: "machine-type=a,machine-type=b", wantErrSubst: `duplicate key "machine-type"`},
		"non-integer iops":       {entry: "machine-type=n4-standard-2,disk-type=hyperdisk-balanced,disk-iops=abc", wantErrSubst: "disk-iops"},
		"negative iops":          {entry: "machine-type=n4-standard-2,disk-type=hyperdisk-balanced,disk-iops=-1", wantErrSubst: "disk-iops"},
		"non-integer throughput": {entry: "machine-type=n4-standard-2,disk-type=hyperdisk-balanced,disk-throughput=fast", wantErrSubst: "disk-throughput"},
		"iops without disk-type": {entry: "machine-type=n4-standard-2,disk-iops=3000", wantErrSubst: "require disk-type"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := parseFlexSelectionEntry(tc.entry)
			if tc.wantErrSubst != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrSubst)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestZoneFromInstanceURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		want    string
		wantErr bool
	}{
		{
			name: "full self-link",
			url:  "https://www.googleapis.com/compute/v1/projects/p/zones/us-east1-b/instances/foo",
			want: "us-east1-b",
		},
		{
			name: "partial path",
			url:  "projects/p/zones/europe-west2-c/instances/bar",
			want: "europe-west2-c",
		},
		{
			name: "zone self-link only",
			url:  "https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a",
			want: "us-central1-a",
		},
		{
			name:    "empty input",
			url:     "",
			wantErr: true,
		},
		{
			name:    "no zone segment",
			url:     "https://www.googleapis.com/compute/v1/projects/p/global/instances/foo",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := zoneFromInstanceURL(tc.url)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestMachineTypeFromInstanceURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		want    string
		wantErr bool
	}{
		{
			name: "full self-link",
			url:  "https://www.googleapis.com/compute/v1/projects/p/zones/us-east1-b/machineTypes/n2-standard-2",
			want: "n2-standard-2",
		},
		{
			name: "partial path",
			url:  "projects/p/zones/europe-west2-c/machineTypes/c2-standard-4",
			want: "c2-standard-4",
		},
		{
			name:    "no machineTypes segment",
			url:     "https://www.googleapis.com/compute/v1/projects/p/zones/us-east1-b",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := machineTypeFromInstanceURL(tc.url)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestBuildBulkInsertInstanceProperties_BareNamesAndSelectionFields(t *testing.T) {
	// Critical bulkInsert invariant: InstanceProperties must not pin
	// to a specific zone (MachineType / DiskType / AcceleratorType
	// are bare names, not zonal URLs), so GCP can resolve them
	// against the placement zone chosen by the LocationPolicy.
	//
	// Per-selection fields override the driver-default disk spec.
	d := &Driver{
		MachineType:           "ignored-in-flex-mode",
		MachineImage:          "ubuntu-os-cloud/global/images/ubuntu-2204",
		DiskSize:              20,
		DiskType:              "pd-balanced",
		Network:               "default",
		ServiceAccount:        defaultServiceAccount,
		Scopes:                defaultScopes,
		ProvisionedIops:       0,
		ProvisionedThroughput: 0,
	}
	c := &ComputeUtil{
		project:        "p",
		networkProject: "p",
		diskTypeURL:    "pd-balanced",
		globalURL:      apiURL + "p/global",
		accelerator:    "count=1,type=nvidia-tesla-t4",
	}

	sel := flexSelection{
		MachineType:    "n4-standard-2",
		DiskType:       "hyperdisk-balanced",
		DiskIops:       3000,
		DiskThroughput: 140,
	}
	props, err := c.buildBulkInsertInstanceProperties(d, sel)
	require.NoError(t, err)
	require.NotNil(t, props)

	// MachineType: bare name from the selection.
	assert.Equal(t, "n4-standard-2", props.MachineType)
	assert.NotContains(t, props.MachineType, "/zones/")

	// DiskType + IOPS + Throughput: from the selection.
	require.Len(t, props.Disks, 1)
	require.NotNil(t, props.Disks[0].InitializeParams)
	assert.Equal(t, "hyperdisk-balanced", props.Disks[0].InitializeParams.DiskType)
	assert.NotContains(t, props.Disks[0].InitializeParams.DiskType, "/zones/")
	assert.Equal(t, int64(3000), props.Disks[0].InitializeParams.ProvisionedIops)
	assert.Equal(t, int64(140), props.Disks[0].InitializeParams.ProvisionedThroughput)

	// AcceleratorType: bare name (the direct path uses a zonal URL
	// here; bulkInsert must not, or GCP rejects the InstanceProperties).
	require.Len(t, props.GuestAccelerators, 1)
	assert.Equal(t, int64(1), props.GuestAccelerators[0].AcceleratorCount)
	assert.Equal(t, "nvidia-tesla-t4", props.GuestAccelerators[0].AcceleratorType)
	assert.NotContains(t, props.GuestAccelerators[0].AcceleratorType, "/zones/")
	assert.NotContains(t, props.GuestAccelerators[0].AcceleratorType, "/acceleratorTypes/")

	// Network is global; subnetwork unset so no regional URL leak.
	require.Len(t, props.NetworkInterfaces, 1)
	assert.Contains(t, props.NetworkInterfaces[0].Network, "/global/networks/default")
	assert.Empty(t, props.NetworkInterfaces[0].Subnetwork)
}

func TestBuildBulkInsertInstanceProperties_SelectionWithoutDiskOverride(t *testing.T) {
	// A selection without disk-type / disk-iops / disk-throughput
	// inherits the driver-default boot disk: same shape the direct
	// path uses (pd-balanced from --google-disk-type, zero IOPS /
	// throughput unless --google-provisioned-* were set).
	d := &Driver{
		MachineType:           "ignored",
		MachineImage:          "ubuntu-os-cloud/global/images/ubuntu-2204",
		DiskSize:              25,
		DiskType:              "pd-balanced",
		Network:               "default",
		ServiceAccount:        defaultServiceAccount,
		Scopes:                defaultScopes,
		ProvisionedIops:       2500,
		ProvisionedThroughput: 100,
	}
	c := &ComputeUtil{
		project:        "p",
		networkProject: "p",
		diskTypeURL:    "pd-balanced",
		globalURL:      apiURL + "p/global",
	}

	sel := flexSelection{MachineType: "n2d-standard-2"}
	props, err := c.buildBulkInsertInstanceProperties(d, sel)
	require.NoError(t, err)
	require.NotNil(t, props)

	assert.Equal(t, "n2d-standard-2", props.MachineType)
	require.Len(t, props.Disks, 1)
	require.NotNil(t, props.Disks[0].InitializeParams)
	assert.Equal(t, "pd-balanced", props.Disks[0].InitializeParams.DiskType)
	// Driver-provided IOPS / Throughput fall through when the
	// selection does not override them.
	assert.Equal(t, int64(2500), props.Disks[0].InitializeParams.ProvisionedIops)
	assert.Equal(t, int64(100), props.Disks[0].InitializeParams.ProvisionedThroughput)
}

func TestIsStockoutError(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"nil":                                   {err: nil, want: false},
		"plain error not retryable":             {err: errors.New("network blew up"), want: false},
		"untyped operation error not retryable": {err: errors.New("operation error: {SOME_CODE [] x y [] []}"), want: false},
		"VM_MIN_COUNT_NOT_REACHED is retryable": {
			err:  &operationError{OperationErrorErrors: &raw.OperationErrorErrors{Code: "VM_MIN_COUNT_NOT_REACHED"}},
			want: true,
		},
		"ZONE_RESOURCE_POOL_EXHAUSTED is retryable": {
			err:  &operationError{OperationErrorErrors: &raw.OperationErrorErrors{Code: "ZONE_RESOURCE_POOL_EXHAUSTED"}},
			want: true,
		},
		"ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS is retryable": {
			err:  &operationError{OperationErrorErrors: &raw.OperationErrorErrors{Code: "ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS"}},
			want: true,
		},
		"wrapped op error still classified": {
			err: fmt.Errorf("bulkInsert for %q did not complete: %w", "tm",
				&operationError{OperationErrorErrors: &raw.OperationErrorErrors{Code: "VM_MIN_COUNT_NOT_REACHED"}},
			),
			want: true,
		},
		"unknown code not retryable": {
			err:  &operationError{OperationErrorErrors: &raw.OperationErrorErrors{Code: "INVALID_FIELD_VALUE"}},
			want: false,
		},
		"empty code not retryable": {
			err:  &operationError{OperationErrorErrors: &raw.OperationErrorErrors{Code: ""}},
			want: false,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, isStockoutError(tc.err))
		})
	}
}

func TestIsStockoutAPIError(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"nil":                      {err: nil, want: false},
		"plain error not stockout": {err: errors.New("network blew up"), want: false},
		"operationError not an API error": {
			err:  &operationError{OperationErrorErrors: &raw.OperationErrorErrors{Code: "VM_MIN_COUNT_NOT_REACHED"}},
			want: false,
		},
		"503 insufficientCapacity is stockout": {
			err: &googleapi.Error{
				Code:   503,
				Errors: []googleapi.ErrorItem{{Reason: "insufficientCapacity", Message: "Region does not currently have sufficient capacity"}},
			},
			want: true,
		},
		"wrapped 503 insufficientCapacity still classified": {
			err: fmt.Errorf("bulkInsert rejected create for %q in %q: %w", "tm", "us-east1",
				&googleapi.Error{
					Code:   503,
					Errors: []googleapi.ErrorItem{{Reason: "insufficientCapacity"}},
				},
			),
			want: true,
		},
		"503 with unrelated reason is fatal": {
			err: &googleapi.Error{
				Code:   503,
				Errors: []googleapi.ErrorItem{{Reason: "backendError"}},
			},
			want: false,
		},
		"403 quota is fatal": {
			err: &googleapi.Error{
				Code:   403,
				Errors: []googleapi.ErrorItem{{Reason: "quotaExceeded"}},
			},
			want: false,
		},
		"api error with no error items is fatal": {
			err:  &googleapi.Error{Code: 503},
			want: false,
		},
		"multiple reasons, one stockout, is stockout": {
			err: &googleapi.Error{
				Code:   503,
				Errors: []googleapi.ErrorItem{{Reason: "rateLimitExceeded"}, {Reason: "insufficientCapacity"}},
			},
			want: true,
		},
		"503 ZONE_RESOURCE_POOL_EXHAUSTED is stockout": {
			err: &googleapi.Error{
				Code:   503,
				Errors: []googleapi.ErrorItem{{Reason: "ZONE_RESOURCE_POOL_EXHAUSTED"}},
			},
			want: true,
		},
		"503 ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS is stockout": {
			err: &googleapi.Error{
				Code:   503,
				Errors: []googleapi.ErrorItem{{Reason: "ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS"}},
			},
			want: true,
		},
		"503 RESOURCE_POOL_EXHAUSTED (non-zonal) is stockout": {
			err: &googleapi.Error{
				Code:   503,
				Errors: []googleapi.ErrorItem{{Reason: "RESOURCE_POOL_EXHAUSTED"}},
			},
			want: true,
		},
		"429 RESOURCE_AVAILABILITY (AIP-193 canonical) is stockout": {
			err: &googleapi.Error{
				Code:   429,
				Errors: []googleapi.ErrorItem{{Reason: "RESOURCE_AVAILABILITY"}},
			},
			want: true,
		},
		"429 quotaExceeded is fatal (quota, not capacity)": {
			err: &googleapi.Error{
				Code:   429,
				Errors: []googleapi.ErrorItem{{Reason: "quotaExceeded"}},
			},
			want: false,
		},
		"403 with capacity-like reason is fatal (wrong status)": {
			err: &googleapi.Error{
				Code:   403,
				Errors: []googleapi.ErrorItem{{Reason: "insufficientCapacity"}},
			},
			want: false,
		},
		"capacity reason in Details ErrorInfo is stockout": {
			err: &googleapi.Error{
				Code: 429,
				Details: []interface{}{
					map[string]interface{}{
						"@type":  "type.googleapis.com/google.rpc.ErrorInfo",
						"reason": "RESOURCE_AVAILABILITY",
						"domain": "compute.googleapis.com",
					},
				},
			},
			want: true,
		},
		"non-capacity reason in Details ErrorInfo is fatal": {
			err: &googleapi.Error{
				Code: 429,
				Details: []interface{}{
					map[string]interface{}{
						"@type":  "type.googleapis.com/google.rpc.ErrorInfo",
						"reason": "RATE_LIMIT_EXCEEDED",
						"domain": "compute.googleapis.com",
					},
				},
			},
			want: false,
		},
		"capacity reason on wrong Details @type is fatal": {
			err: &googleapi.Error{
				Code: 429,
				Details: []interface{}{
					map[string]interface{}{
						"@type":  "type.googleapis.com/google.rpc.Help",
						"reason": "RESOURCE_AVAILABILITY",
					},
				},
			},
			want: false,
		},
		"capacity reason with absent Details @type is stockout (tolerant)": {
			err: &googleapi.Error{
				Code: 429,
				Details: []interface{}{
					map[string]interface{}{"reason": "RESOURCE_AVAILABILITY"},
				},
			},
			want: true,
		},
		"capacity reason with present non-string Details @type is fatal": {
			err: &googleapi.Error{
				Code: 429,
				Details: []interface{}{
					map[string]interface{}{"@type": 123, "reason": "RESOURCE_AVAILABILITY"},
				},
			},
			want: false,
		},
		"capacity reason with null Details @type is fatal": {
			err: &googleapi.Error{
				Code: 429,
				Details: []interface{}{
					map[string]interface{}{"@type": nil, "reason": "RESOURCE_AVAILABILITY"},
				},
			},
			want: false,
		},
		"Details capacity reason but wrong status is fatal": {
			err: &googleapi.Error{
				Code: 400,
				Details: []interface{}{
					map[string]interface{}{"reason": "RESOURCE_AVAILABILITY"},
				},
			},
			want: false,
		},
		"Details entry not a map is ignored (no panic)": {
			err: &googleapi.Error{
				Code:    503,
				Details: []interface{}{"some string detail", 42},
			},
			want: false,
		},
		"Details reason field not a string is ignored": {
			err: &googleapi.Error{
				Code: 503,
				Details: []interface{}{
					map[string]interface{}{"reason": 123},
				},
			},
			want: false,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, isStockoutAPIError(tc.err))
		})
	}
}

func TestEffectiveFlexSelections_SynthesisedFromMachineType(t *testing.T) {
	// With --google-bulk-insert but no --google-flex-selection, the
	// loop iterates a single selection synthesised from
	// --google-machine-type / --google-disk-type / --google-provisioned-*.
	d := &Driver{
		MachineType:           "n2d-standard-2",
		ProvisionedIops:       1500,
		ProvisionedThroughput: 90,
	}
	c := &ComputeUtil{
		diskTypeURL: "pd-balanced",
	}

	sels, err := c.effectiveFlexSelections(d)
	require.NoError(t, err)
	require.Len(t, sels, 1)
	assert.Equal(t, "n2d-standard-2", sels[0].MachineType)
	assert.Equal(t, "pd-balanced", sels[0].DiskType)
	assert.Equal(t, int64(1500), sels[0].DiskIops)
	assert.Equal(t, int64(90), sels[0].DiskThroughput)
}

func TestEffectiveFlexSelections_EmptyMachineTypeIsAnError(t *testing.T) {
	// Defensive: --google-bulk-insert without --google-flex-selection
	// or --google-machine-type has nothing to attempt.
	d := &Driver{}
	c := &ComputeUtil{}

	_, err := c.effectiveFlexSelections(d)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires either --google-flex-selection or --google-machine-type")
}

func TestEffectiveFlexSelections_ParsedFromFlexSelections(t *testing.T) {
	c := &ComputeUtil{
		flexSelections: []string{
			"machine-type=n2-standard-2",
			"machine-type=t2d-standard-2",
			"machine-type=n4-standard-2,disk-type=hyperdisk-balanced,disk-iops=3000,disk-throughput=140",
		},
	}

	sels, err := c.effectiveFlexSelections(&Driver{})
	require.NoError(t, err)
	require.Len(t, sels, 3)
	assert.Equal(t, "n2-standard-2", sels[0].MachineType)
	assert.Empty(t, sels[0].DiskType)
	assert.Equal(t, "t2d-standard-2", sels[1].MachineType)
	assert.Equal(t, "n4-standard-2", sels[2].MachineType)
	assert.Equal(t, "hyperdisk-balanced", sels[2].DiskType)
	assert.Equal(t, int64(3000), sels[2].DiskIops)
	assert.Equal(t, int64(140), sels[2].DiskThroughput)
}

func TestEffectiveFlexSelections_ParseErrorSurfaced(t *testing.T) {
	c := &ComputeUtil{
		flexSelections: []string{"machine-type=n2-standard-2", "bogus-without-equals"},
	}
	_, err := c.effectiveFlexSelections(&Driver{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--google-flex-selection")
}

func TestUsesBulkInsert(t *testing.T) {
	t.Run("default direct mode", func(t *testing.T) {
		d := &Driver{}
		assert.False(t, d.BulkInsert)
	})
	t.Run("region alone is not enough", func(t *testing.T) {
		// Explicit opt-in is required: Region without --google-bulk-insert
		// keeps the direct path. Avoids accidentally switching modes
		// when an operator only meant to set a region for some
		// hypothetical future feature.
		d := &Driver{Region: "us-east1"}
		assert.False(t, d.BulkInsert)
	})
	t.Run("BulkInsert opt-in flips mode", func(t *testing.T) {
		d := &Driver{BulkInsert: true, Region: "us-east1"}
		assert.True(t, d.BulkInsert)
	})
}

// writeJSONError writes body as a GCE-style JSON error response with
// the given status and an application/json content-type, matching the
// shape googleapi clients receive in production. Preferred over
// http.Error, which forces text/plain and appends a newline.
func writeJSONError(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// newBulkInsertComputeUtil builds a ComputeUtil pointed at a test
// server, sufficient to drive attemptBulkInsertForSelection up to the
// RegionInstances.BulkInsert call.
func newBulkInsertComputeUtil(t *testing.T, srv *httptest.Server) *ComputeUtil {
	t.Helper()
	svc, err := raw.NewService(context.Background(), option.WithoutAuthentication(), option.WithEndpoint(srv.URL))
	require.NoError(t, err)
	return &ComputeUtil{
		instanceName: "runner-abc",
		project:      "p",
		zone:         "us-east1-c",
		globalURL:    apiURL + "p/global",
		service:      svc,
		bulkInsert:   true,
		operationBackoffFactory: &backoffFactory{
			InitialInterval:     time.Millisecond,
			RandomizationFactor: 0,
			Multiplier:          2,
			MaxInterval:         10 * time.Millisecond,
			MaxElapsedTime:      time.Second,
		},
	}
}

// TestAttemptBulkInsertForSelection_SyncStockoutIsRetryable is the
// regression test for the 2026-06-22 incident: a synchronous HTTP 503
// insufficientCapacity rejection from BulkInsert must be classified as
// retryable so the loop advances to the next flex selection, instead of
// aborting the whole create.
func TestAttemptBulkInsertForSelection_SyncStockoutIsRetryable(t *testing.T) {
	cases := map[string]struct {
		status        int
		body          string
		wantRetryable bool
		wantErr       bool
	}{
		"503 insufficientCapacity -> retryable": {
			status:        http.StatusServiceUnavailable,
			body:          `{"error":{"code":503,"message":"Region does not currently have sufficient capacity for the requested resources.","errors":[{"reason":"insufficientCapacity","message":"insufficient capacity"}]}}`,
			wantRetryable: true,
			wantErr:       true,
		},
		"403 quotaExceeded -> fatal": {
			status:        http.StatusForbidden,
			body:          `{"error":{"code":403,"message":"Quota exceeded","errors":[{"reason":"quotaExceeded"}]}}`,
			wantRetryable: false,
			wantErr:       true,
		},
		"503 backendError -> fatal": {
			status:        http.StatusServiceUnavailable,
			body:          `{"error":{"code":503,"message":"backend hiccup","errors":[{"reason":"backendError"}]}}`,
			wantRetryable: false,
			wantErr:       true,
		},
		// 429 capacity, real-decoded through googleapi: proves the
		// status gate accepts 429 end-to-end, not just 503.
		"429 RESOURCE_AVAILABILITY -> retryable": {
			status:        http.StatusTooManyRequests,
			body:          `{"error":{"code":429,"message":"The zone does not have enough resources available.","errors":[{"reason":"RESOURCE_AVAILABILITY"}]}}`,
			wantRetryable: true,
			wantErr:       true,
		},
		// 429 quota, real-decoded: must stay fatal (quota != capacity).
		"429 quotaExceeded -> fatal": {
			status:        http.StatusTooManyRequests,
			body:          `{"error":{"code":429,"message":"Quota exceeded","errors":[{"reason":"quotaExceeded"}]}}`,
			wantRetryable: false,
			wantErr:       true,
		},
		// Capacity reason carried ONLY in details[] (google.rpc.ErrorInfo),
		// with no legacy errors[]. This proves the real googleapi decoder
		// produces the map[string]interface{} shape isStockoutAPIError
		// reads from Details — the load-bearing assumption of that branch.
		"429 capacity in details[] only -> retryable": {
			status:        http.StatusTooManyRequests,
			body:          `{"error":{"code":429,"message":"out of capacity","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"RESOURCE_AVAILABILITY","domain":"compute.googleapis.com"}]}}`,
			wantRetryable: true,
			wantErr:       true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSONError(w, tc.status, tc.body)
			}))
			defer srv.Close()

			c := newBulkInsertComputeUtil(t, srv)
			retryable, err := c.attemptBulkInsertForSelection(&Driver{Network: "default"}, flexSelection{MachineType: "n2d-standard-2"})

			assert.Equal(t, tc.wantRetryable, retryable)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

const stockout503Body = `{"error":{"code":503,"message":"Region does not currently have sufficient capacity for the requested resources.","errors":[{"reason":"insufficientCapacity","message":"insufficient capacity"}]}}`

// TestCreateInstanceViaBulkInsert_LoopAdvancesOnSyncStockout is the
// end-to-end regression guard for the 2026-06-22 incident. The leaf
// test above proves attemptBulkInsertForSelection *returns*
// retryable=true on a sync 503; this proves the loop in
// createInstanceViaBulkInsert actually *acts* on it — advancing across
// selections rather than aborting on the first synchronous stockout.
//
// It counts BulkInsert POSTs server-side so a regression that dropped
// the fall-through (e.g. inverted `if !retryable`) would change the
// attempt count and fail here, even though the leaf tests stayed green.
func TestCreateInstanceViaBulkInsert_LoopAdvancesOnSyncStockout(t *testing.T) {
	t.Run("all selections sync-stockout: every selection attempted, aggregated error", func(t *testing.T) {
		var bulkInsertCalls int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "bulkInsert") {
				bulkInsertCalls++
				writeJSONError(w, http.StatusServiceUnavailable, stockout503Body)
				return
			}
			t.Errorf("unexpected non-bulkInsert call to %s", r.URL.Path)
		}))
		defer srv.Close()

		c := newBulkInsertComputeUtil(t, srv)
		c.regionExplicit = "us-east1"
		c.flexSelections = []string{
			"machine-type=n4d-standard-2",
			"machine-type=n2d-standard-2",
			"machine-type=n4-standard-2",
		}

		err := c.createInstanceViaBulkInsert(&Driver{Network: "default"})

		require.Error(t, err)
		// The loop must have advanced through ALL three selections, not
		// aborted on the first synchronous 503.
		assert.Equal(t, 3, bulkInsertCalls, "loop should attempt every selection on sync stockout")
		assert.Contains(t, err.Error(), "all 3 bulkInsert selections failed with stockout-class errors")
	})

	t.Run("fatal sync error on first selection: aborts without advancing", func(t *testing.T) {
		var bulkInsertCalls int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "bulkInsert") {
				bulkInsertCalls++
				writeJSONError(w, http.StatusForbidden, `{"error":{"code":403,"message":"Quota exceeded","errors":[{"reason":"quotaExceeded"}]}}`)
				return
			}
			t.Errorf("unexpected non-bulkInsert call to %s", r.URL.Path)
		}))
		defer srv.Close()

		c := newBulkInsertComputeUtil(t, srv)
		c.regionExplicit = "us-east1"
		c.flexSelections = []string{
			"machine-type=n4d-standard-2",
			"machine-type=n2d-standard-2",
		}

		err := c.createInstanceViaBulkInsert(&Driver{Network: "default"})

		require.Error(t, err)
		// A non-stockout (fatal) rejection must short-circuit the loop:
		// exactly one attempt, and the raw error, not the aggregate.
		assert.Equal(t, 1, bulkInsertCalls, "fatal error must not advance to the next selection")
		assert.Contains(t, err.Error(), "bulkInsert rejected create")
		assert.NotContains(t, err.Error(), "all 2 bulkInsert selections failed")
	})

	// Proves the loop also advances on a 429 whose capacity reason is
	// carried ONLY in details[] (no legacy errors[]) — exercising the
	// 429 status branch and the Details[] reason branch end-to-end
	// through the real googleapi decoder, not just the hand-built unit
	// fixtures.
	t.Run("429 details-only stockout advances the loop", func(t *testing.T) {
		var bulkInsertCalls int
		const body = `{"error":{"code":429,"message":"out of capacity","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"RESOURCE_AVAILABILITY","domain":"compute.googleapis.com"}]}}`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "bulkInsert") {
				bulkInsertCalls++
				writeJSONError(w, http.StatusTooManyRequests, body)
				return
			}
			t.Errorf("unexpected non-bulkInsert call to %s", r.URL.Path)
		}))
		defer srv.Close()

		c := newBulkInsertComputeUtil(t, srv)
		c.regionExplicit = "us-east1"
		c.flexSelections = []string{
			"machine-type=n4d-standard-2",
			"machine-type=n2d-standard-2",
		}

		err := c.createInstanceViaBulkInsert(&Driver{Network: "default"})

		require.Error(t, err)
		assert.Equal(t, 2, bulkInsertCalls, "429 details-only stockout should advance through every selection")
		assert.Contains(t, err.Error(), "all 2 bulkInsert selections failed with stockout-class errors")
	})
}
