package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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
	p := c.buildLocationPolicy(flexSelection{})
	require.NotNil(t, p)
	assert.Equal(t, "ANY", p.TargetShape)
	assert.Empty(t, p.Locations)
}

func TestBuildLocationPolicy_Zones(t *testing.T) {
	c := &ComputeUtil{
		locationZones: []string{"us-east1-b", "us-east1-c:PREFERRED", "us-east1-d:DENY"},
	}
	p := c.buildLocationPolicy(flexSelection{})
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

func TestBuildLocationPolicy_SelectionZones(t *testing.T) {
	c := &ComputeUtil{
		regionExplicit: "us-east1",
		locationZones:  []string{"us-east1-b", "us-east1-c"},
	}

	p := c.buildLocationPolicy(flexSelection{Region: "us-east4", Zones: []string{"us-east4-a", "us-east4-b:DENY"}})
	require.Len(t, p.Locations, 2)
	assert.Equal(t, "ALLOW", p.Locations["zones/us-east4-a"].Preference)
	assert.Equal(t, "DENY", p.Locations["zones/us-east4-b"].Preference)

	p = c.buildLocationPolicy(flexSelection{Region: "us-east4"})
	assert.Empty(t, p.Locations, "a selection in another region must not inherit the driver zones")

	p = c.buildLocationPolicy(flexSelection{Region: "us-east1"})
	assert.Len(t, p.Locations, 2, "a selection in the driver region inherits the driver zones")

	p = c.buildLocationPolicy(flexSelection{Zones: []string{"us-east1-d"}})
	require.Len(t, p.Locations, 1)
	assert.Contains(t, p.Locations, "zones/us-east1-d")
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
		"region zones and subnetwork": {
			entry: "machine-type=n4-standard-2,region=us-east4,zone=us-east4-a,zone=us-east4-b:DENY,subnetwork=projects/net/regions/us-east4/subnetworks/sub",
			want: flexSelection{
				MachineType: "n4-standard-2",
				Region:      "us-east4",
				Zones:       []string{"us-east4-a", "us-east4-b:DENY"},
				Subnetwork:  "projects/net/regions/us-east4/subnetworks/sub",
			},
		},
		"zones without region": {
			entry: "machine-type=n4-standard-2,zone=us-east1-b",
			want:  flexSelection{MachineType: "n4-standard-2", Zones: []string{"us-east1-b"}},
		},
		"zone outside region":    {entry: "machine-type=n4-standard-2,region=us-east4,zone=us-east1-b", wantErrSubst: `zone "us-east1-b" is not in region "us-east4"`},
		"empty zone":             {entry: "machine-type=n4-standard-2,zone=:DENY", wantErrSubst: "empty zone"},
		"empty region":           {entry: "machine-type=n4-standard-2,region=", wantErrSubst: `empty value for key "region"`},
		"empty subnetwork":       {entry: "machine-type=n4-standard-2,subnetwork=", wantErrSubst: `empty value for key "subnetwork"`},
		"duplicate region":       {entry: "machine-type=n4-standard-2,region=a,region=b", wantErrSubst: `duplicate key "region"`},
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
	props, err := c.buildBulkInsertInstanceProperties(withSSHKey(t, d), sel)
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
	props, err := c.buildBulkInsertInstanceProperties(withSSHKey(t, d), sel)
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

func TestClassifyOpError(t *testing.T) {
	cases := map[string]struct {
		err  error
		want failureClass
	}{
		"nil":                                   {err: nil, want: ""},
		"plain error not retryable":             {err: errors.New("network blew up"), want: ""},
		"untyped operation error not retryable": {err: errors.New("operation error: {SOME_CODE [] x y [] []}"), want: ""},
		"VM_MIN_COUNT_NOT_REACHED is retryable": {
			err:  &operationError{OperationErrorErrors: &raw.OperationErrorErrors{Code: "VM_MIN_COUNT_NOT_REACHED"}},
			want: failureStockout,
		},
		"ZONE_RESOURCE_POOL_EXHAUSTED is retryable": {
			err:  &operationError{OperationErrorErrors: &raw.OperationErrorErrors{Code: "ZONE_RESOURCE_POOL_EXHAUSTED"}},
			want: failureStockout,
		},
		"ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS is retryable": {
			err:  &operationError{OperationErrorErrors: &raw.OperationErrorErrors{Code: "ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS"}},
			want: failureStockout,
		},
		"wrapped op error still classified": {
			err: fmt.Errorf("bulkInsert for %q did not complete: %w", "tm",
				&operationError{OperationErrorErrors: &raw.OperationErrorErrors{Code: "VM_MIN_COUNT_NOT_REACHED"}},
			),
			want: failureStockout,
		},
		"unknown code not retryable": {
			err:  &operationError{OperationErrorErrors: &raw.OperationErrorErrors{Code: "INVALID_FIELD_VALUE"}},
			want: "",
		},
		"QUOTA_EXCEEDED is quota": {
			err:  &operationError{OperationErrorErrors: &raw.OperationErrorErrors{Code: "QUOTA_EXCEEDED"}},
			want: failureQuota,
		},
		"INTERNAL_ERROR is backend": {
			err:  &operationError{OperationErrorErrors: &raw.OperationErrorErrors{Code: "INTERNAL_ERROR"}},
			want: failureBackend,
		},
		"empty code not retryable": {
			err:  &operationError{OperationErrorErrors: &raw.OperationErrorErrors{Code: ""}},
			want: "",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, classifyOpError(tc.err))
		})
	}
}

