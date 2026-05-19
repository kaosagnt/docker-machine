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
		{"us-east1-c:PREFERRED", "us-east1-c", "PREFERRED"},
		{"us-east1-d:DENY", "us-east1-d", "DENY"},
		{"us-east1-d:preferred", "us-east1-d", "PREFERRED"},
		// Pass-through for unknown preferences: GCP rejects them server-side
		// rather than us silently coercing — surfaces typos as API errors.
		{"us-east1-d:nonsense", "us-east1-d", "NONSENSE"},
	}
	for _, tc := range tests {
		t.Run(tc.entry, func(t *testing.T) {
			gotZone, gotPref := parseLocationZoneEntry(tc.entry)
			assert.Equal(t, tc.wantZone, gotZone)
			assert.Equal(t, tc.wantPref, gotPref)
		})
	}
}

func TestBuildLocationPolicy_Nil(t *testing.T) {
	// No zones → nil policy, GCP picks any zone in the region.
	c := &ComputeUtil{}
	assert.Nil(t, c.buildLocationPolicy())
}

func TestBuildLocationPolicy_Zones(t *testing.T) {
	c := &ComputeUtil{
		locationZones: []string{"us-east1-b", "us-east1-c:PREFERRED", "us-east1-d:DENY"},
	}
	p := c.buildLocationPolicy()
	require.NotNil(t, p)
	assert.Empty(t, p.TargetShape)
	require.Len(t, p.Locations, 3)
	assert.Equal(t, "ALLOW", p.Locations["zones/us-east1-b"].Preference)
	assert.Equal(t, "PREFERRED", p.Locations["zones/us-east1-c"].Preference)
	assert.Equal(t, "DENY", p.Locations["zones/us-east1-d"].Preference)
}

func TestBuildInstanceFlexibilityPolicy_Nil(t *testing.T) {
	c := &ComputeUtil{}
	assert.Nil(t, c.buildInstanceFlexibilityPolicy())
}

func TestBuildInstanceFlexibilityPolicy_Ranked(t *testing.T) {
	c := &ComputeUtil{
		flexMachineTypes: []string{"n2-standard-2", "n2d-standard-2", "c2-standard-4"},
	}
	p := c.buildInstanceFlexibilityPolicy()
	require.NotNil(t, p)
	require.Len(t, p.InstanceSelections, 3)

	r0 := p.InstanceSelections["rank-0"]
	r1 := p.InstanceSelections["rank-1"]
	r2 := p.InstanceSelections["rank-2"]

	assert.Equal(t, int64(0), r0.Rank)
	assert.Equal(t, []string{"n2-standard-2"}, r0.MachineTypes)
	assert.Equal(t, int64(1), r1.Rank)
	assert.Equal(t, []string{"n2d-standard-2"}, r1.MachineTypes)
	assert.Equal(t, int64(2), r2.Rank)
	assert.Equal(t, []string{"c2-standard-4"}, r2.MachineTypes)
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

	// MachineType: bare name, no /zones/ prefix.
	assert.Equal(t, "n2-standard-2", props.MachineType)
	assert.NotContains(t, props.MachineType, "/zones/")

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
		assert.False(t, d.usesBulkInsert())
	})
	t.Run("region alone is not enough", func(t *testing.T) {
		// Explicit opt-in is required: Region without --google-bulk-insert
		// keeps the direct path. Avoids accidentally switching modes
		// when an operator only meant to set a region for some
		// hypothetical future feature.
		d := &Driver{Region: "us-east1"}
		assert.False(t, d.usesBulkInsert())
	})
	t.Run("BulkInsert opt-in flips mode", func(t *testing.T) {
		d := &Driver{BulkInsert: true, Region: "us-east1"}
		assert.True(t, d.usesBulkInsert())
	})
}
