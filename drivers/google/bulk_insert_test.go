package google

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestBuildInstanceFlexibilityPolicy_Nil(t *testing.T) {
	c := &ComputeUtil{}
	p, err := c.buildInstanceFlexibilityPolicy(&Driver{})
	require.NoError(t, err)
	assert.Nil(t, p)
}

func TestBuildInstanceFlexibilityPolicy_Ranked(t *testing.T) {
	c := &ComputeUtil{
		flexSelections: []string{
			"machine-type=n2-standard-2",
			"machine-type=n2d-standard-2",
			"machine-type=c2-standard-4",
		},
	}
	p, err := c.buildInstanceFlexibilityPolicy(&Driver{})
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Len(t, p.InstanceSelections, 3)
	require.Contains(t, p.InstanceSelections, "rank-0")
	require.Contains(t, p.InstanceSelections, "rank-1")
	require.Contains(t, p.InstanceSelections, "rank-2")
	r0 := p.InstanceSelections["rank-0"]
	r1 := p.InstanceSelections["rank-1"]
	r2 := p.InstanceSelections["rank-2"]

	assert.Equal(t, int64(0), r0.Rank)
	assert.Equal(t, []string{"n2-standard-2"}, r0.MachineTypes)
	assert.Nil(t, r0.Disks)
	assert.Equal(t, int64(1), r1.Rank)
	assert.Equal(t, []string{"n2d-standard-2"}, r1.MachineTypes)
	assert.Equal(t, int64(2), r2.Rank)
	assert.Equal(t, []string{"c2-standard-4"}, r2.MachineTypes)
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

func TestBuildInstanceFlexibilityPolicy_PerSelectionDiskOverride(t *testing.T) {
	c := &ComputeUtil{
		flexSelections: []string{
			"machine-type=t2d-standard-2",
			"machine-type=n2d-standard-2",
			"machine-type=n4-standard-2,disk-type=hyperdisk-balanced,disk-iops=3000,disk-throughput=140",
		},
	}
	d := &Driver{
		MachineImage: "cos-cloud/global/images/family/cos-stable",
		DiskSize:     10,
		Labels:       []string{"team:runners", "env:ci"},
	}
	p, err := c.buildInstanceFlexibilityPolicy(d)
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Len(t, p.InstanceSelections, 3)
	require.Contains(t, p.InstanceSelections, "rank-0")
	require.Contains(t, p.InstanceSelections, "rank-1")
	require.Contains(t, p.InstanceSelections, "rank-2")
	r0 := p.InstanceSelections["rank-0"]
	assert.Equal(t, []string{"t2d-standard-2"}, r0.MachineTypes)
	assert.Nil(t, r0.Disks, "bare entry must not carry a disk override")

	r1 := p.InstanceSelections["rank-1"]
	assert.Nil(t, r1.Disks)

	r2 := p.InstanceSelections["rank-2"]
	assert.Equal(t, []string{"n4-standard-2"}, r2.MachineTypes)
	require.Len(t, r2.Disks, 1)
	assert.True(t, r2.Disks[0].Boot)
	assert.True(t, r2.Disks[0].AutoDelete)
	assert.Equal(t, bootDeviceName, r2.Disks[0].DeviceName)
	assert.Equal(t, "hyperdisk-balanced", r2.Disks[0].InitializeParams.DiskType)
	assert.Equal(t, int64(10), r2.Disks[0].InitializeParams.DiskSizeGb)
	assert.Equal(t, int64(3000), r2.Disks[0].InitializeParams.ProvisionedIops)
	assert.Equal(t, int64(140), r2.Disks[0].InitializeParams.ProvisionedThroughput)
	assert.Equal(t, map[string]string{"team": "runners", "env": "ci"}, r2.Disks[0].InitializeParams.Labels)
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

func TestBuildBulkInsertInstanceProperties_BareNamesNoZone(t *testing.T) {
	// Critical bulkInsert invariant: InstanceProperties must not pin to
	// a specific zone (MachineType / DiskType / AcceleratorType must be
	// bare names, not zonal URLs), so GCP can resolve them against the
	// placement zone chosen by the LocationPolicy.
	d := &Driver{
		MachineType:    "n2-standard-2",
		MachineImage:   "ubuntu-os-cloud/global/images/ubuntu-2204",
		DiskSize:       20,
		DiskType:       "pd-balanced",
		Network:        "default",
		ServiceAccount: defaultServiceAccount,
		Scopes:         defaultScopes,
	}
	c := &ComputeUtil{
		project:        "p",
		networkProject: "p",
		diskTypeURL:    "pd-balanced",
		globalURL:      apiURL + "p/global",
		accelerator:    "count=1,type=nvidia-tesla-t4",
	}

	props, err := c.buildBulkInsertInstanceProperties(d)
	require.NoError(t, err)
	require.NotNil(t, props)

	// MachineType: unset in flex mode (flex selections provide it).
	assert.Empty(t, props.MachineType)

	// DiskType: bare name on InitializeParams.
	require.Len(t, props.Disks, 1)
	require.NotNil(t, props.Disks[0].InitializeParams)
	assert.Equal(t, "pd-balanced", props.Disks[0].InitializeParams.DiskType)
	assert.NotContains(t, props.Disks[0].InitializeParams.DiskType, "/zones/")

	// AcceleratorType: bare name (the direct path uses a zonal URL here;
	// bulkInsert must not, or GCP rejects the InstanceProperties).
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