func TestClassifyAPIError(t *testing.T) {
	cases := map[string]struct {
		err  error
		want failureClass
	}{
		"nil":                      {err: nil, want: ""},
		"plain error not stockout": {err: errors.New("network blew up"), want: ""},
		"operationError not an API error": {
			err:  &operationError{OperationErrorErrors: &raw.OperationErrorErrors{Code: "VM_MIN_COUNT_NOT_REACHED"}},
			want: "",
		},
		"503 insufficientCapacity is stockout": {
			err: &googleapi.Error{
				Code:   503,
				Errors: []googleapi.ErrorItem{{Reason: "insufficientCapacity", Message: "Region does not currently have sufficient capacity"}},
			},
			want: failureStockout,
		},
		"wrapped 503 insufficientCapacity still classified": {
			err: fmt.Errorf("bulkInsert rejected create for %q in %q: %w", "tm", "us-east1",
				&googleapi.Error{
					Code:   503,
					Errors: []googleapi.ErrorItem{{Reason: "insufficientCapacity"}},
				},
			),
			want: failureStockout,
		},
		"503 backendError is backend": {
			err: &googleapi.Error{
				Code:   503,
				Errors: []googleapi.ErrorItem{{Reason: "backendError"}},
			},
			want: failureBackend,
		},
		"403 backendError is fatal (wrong status)": {
			err: &googleapi.Error{
				Code:   403,
				Errors: []googleapi.ErrorItem{{Reason: "backendError"}},
			},
			want: "",
		},
		"403 quotaExceeded is quota": {
			err: &googleapi.Error{
				Code:   403,
				Errors: []googleapi.ErrorItem{{Reason: "quotaExceeded"}},
			},
			want: failureQuota,
		},
		"503 with unknown reason is fatal": {
			err: &googleapi.Error{
				Code:   503,
				Errors: []googleapi.ErrorItem{{Reason: "notFound"}},
			},
			want: "",
		},
		"api error with no error items is fatal": {
			err:  &googleapi.Error{Code: 503},
			want: "",
		},
		"multiple reasons, one stockout, is stockout": {
			err: &googleapi.Error{
				Code:   503,
				Errors: []googleapi.ErrorItem{{Reason: "rateLimitExceeded"}, {Reason: "insufficientCapacity"}},
			},
			want: failureStockout,
		},
		"503 ZONE_RESOURCE_POOL_EXHAUSTED is stockout": {
			err: &googleapi.Error{
				Code:   503,
				Errors: []googleapi.ErrorItem{{Reason: "ZONE_RESOURCE_POOL_EXHAUSTED"}},
			},
			want: failureStockout,
		},
		"503 ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS is stockout": {
			err: &googleapi.Error{
				Code:   503,
				Errors: []googleapi.ErrorItem{{Reason: "ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS"}},
			},
			want: failureStockout,
		},
		"503 RESOURCE_POOL_EXHAUSTED (non-zonal) is stockout": {
			err: &googleapi.Error{
				Code:   503,
				Errors: []googleapi.ErrorItem{{Reason: "RESOURCE_POOL_EXHAUSTED"}},
			},
			want: failureStockout,
		},
		"429 RESOURCE_AVAILABILITY (AIP-193 canonical) is stockout": {
			err: &googleapi.Error{
				Code:   429,
				Errors: []googleapi.ErrorItem{{Reason: "RESOURCE_AVAILABILITY"}},
			},
			want: failureStockout,
		},
		"429 quotaExceeded is quota": {
			err: &googleapi.Error{
				Code:   429,
				Errors: []googleapi.ErrorItem{{Reason: "quotaExceeded"}},
			},
			want: failureQuota,
		},
		"403 with capacity-like reason is fatal (wrong status)": {
			err: &googleapi.Error{
				Code:   403,
				Errors: []googleapi.ErrorItem{{Reason: "insufficientCapacity"}},
			},
			want: "",
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
			want: failureStockout,
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
			want: "",
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
			want: "",
		},
		"capacity reason with absent Details @type is stockout (tolerant)": {
			err: &googleapi.Error{
				Code: 429,
				Details: []interface{}{
					map[string]interface{}{"reason": "RESOURCE_AVAILABILITY"},
				},
			},
			want: failureStockout,
		},
		"capacity reason with present non-string Details @type is fatal": {
			err: &googleapi.Error{
				Code: 429,
				Details: []interface{}{
					map[string]interface{}{"@type": 123, "reason": "RESOURCE_AVAILABILITY"},
				},
			},
			want: "",
		},
		"capacity reason with null Details @type is fatal": {
			err: &googleapi.Error{
				Code: 429,
				Details: []interface{}{
					map[string]interface{}{"@type": nil, "reason": "RESOURCE_AVAILABILITY"},
				},
			},
			want: "",
		},
		"Details capacity reason but wrong status is fatal": {
			err: &googleapi.Error{
				Code: 400,
				Details: []interface{}{
					map[string]interface{}{"reason": "RESOURCE_AVAILABILITY"},
				},
			},
			want: "",
		},
		"Details entry not a map is ignored (no panic)": {
			err: &googleapi.Error{
				Code:    503,
				Details: []interface{}{"some string detail", 42},
			},
			want: "",
		},
		"Details reason field not a string is ignored": {
			err: &googleapi.Error{
				Code: 503,
				Details: []interface{}{
					map[string]interface{}{"reason": 123},
				},
			},
			want: "",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, classifyAPIError(tc.err))
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

func TestAttemptBulkInsertForSelection_SyncRejectionClass(t *testing.T) {
	cases := map[string]struct {
		status    int
		body      string
		wantClass failureClass
		wantErr   bool
	}{
		"503 insufficientCapacity": {
			status:    http.StatusServiceUnavailable,
			body:      `{"error":{"code":503,"message":"Region does not currently have sufficient capacity for the requested resources.","errors":[{"reason":"insufficientCapacity","message":"insufficient capacity"}]}}`,
			wantClass: failureStockout,
			wantErr:   true,
		},
		"403 quotaExceeded": {
			status:    http.StatusForbidden,
			body:      `{"error":{"code":403,"message":"Quota exceeded","errors":[{"reason":"quotaExceeded"}]}}`,
			wantClass: failureQuota,
			wantErr:   true,
		},
		"503 backendError": {
			status:    http.StatusServiceUnavailable,
			body:      `{"error":{"code":503,"message":"Internal error. Please try again or contact Google Support.","errors":[{"reason":"backendError"}]}}`,
			wantClass: failureBackend,
			wantErr:   true,
		},
		"429 RESOURCE_AVAILABILITY": {
			status:    http.StatusTooManyRequests,
			body:      `{"error":{"code":429,"message":"The zone does not have enough resources available.","errors":[{"reason":"RESOURCE_AVAILABILITY"}]}}`,
			wantClass: failureStockout,
			wantErr:   true,
		},
		"429 quotaExceeded": {
			status:    http.StatusTooManyRequests,
			body:      `{"error":{"code":429,"message":"Quota exceeded","errors":[{"reason":"quotaExceeded"}]}}`,
			wantClass: failureQuota,
			wantErr:   true,
		},
		"404 notFound": {
			status:    http.StatusNotFound,
			body:      `{"error":{"code":404,"message":"not found","errors":[{"reason":"notFound"}]}}`,
			wantClass: "",
			wantErr:   true,
		},
		"429 capacity in details[] only": {
			status:    http.StatusTooManyRequests,
			body:      `{"error":{"code":429,"message":"out of capacity","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"RESOURCE_AVAILABILITY","domain":"compute.googleapis.com"}]}}`,
			wantClass: failureStockout,
			wantErr:   true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSONError(w, tc.status, tc.body)
			}))
			defer srv.Close()

			c := newBulkInsertComputeUtil(t, srv)
			class, err := c.attemptBulkInsertForSelection(withSSHKey(t, &Driver{Network: "default"}), flexSelection{MachineType: "n2d-standard-2"})

			assert.Equal(t, tc.wantClass, class)
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
		var bulkInsertCalls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "bulkInsert") {
				bulkInsertCalls.Add(1)
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

		err := c.createInstanceViaBulkInsert(withSSHKey(t, &Driver{Network: "default"}))

		require.Error(t, err)
		// The loop must have advanced through ALL three selections, not
		// aborted on the first synchronous 503.
		assert.Equal(t, int32(3), bulkInsertCalls.Load(), "loop should attempt every selection on sync stockout")
		assert.Contains(t, err.Error(), "all 3 bulkInsert selections failed with stockout-class errors")
	})

	t.Run("fatal sync error on first selection: aborts without advancing", func(t *testing.T) {
		var bulkInsertCalls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "bulkInsert") {
				bulkInsertCalls.Add(1)
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

		err := c.createInstanceViaBulkInsert(withSSHKey(t, &Driver{Network: "default"}))

		require.Error(t, err)
		// A non-stockout (fatal) rejection must short-circuit the loop:
		// exactly one attempt, and the raw error, not the aggregate.
		assert.Equal(t, int32(1), bulkInsertCalls.Load(), "fatal error must not advance to the next selection")
		assert.Contains(t, err.Error(), "bulkInsert rejected create")
		assert.NotContains(t, err.Error(), "all 2 bulkInsert selections failed")
	})

	// Proves the loop also advances on a 429 whose capacity reason is
	// carried ONLY in details[] (no legacy errors[]) — exercising the
	// 429 status branch and the Details[] reason branch end-to-end
	// through the real googleapi decoder, not just the hand-built unit
	// fixtures.
	t.Run("429 details-only stockout advances the loop", func(t *testing.T) {
		var bulkInsertCalls atomic.Int32
		const body = `{"error":{"code":429,"message":"out of capacity","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"RESOURCE_AVAILABILITY","domain":"compute.googleapis.com"}]}}`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "bulkInsert") {
				bulkInsertCalls.Add(1)
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

		err := c.createInstanceViaBulkInsert(withSSHKey(t, &Driver{Network: "default"}))

		require.Error(t, err)
		assert.Equal(t, int32(2), bulkInsertCalls.Load(), "429 details-only stockout should advance through every selection")
		assert.Contains(t, err.Error(), "all 2 bulkInsert selections failed with stockout-class errors")
	})
}

