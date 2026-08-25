package google

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testSelections() []flexSelection {
	return []flexSelection{
		{MachineType: "n4d-standard-2", DiskType: "hyperdisk-balanced", DiskIops: 3000, DiskThroughput: 140},
		{MachineType: "n2d-standard-2", DiskType: "pd-balanced"},
		{MachineType: "n4-standard-2", DiskType: "hyperdisk-balanced", DiskIops: 3000, DiskThroughput: 140},
	}
}

func newTestPlacementHealth(t *testing.T) (*placementHealth, *Driver, time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)
	d := NewDriver("runner-1", t.TempDir())
	d.Project = "project"
	d.Region = "us-east1"
	d.LocationZones = []string{"us-east1-b", "us-east1-c", "us-east1-d"}
	d.FlexStockoutCooldown = time.Minute
	return newPlacementHealth(d, func() time.Time { return now }), d, now
}

func machineTypes(selections []flexSelection) []string {
	result := make([]string, 0, len(selections))
	for _, selection := range selections {
		result = append(result, selection.MachineType)
	}
	return result
}

func TestPlacementHealthDisabledPreservesConfiguredOrder(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	d.FlexStockoutCooldown = 0

	ordered := health.order(d, testSelections())

	assert.Equal(t, []string{"n4d-standard-2", "n2d-standard-2", "n4-standard-2"}, machineTypes(ordered))
	_, err := os.Stat(health.statePath)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestPlacementHealthStockoutMovesSelectionBehindHealthySelections(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	selections := testSelections()
	require.NoError(t, health.recordStockout(d, selections[0]))

	ordered := health.order(d, selections)

	assert.Equal(t, []string{"n2d-standard-2", "n4-standard-2", "n4d-standard-2"}, machineTypes(ordered))
}

func TestPlacementHealthPreservesConfiguredOrderWithinPartitions(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	selections := testSelections()
	require.NoError(t, health.recordStockout(d, selections[0]))
	require.NoError(t, health.recordStockout(d, selections[2]))

	ordered := health.order(d, selections)

	assert.Equal(t, []string{"n2d-standard-2", "n4d-standard-2", "n4-standard-2"}, machineTypes(ordered))
}

func TestPlacementHealthExpiredCooldownRestoresConfiguredOrder(t *testing.T) {
	health, d, now := newTestPlacementHealth(t)
	selections := testSelections()
	for _, selection := range selections {
		require.NoError(t, health.recordStockout(d, selection))
	}
	health.now = func() time.Time { return now.Add(61 * time.Second) }

	ordered := health.order(d, selections)

	assert.Equal(t, machineTypes(selections), machineTypes(ordered))
}

func TestPlacementHealthAllCoolingUsesConfiguredOrder(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	selections := testSelections()
	for _, selection := range selections {
		require.NoError(t, health.recordStockout(d, selection))
	}

	ordered := health.order(d, selections)

	assert.Equal(t, machineTypes(selections), machineTypes(ordered))
}

func TestPlacementHealthPlacementSuccessClearsCooldown(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	selections := testSelections()
	require.NoError(t, health.recordStockout(d, selections[0]))
	require.NoError(t, health.recordPlacement(d, selections[0]))

	ordered := health.order(d, selections)

	assert.Equal(t, []string{"n4d-standard-2", "n2d-standard-2", "n4-standard-2"}, machineTypes(ordered))
}

func TestPlacementHealthCorruptStateFailsOpen(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(health.statePath), 0700))
	require.NoError(t, os.WriteFile(health.statePath, []byte("not-json"), 0600))

	ordered := health.order(d, testSelections())

	assert.Equal(t, []string{"n4d-standard-2", "n2d-standard-2", "n4-standard-2"}, machineTypes(ordered))
}

func TestPlacementHealthUnknownVersionIsNotOverwritten(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	original := []byte(`{"version":2,"classes":{}}`)
	require.NoError(t, os.WriteFile(health.statePath, original, 0600))

	err := health.recordStockout(d, testSelections()[0])

	require.ErrorContains(t, err, "unsupported placement health version")
	actual, readErr := os.ReadFile(health.statePath)
	require.NoError(t, readErr)
	assert.Equal(t, original, actual)
}

func TestPlacementHealthOversizedStateFailsOpen(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	require.NoError(t, os.WriteFile(health.statePath, make([]byte, placementHealthMaxFileSize+1), 0600))

	ordered := health.order(d, testSelections())

	assert.Equal(t, machineTypes(testSelections()), machineTypes(ordered))
}

func TestPlacementHealthConcurrentStockoutsAreNotLost(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	selections := testSelections()

	var wg sync.WaitGroup
	for _, selection := range selections {
		selection := selection
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, health.recordStockout(d, selection))
		}()
	}
	wg.Wait()

	state, err := health.load()
	require.NoError(t, err)
	assert.Len(t, state.Classes, len(selections))
}

func TestPlacementHealthAtomicWritesRemainReadable(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	selections := testSelections()
	stop := make(chan struct{})
	errs := make(chan error, 1)

	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				data, err := os.ReadFile(health.statePath)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					select {
					case errs <- err:
					default:
					}
					return
				}
				var state placementHealthFile
				if err := json.Unmarshal(data, &state); err != nil || state.Version != placementHealthVersion {
					select {
					case errs <- fmt.Errorf("invalid state: %w", err):
					default:
					}
					return
				}
			}
		}
	}()

	for i := 0; i < 50; i++ {
		selection := selections[i%len(selections)]
		require.NoError(t, health.recordStockout(d, selection))
		require.NoError(t, health.recordPlacement(d, selection))
	}
	close(stop)
	select {
	case err := <-errs:
		require.NoError(t, err)
	default:
	}
}

func TestPlacementHealthKeySeparatesPlacementConfiguration(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	selection := testSelections()[0]
	key := health.selectionKey(d, selection)

	d.Project = "other-project"
	assert.NotEqual(t, key, health.selectionKey(d, selection))
	d.Project = "project"
	d.LocationZones = []string{"us-east1-b"}
	assert.NotEqual(t, key, health.selectionKey(d, selection))
	selection.DiskIops++
	assert.NotEqual(t, key, health.selectionKey(d, selection))
}

func TestPlacementHealthKeyUsesEffectiveDriverDefaults(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	selection := flexSelection{MachineType: "n2d-standard-2"}
	d.DiskType = "pd-balanced"
	key := health.selectionKey(d, selection)

	d.DiskType = "hyperdisk-balanced"
	assert.NotEqual(t, key, health.selectionKey(d, selection))
	d.DiskType = "pd-balanced"
	d.Accelerator = "count=1,type=nvidia-l4"
	assert.NotEqual(t, key, health.selectionKey(d, selection))
	d.Accelerator = ""
	d.MinCPUPlatform = "AMD Milan"
	assert.NotEqual(t, key, health.selectionKey(d, selection))
}

func TestPlacementHealthUnavailableStoreDoesNotWriteState(t *testing.T) {
	d := &Driver{FlexStockoutCooldown: time.Minute}
	health := newPlacementHealth(d, time.Now)
	selection := flexSelection{MachineType: "n2d-standard-2"}

	assert.Equal(t, []flexSelection{selection}, health.order(d, []flexSelection{selection}))
	require.NoError(t, health.recordStockout(d, selection))
	require.NoError(t, health.recordPlacement(d, selection))
	assert.Empty(t, health.statePath)
}