func TestCreateInstanceViaBulkInsert_CooldownReordersNextCreate(t *testing.T) {
	var callsMu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "bulkInsert") {
			t.Errorf("unexpected request to %s", r.URL.Path)
			return
		}
		var req raw.BulkInsertInstanceResource
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		machineType := req.InstanceProperties.MachineType
		callsMu.Lock()
		calls = append(calls, machineType)
		callsMu.Unlock()
		if machineType == "n4d-standard-2" {
			writeJSONError(w, http.StatusServiceUnavailable, stockout503Body)
			return
		}
		writeJSONError(w, http.StatusForbidden, `{"error":{"code":403,"message":"Quota exceeded","errors":[{"reason":"quotaExceeded"}]}}`)
	}))
	defer srv.Close()

	d := NewDriver("runner-abc", t.TempDir())
	writeSSHKey(t, d)
	d.Network = "default"
	d.Project = "p"
	d.Region = "us-east1"
	d.FlexStockoutCooldown = time.Minute

	newUtil := func() *ComputeUtil {
		c := newBulkInsertComputeUtil(t, srv)
		c.regionExplicit = "us-east1"
		c.flexSelections = []string{"machine-type=n4d-standard-2", "machine-type=n2d-standard-2"}
		c.skipFirewall = true
		return c
	}

	_ = newUtil().createInstanceViaBulkInsert(d)
	_ = newUtil().createInstanceViaBulkInsert(d)

	callsMu.Lock()
	defer callsMu.Unlock()
	assert.Equal(t, []string{"n4d-standard-2", "n2d-standard-2", "n2d-standard-2"}, calls)
}

func TestCreateInstanceViaBulkInsert_AdvanceOn(t *testing.T) {
	const quotaBody = `{"error":{"code":403,"message":"Quota exceeded","errors":[{"reason":"quotaExceeded"}]}}`
	const backendBody = `{"error":{"code":503,"message":"Internal error.","errors":[{"reason":"backendError"}]}}`

	// run returns the machine types attempted across two creates. The
	// first selection fails with body on every call, the second with a
	// fatal 404, so the second create shows whether the first was cooled.
	run := func(t *testing.T, advanceOn []string, body string, status int) []string {
		var mu sync.Mutex
		var calls []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req raw.BulkInsertInstanceResource
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			mu.Lock()
			calls = append(calls, req.InstanceProperties.MachineType)
			mu.Unlock()
			if req.InstanceProperties.MachineType == "n4d-standard-2" {
				writeJSONError(w, status, body)
				return
			}
			writeJSONError(w, http.StatusNotFound, `{"error":{"code":404,"message":"not found","errors":[{"reason":"notFound"}]}}`)
		}))
		defer srv.Close()

		d := NewDriver("runner-abc", t.TempDir())
		writeSSHKey(t, d)
		d.Network = "default"
		d.Project = "p"
		d.Region = "us-east1"
		d.FlexStockoutCooldown = time.Minute
		d.FlexAdvanceOn = advanceOn

		for i := 0; i < 2; i++ {
			c := newBulkInsertComputeUtil(t, srv)
			c.regionExplicit = "us-east1"
			c.flexSelections = []string{"machine-type=n4d-standard-2", "machine-type=n2d-standard-2"}
			c.skipFirewall = true
			_ = c.createInstanceViaBulkInsert(d)
		}
		mu.Lock()
		defer mu.Unlock()
		return calls
	}

	t.Run("quota is fatal by default", func(t *testing.T) {
		assert.Equal(t, []string{"n4d-standard-2", "n4d-standard-2"}, run(t, nil, quotaBody, http.StatusForbidden))
	})
	t.Run("quota advances and cools when opted in", func(t *testing.T) {
		assert.Equal(t, []string{"n4d-standard-2", "n2d-standard-2", "n2d-standard-2"}, run(t, []string{"stockout,quota"}, quotaBody, http.StatusForbidden))
	})
	t.Run("backend advances without cooling when opted in", func(t *testing.T) {
		assert.Equal(t, []string{"n4d-standard-2", "n2d-standard-2", "n4d-standard-2", "n2d-standard-2"}, run(t, []string{"stockout", "backend"}, backendBody, http.StatusServiceUnavailable))
	})
}

func TestParseFailureClasses(t *testing.T) {
	got, err := parseFailureClasses([]string{"stockout, Quota", "backend"})
	require.NoError(t, err)
	assert.Equal(t, map[failureClass]bool{failureStockout: true, failureQuota: true, failureBackend: true}, got)
	_, err = parseFailureClasses([]string{"stockout,rainbows"})
	assert.EqualError(t, err, `unknown class "rainbows" (known: stockout, quota, backend)`)
}

func TestZoneFromBulkInsertOp(t *testing.T) {
	mkOp := func(perLoc map[string]raw.BulkInsertOperationStatus) *raw.Operation {
		if perLoc == nil {
			return &raw.Operation{}
		}
		return &raw.Operation{
			InstancesBulkInsertOperationMetadata: &raw.InstancesBulkInsertOperationMetadata{
				PerLocationStatus: perLoc,
			},
		}
	}

	cases := map[string]struct {
		op       *raw.Operation
		wantZone string
		wantOk   bool
	}{
		"nil op": {op: nil, wantOk: false},
		"no bulkInsert metadata": {
			op:     &raw.Operation{},
			wantOk: false,
		},
		"single placed zone": {
			op: mkOp(map[string]raw.BulkInsertOperationStatus{
				"zones/us-east1-b": {CreatedVmCount: 1, TargetVmCount: 1, Status: "DONE"},
			}),
			wantZone: "us-east1-b",
			wantOk:   true,
		},
		"failure: zone present but created=0": {
			op: mkOp(map[string]raw.BulkInsertOperationStatus{
				"zones/us-east1-b": {CreatedVmCount: 0, FailedToCreateVmCount: 1, TargetVmCount: 1, Status: "DONE"},
			}),
			wantOk: false,
		},
		"empty perLocationStatus map": {
			op:     mkOp(map[string]raw.BulkInsertOperationStatus{}),
			wantOk: false,
		},
		"multi-zone, exactly one created -> that zone": {
			// Defensive: a failed attempt in one zone plus a success in
			// another. The created one is unambiguous.
			op: mkOp(map[string]raw.BulkInsertOperationStatus{
				"zones/us-east1-c": {CreatedVmCount: 0, FailedToCreateVmCount: 1},
				"zones/us-east1-d": {CreatedVmCount: 1, TargetVmCount: 1, Status: "DONE"},
			}),
			wantZone: "us-east1-d",
			wantOk:   true,
		},
		"ambiguous: two zones both created -> no guess": {
			op: mkOp(map[string]raw.BulkInsertOperationStatus{
				"zones/us-east1-b": {CreatedVmCount: 1},
				"zones/us-east1-d": {CreatedVmCount: 1},
			}),
			wantOk: false,
		},
		"location key without zones/ prefix -> fallback (not trusted)": {
			op: mkOp(map[string]raw.BulkInsertOperationStatus{
				"us-east1-b": {CreatedVmCount: 1},
			}),
			wantOk: false,
		},
		"location key is a self-link -> fallback (not trusted)": {
			op: mkOp(map[string]raw.BulkInsertOperationStatus{
				"https://www.googleapis.com/compute/v1/projects/p/zones/us-east1-b": {CreatedVmCount: 1},
			}),
			wantOk: false,
		},
		"location key with empty zone suffix -> fallback": {
			op: mkOp(map[string]raw.BulkInsertOperationStatus{
				"zones/": {CreatedVmCount: 1},
			}),
			wantOk: false,
		},
		"location key with trailing whitespace -> fallback": {
			op: mkOp(map[string]raw.BulkInsertOperationStatus{
				"zones/us-east1-b ": {CreatedVmCount: 1},
			}),
			wantOk: false,
		},
		"location key with leading whitespace -> fallback": {
			op: mkOp(map[string]raw.BulkInsertOperationStatus{
				" zones/us-east1-b": {CreatedVmCount: 1},
			}),
			wantOk: false,
		},
		"region-shaped key (no zone suffix) -> fallback": {
			op: mkOp(map[string]raw.BulkInsertOperationStatus{
				"zones/us-east1": {CreatedVmCount: 1},
			}),
			wantOk: false,
		},
		"multi-segment region zone parses": {
			op: mkOp(map[string]raw.BulkInsertOperationStatus{
				"zones/northamerica-northeast2-a": {CreatedVmCount: 1},
			}),
			wantZone: "northamerica-northeast2-a",
			wantOk:   true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			zone, ok := zoneFromBulkInsertOp(tc.op)
			assert.Equal(t, tc.wantOk, ok)
			assert.Equal(t, tc.wantZone, zone)
		})
	}
}

// TestResolvePlacedZone verifies the zone-resolution decision that backs
// finishPostCreate: when the bulkInsert operation already reported a
// placement zone (c.placedZone), resolvePlacedZone returns it WITHOUT
// issuing an AggregatedList call (the rate-limited, orphan-prone path).
// When placedZone is empty it must fall back to AggregatedList. We assert
// by observing whether the test server's AggregatedList path is hit.
func TestResolvePlacedZone(t *testing.T) {
	for _, tc := range []struct {
		name              string
		placedZone        string
		wantAggregatedHit bool
		wantZone          string
	}{
		{name: "placedZone set: no AggregatedList", placedZone: "us-east1-b", wantAggregatedHit: false, wantZone: "us-east1-b"},
		{name: "placedZone empty: falls back to AggregatedList", placedZone: "", wantAggregatedHit: true, wantZone: "us-east1-c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var aggregatedHit atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/aggregated/instances") {
					aggregatedHit.Store(true)
					resp := raw.InstanceAggregatedList{
						Items: map[string]raw.InstancesScopedList{
							"zones/us-east1-c": {Instances: []*raw.Instance{{
								Name: "runner-abc",
								Zone: "https://www.googleapis.com/compute/v1/projects/p/zones/us-east1-c",
							}}},
						},
					}
					body, _ := googleapi.WithoutDataWrapper.JSONReader(resp)
					fmt.Fprint(w, body)
					return
				}
				t.Errorf("unexpected non-AggregatedList call to %s", r.URL.Path)
			}))
			defer srv.Close()

			c := newBulkInsertComputeUtil(t, srv)
			c.placedZone = tc.placedZone

			zone, err := c.resolvePlacedZone()
			require.NoError(t, err)

			assert.Equal(t, tc.wantZone, zone)
			assert.Equal(t, tc.wantAggregatedHit, aggregatedHit.Load(),
				"AggregatedList should be called only when placedZone is empty")
		})
	}
}

// TestAttemptBulkInsertForSelection_SuccessSetsPlacedZone is the
// end-to-end regression guard for #155: a successful bulkInsert must
// populate c.placedZone from the operation's PerLocationStatus, so the
// subsequent finishPostCreate resolves the zone WITHOUT an
// AggregatedList call. A regression (e.g. discarding the completed
// operation, or not assigning placedZone) would leave placedZone empty
// and fail this test.
func TestAttemptBulkInsertForSelection_SuccessSetsPlacedZone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/aggregated/instances") {
			t.Errorf("AggregatedList must not be called when the operation reports the zone; got %s", r.URL.Path)
		}
		var op raw.Operation
		switch {
		case strings.Contains(r.URL.Path, "/instances/bulkInsert"):
			// The async BulkInsert call returns a (pending) operation.
			op = raw.Operation{Name: "op-bulk-1", Status: "RUNNING"}
		case strings.Contains(r.URL.Path, "/operations/op-bulk-1"):
			// RegionOperations.Wait returns the DONE op carrying the
			// placed-zone metadata.
			op = raw.Operation{
				Name:   "op-bulk-1",
				Status: "DONE",
				InstancesBulkInsertOperationMetadata: &raw.InstancesBulkInsertOperationMetadata{
					PerLocationStatus: map[string]raw.BulkInsertOperationStatus{
						"zones/us-east1-d": {CreatedVmCount: 1, TargetVmCount: 1, Status: "DONE"},
					},
				},
			}
		default:
			op = raw.Operation{Name: "op-bulk-1", Status: "DONE"}
		}
		body, _ := googleapi.WithoutDataWrapper.JSONReader(op)
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	c := newBulkInsertComputeUtil(t, srv)
	c.regionExplicit = "us-east1"
	// Pre-seed a stale zone to prove the reset path clears it before use.
	c.placedZone = "stale-zone-from-prior-attempt"

	class, err := c.attemptBulkInsertForSelection(withSSHKey(t, &Driver{Network: "default"}), flexSelection{MachineType: "n4-standard-2"})

	require.NoError(t, err)
	assert.Equal(t, failureClass(""), class)
	assert.Equal(t, "us-east1-d", c.placedZone, "placedZone must come from the operation's PerLocationStatus")
}

// TestAttemptBulkInsertForSelection_SalvagesZoneOnOpError covers the
// partial-failure case: the bulkInsert operation completes with an error
// overall, but its PerLocationStatus shows a VM was placed. The placed
// zone must be salvaged onto d.ResolvedZone so the later Remove can
// delete the VM directly instead of orphaning it / relying on the
// rate-limited AggregatedList lookup.
func TestAttemptBulkInsertForSelection_SalvagesZoneOnOpError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var op raw.Operation
		switch {
		case strings.Contains(r.URL.Path, "/instances/bulkInsert"):
			op = raw.Operation{Name: "op-bulk-err", Status: "RUNNING"}
		case strings.Contains(r.URL.Path, "/operations/op-bulk-err"):
			// DONE but with a (non-stockout) operation error, AND a
			// placement recorded in PerLocationStatus.
			op = raw.Operation{
				Name:   "op-bulk-err",
				Status: "DONE",
				Error: &raw.OperationError{
					Errors: []*raw.OperationErrorErrors{{Code: "INTERNAL_ERROR", Message: "boom"}},
				},
				InstancesBulkInsertOperationMetadata: &raw.InstancesBulkInsertOperationMetadata{
					PerLocationStatus: map[string]raw.BulkInsertOperationStatus{
						"zones/us-east1-d": {CreatedVmCount: 1, TargetVmCount: 1, Status: "DONE"},
					},
				},
			}
		default:
			op = raw.Operation{Name: "op-bulk-err", Status: "DONE"}
		}
		body, _ := googleapi.WithoutDataWrapper.JSONReader(op)
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	c := newBulkInsertComputeUtil(t, srv)
	c.regionExplicit = "us-east1"
	d := withSSHKey(t, &Driver{Network: "default"})

	class, err := c.attemptBulkInsertForSelection(d, flexSelection{MachineType: "n4-standard-2"})

	require.Error(t, err)
	assert.Equal(t, failureBackend, class)
	// The VM was placed; its zone must be recorded for cleanup.
	assert.Equal(t, "us-east1-d", d.ResolvedZone, "placed zone must be salvaged onto the Driver for Remove")
	// placedZone (the success-path field) must NOT be set on a failed op.
	assert.Empty(t, c.placedZone)
}

func TestBuildBulkInsertInstanceProperties_ServiceAccount(t *testing.T) {
	c := &ComputeUtil{
		project:        "p",
		networkProject: "p",
		diskTypeURL:    "pd-balanced",
		globalURL:      apiURL + "p/global",
	}
	sel := flexSelection{MachineType: "n2d-standard-2"}
	newDriver := func(noSA bool) *Driver {
		return &Driver{
			MachineImage:     "ubuntu-os-cloud/global/images/ubuntu-2204",
			DiskSize:         20,
			DiskType:         "pd-balanced",
			Network:          "default",
			ServiceAccount:   defaultServiceAccount,
			NoServiceAccount: noSA,
			Scopes:           defaultScopes,
		}
	}

	props, err := c.buildBulkInsertInstanceProperties(withSSHKey(t, newDriver(false)), sel)
	require.NoError(t, err)
	require.Len(t, props.ServiceAccounts, 1)
	assert.Equal(t, defaultServiceAccount, props.ServiceAccounts[0].Email)
	assert.Equal(t, strings.Split(defaultScopes, ","), props.ServiceAccounts[0].Scopes)

	props, err = c.buildBulkInsertInstanceProperties(withSSHKey(t, newDriver(true)), sel)
	require.NoError(t, err)
	assert.Nil(t, props.ServiceAccounts)
}

func TestBuildBulkInsertInstanceProperties_SelectionSubnetwork(t *testing.T) {
	d := withSSHKey(t, &Driver{
		MachineImage:   "ubuntu-os-cloud/global/images/ubuntu-2204",
		DiskSize:       20,
		Network:        "default",
		ServiceAccount: defaultServiceAccount,
		Scopes:         defaultScopes,
	})
	c := &ComputeUtil{
		project:        "p",
		networkProject: "net",
		diskTypeURL:    "pd-balanced",
		globalURL:      apiURL + "p/global",
		regionExplicit: "us-east1",
		subnetwork:     "projects/net/regions/us-east1/subnetworks/home",
	}

	props, err := c.buildBulkInsertInstanceProperties(d, flexSelection{MachineType: "n4-standard-2"})
	require.NoError(t, err)
	assert.Equal(t, "projects/net/regions/us-east1/subnetworks/home", props.NetworkInterfaces[0].Subnetwork)

	props, err = c.buildBulkInsertInstanceProperties(d, flexSelection{
		MachineType: "n4-standard-2",
		Region:      "us-east4",
		Subnetwork:  "projects/net/regions/us-east4/subnetworks/fallback",
	})
	require.NoError(t, err)
	assert.Equal(t, "projects/net/regions/us-east4/subnetworks/fallback", props.NetworkInterfaces[0].Subnetwork)

	c.subnetwork = "shared"
	props, err = c.buildBulkInsertInstanceProperties(d, flexSelection{MachineType: "n4-standard-2", Region: "us-east4"})
	require.NoError(t, err)
	assert.Equal(t, "projects/net/regions/us-east4/subnetworks/shared", props.NetworkInterfaces[0].Subnetwork,
		"a bare subnetwork name resolves in the selection's region")
}

func TestAttemptBulkInsertForSelection_UsesSelectionRegion(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var op raw.Operation
		switch {
		case strings.Contains(r.URL.Path, "/instances/bulkInsert"):
			var req raw.BulkInsertInstanceResource
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			assert.Contains(t, req.LocationPolicy.Locations, "zones/us-east4-a")
			assert.NotContains(t, req.LocationPolicy.Locations, "zones/us-east1-c")
			op = raw.Operation{Name: "op-x", Status: "RUNNING"}
		default:
			op = raw.Operation{
				Name:   "op-x",
				Status: "DONE",
				InstancesBulkInsertOperationMetadata: &raw.InstancesBulkInsertOperationMetadata{
					PerLocationStatus: map[string]raw.BulkInsertOperationStatus{
						"zones/us-east4-a": {CreatedVmCount: 1, TargetVmCount: 1, Status: "DONE"},
					},
				},
			}
		}
		body, _ := googleapi.WithoutDataWrapper.JSONReader(op)
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	c := newBulkInsertComputeUtil(t, srv)
	c.zone = ""
	c.regionExplicit = "us-east1"
	c.locationZones = []string{"us-east1-c"}

	class, err := c.attemptBulkInsertForSelection(withSSHKey(t, &Driver{Network: "default"}), flexSelection{
		MachineType: "n4-standard-2",
		Region:      "us-east4",
		Zones:       []string{"us-east4-a"},
	})
	require.NoError(t, err)
	assert.Equal(t, failureClass(""), class)
	assert.Equal(t, "us-east4-a", c.placedZone)

	require.Len(t, paths, 2)
	assert.Contains(t, paths[0], "/regions/us-east4/instances/bulkInsert")
	assert.Contains(t, paths[1], "/regions/us-east4/operations/op-x")
}

func TestRegion_ZoneWinsOverExplicit(t *testing.T) {
	c := &ComputeUtil{regionExplicit: "us-east1"}
	assert.Equal(t, "us-east1", c.region())
	c.zone = "us-east4-b"
	assert.Equal(t, "us-east4", c.region())
	c.zone = "malformed"
	assert.Equal(t, "us-east1", c.region())
}

func TestRegionOfSubnetworkPath(t *testing.T) {
	assert.Equal(t, "us-east4", regionOfSubnetworkPath("projects/net/regions/us-east4/subnetworks/sub"))
	assert.Equal(t, "us-east4", regionOfSubnetworkPath("https://www.googleapis.com/compute/v1/projects/net/regions/us-east4/subnetworks/sub"))
	assert.Equal(t, "us-east4", regionOfSubnetworkPath("regions/us-east4/subnetworks/sub"))
	assert.Equal(t, "", regionOfSubnetworkPath("sub"))
}
