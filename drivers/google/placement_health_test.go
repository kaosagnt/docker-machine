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
	d.FlexStockoutProbeLease = 90 * time.Second
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
	assert.Equal(t, machineTypes(testSelections()), machineTypes(health.order(d, testSelections())))
	_, err := os.Stat(health.statePath)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestPlacementHealthCoolingSelectionsRemainAtBack(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	selections := testSelections()
	require.NoError(t, health.recordStockout(d, selections[0]))
	assert.Equal(t, []string{"n2d-standard-2", "n4-standard-2", "n4d-standard-2"}, machineTypes(health.order(d, selections)))
}

func TestPlacementHealthPreservesOrderWithinPartitions(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	selections := testSelections()
	require.NoError(t, health.recordStockout(d, selections[0]))
	require.NoError(t, health.recordStockout(d, selections[2]))
	assert.Equal(t, []string{"n2d-standard-2", "n4d-standard-2", "n4-standard-2"}, machineTypes(health.order(d, selections)))
}

func TestPlacementHealthPriorityProbeHolderAttemptsProbeFirst(t *testing.T) {
	health, d, now := newTestPlacementHealth(t)
	selections := testSelections()
	require.NoError(t, health.recordStockout(d, selections[0]))
	health.now = func() time.Time { return now.Add(61 * time.Second) }
	assert.Equal(t, []string{"n4d-standard-2", "n2d-standard-2", "n4-standard-2"}, machineTypes(health.order(d, selections)))
	assert.Equal(t, []string{"n2d-standard-2", "n4-standard-2", "n4d-standard-2"}, machineTypes(newPlacementHealth(d, health.now).order(d, selections)))
}

func TestPlacementHealthOnlyOneConcurrentPriorityProbeHolderUsesConfiguredOrder(t *testing.T) {
	health, d, now := newTestPlacementHealth(t)
	selections := testSelections()
	require.NoError(t, health.recordStockout(d, selections[0]))
	nowFn := func() time.Time { return now.Add(61 * time.Second) }

	const workers = 8
	orders := make(chan []string, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			orders <- machineTypes(newPlacementHealth(d, nowFn).order(d, selections))
		}()
	}
	wg.Wait()
	close(orders)
	probes := 0
	for order := range orders {
		if len(order) > 0 && order[0] == "n4d-standard-2" {
			probes++
		} else {
			assert.Equal(t, []string{"n2d-standard-2", "n4-standard-2", "n4d-standard-2"}, order)
		}
	}
	assert.Equal(t, 1, probes)
}

func TestPlacementHealthLowerRankedProbeIsAttemptedFirst(t *testing.T) {
	health, d, now := newTestPlacementHealth(t)
	selections := testSelections()
	require.NoError(t, health.recordStockout(d, selections[2]))
	health.now = func() time.Time { return now.Add(61 * time.Second) }
	assert.Equal(t, []string{"n4-standard-2", "n4d-standard-2", "n2d-standard-2"}, machineTypes(health.order(d, selections)))
}

func TestPlacementHealthProbeRemainderKeepsHealthyBeforeCooling(t *testing.T) {
	health, d, now := newTestPlacementHealth(t)
	selections := testSelections()
	require.NoError(t, health.recordStockoutAt(d, selections[0], now))
	require.NoError(t, health.recordStockoutAt(d, selections[2], now.Add(time.Second)))
	health.now = func() time.Time { return now.Add(62 * time.Second) }
	assert.Equal(t, []string{"n4d-standard-2", "n2d-standard-2", "n4-standard-2"}, machineTypes(health.order(d, selections)))
}

func TestPlacementHealthZeroProbeLeaseDisablesPriorityProbe(t *testing.T) {
	health, d, now := newTestPlacementHealth(t)
	d.FlexStockoutProbeLease = 0
	selections := testSelections()
	require.NoError(t, health.recordStockout(d, selections[0]))
	health.now = func() time.Time { return now.Add(61 * time.Second) }
	assert.Equal(t, []string{"n2d-standard-2", "n4-standard-2", "n4d-standard-2"}, machineTypes(health.order(d, selections)))
}

func TestPlacementHealthAllCoolingUsesConfiguredOrder(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	selections := testSelections()
	for _, selection := range selections {
		require.NoError(t, health.recordStockout(d, selection))
	}
	assert.Equal(t, machineTypes(selections), machineTypes(health.order(d, selections)))
}

func TestPlacementHealthPlacementSuccessClearsCooldownWithoutCreatingHealthyEntry(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	selection := testSelections()[0]
	require.NoError(t, health.recordPlacement(d, selection))
	_, err := os.Stat(health.statePath)
	assert.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, health.recordStockout(d, selection))
	require.NoError(t, health.recordPlacement(d, selection))
	assert.Equal(t, machineTypes(testSelections()), machineTypes(health.order(d, testSelections())))
}

func TestPlacementHealthCorruptStateIsRebuiltOnUpdate(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	require.NoError(t, os.WriteFile(health.statePath, []byte("not-json"), 0600))
	require.NoError(t, health.recordStockout(d, testSelections()[0]))
	state, err := health.load()
	require.NoError(t, err)
	require.Len(t, state.Classes, 1)
}

func TestPlacementHealthVersionlessStateIsRebuiltOnUpdate(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	require.NoError(t, os.WriteFile(health.statePath, []byte(`{}`), 0600))
	require.NoError(t, health.recordStockout(d, testSelections()[0]))
	state, err := health.load()
	require.NoError(t, err)
	assert.Equal(t, placementHealthVersion, state.Version)
}

func TestPlacementHealthOversizedStateIsNotOverwritten(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	original := make([]byte, placementHealthMaxFileSize+1)
	require.NoError(t, os.WriteFile(health.statePath, original, 0600))
	require.ErrorContains(t, health.recordStockout(d, testSelections()[0]), "exceeds")
	actual, err := os.ReadFile(health.statePath)
	require.NoError(t, err)
	assert.Equal(t, original, actual)
}

func TestPlacementHealthFutureVersionIsNotOverwritten(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	original := []byte(`{"version":999,"classes":[]}`)
	require.NoError(t, os.WriteFile(health.statePath, original, 0600))
	require.ErrorContains(t, health.recordStockout(d, testSelections()[0]), "unsupported placement health version")
	actual, err := os.ReadFile(health.statePath)
	require.NoError(t, err)
	assert.Equal(t, original, actual)
}

func TestPlacementHealthFutureVersionWithDifferentSchemaIsNotOverwritten(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	original := []byte(`{"version":999,"classes":{}}`)
	require.NoError(t, os.WriteFile(health.statePath, original, 0600))

	require.ErrorContains(t, health.recordStockout(d, testSelections()[0]), "unsupported placement health version")
	actual, err := os.ReadFile(health.statePath)
	require.NoError(t, err)
	assert.Equal(t, original, actual)
}

func TestPlacementHealthLegacyV1StateIsRebuilt(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	require.NoError(t, os.WriteFile(health.statePath, []byte(`{"version":1,"classes":{"opaque":{}}}`), 0600))
	require.NoError(t, health.recordStockout(d, testSelections()[0]))
	state, err := health.load()
	require.NoError(t, err)
	assert.Equal(t, placementHealthVersion, state.Version)
	require.Len(t, state.Classes, 1)
}

func TestPlacementHealthStateIsReadable(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	require.NoError(t, health.recordStockout(d, testSelections()[0]))
	data, err := os.ReadFile(health.statePath)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"machine_type": "n4d-standard-2"`)
	assert.NotContains(t, string(data), "sha256")
}

func TestPlacementHealthPrunesOldEntriesAndTempFiles(t *testing.T) {
	health, d, now := newTestPlacementHealth(t)
	old := placementClassHealth{Class: health.selectionClass(d, testSelections()[0]), LastStockout: now.Add(-25 * time.Hour)}
	state := &placementHealthFile{Version: placementHealthVersion, Classes: []placementClassHealth{old}}
	require.NoError(t, health.save(state))
	tmp := filepath.Join(filepath.Dir(health.statePath), placementHealthFilename+".tmp-orphan")
	require.NoError(t, os.WriteFile(tmp, []byte("orphan"), 0600))
	require.NoError(t, os.Chtimes(tmp, now.Add(-25*time.Hour), now.Add(-25*time.Hour)))
	require.NoError(t, health.recordStockout(d, testSelections()[1]))
	loaded, err := health.load()
	require.NoError(t, err)
	require.Len(t, loaded.Classes, 1)
	assert.Equal(t, "n2d-standard-2", loaded.Classes[0].Class.MachineType)
	_, err = os.Stat(tmp)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestPlacementHealthKeepsActiveCooldownOlderThanPruneAge(t *testing.T) {
	health, d, now := newTestPlacementHealth(t)
	entry := placementClassHealth{
		Class:         health.selectionClass(d, testSelections()[0]),
		LastStockout:  now.Add(-25 * time.Hour),
		CooldownUntil: now.Add(time.Hour),
	}
	state := &placementHealthFile{Version: placementHealthVersion, Classes: []placementClassHealth{entry}}
	assert.False(t, health.prune(state, now))
	assert.Len(t, state.Classes, 1)
}

func TestPlacementHealthClassBoundKeepsMostRecentActiveEntries(t *testing.T) {
	health, d, now := newTestPlacementHealth(t)
	state := newPlacementHealthFile()
	for i := 0; i < placementHealthMaxClasses+10; i++ {
		state.Classes = append(state.Classes, placementClassHealth{
			Class:         health.selectionClass(d, flexSelection{MachineType: fmt.Sprintf("type-%d", i)}),
			LastStockout:  now,
			CooldownUntil: now.Add(time.Hour),
		})
	}
	assert.True(t, health.prune(state, now))
	assert.Len(t, state.Classes, placementHealthMaxClasses)
}

func TestPlacementHealthBoundsClassesWhileRecording(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	for i := 0; i < placementHealthMaxClasses+10; i++ {
		selection := flexSelection{MachineType: fmt.Sprintf("type-%d", i)}
		require.NoError(t, health.recordStockout(d, selection))
	}
	state, err := health.load()
	require.NoError(t, err)
	assert.Len(t, state.Classes, placementHealthMaxClasses)
}

func TestPlacementHealthConcurrentStockoutsAreNotLost(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	var wg sync.WaitGroup
	for _, selection := range testSelections() {
		selection := selection
		wg.Add(1)
		go func() { defer wg.Done(); require.NoError(t, health.recordStockout(d, selection)) }()
	}
	wg.Wait()
	state, err := health.load()
	require.NoError(t, err)
	assert.Len(t, state.Classes, len(testSelections()))
}

func TestPlacementHealthNewestAttemptObservationWins(t *testing.T) {
	health, d, now := newTestPlacementHealth(t)
	selection := testSelections()[0]
	require.NoError(t, health.recordStockoutAt(d, selection, now))
	require.NoError(t, health.recordPlacementAt(d, selection, now.Add(time.Second)))
	require.NoError(t, health.recordStockoutAt(d, selection, now.Add(-time.Second)))

	state, err := health.load()
	require.NoError(t, err)
	require.Len(t, state.Classes, 1)
	assert.True(t, state.Classes[0].CooldownUntil.IsZero())
	assert.Equal(t, now.Add(time.Second), state.Classes[0].LastPlaced)
}

func TestPlacementHealthLatestCompletedOutcomeWins(t *testing.T) {
	health, d, now := newTestPlacementHealth(t)
	selection := testSelections()[0]
	require.NoError(t, health.recordStockoutAt(d, selection, now.Add(time.Second)))
	require.NoError(t, health.recordPlacementAt(d, selection, now.Add(2*time.Second)))

	state, err := health.load()
	require.NoError(t, err)
	require.Len(t, state.Classes, 1)
	assert.True(t, state.Classes[0].CooldownUntil.IsZero())
	assert.Equal(t, now.Add(2*time.Second), state.Classes[0].LastPlaced)
}

func TestPlacementHealthAtomicWritesRemainReadable(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	stop, done := make(chan struct{}), make(chan struct{})
	errs := make(chan error, 1)
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
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
	}()
	for i := 0; i < 50; i++ {
		selection := testSelections()[i%len(testSelections())]
		require.NoError(t, health.recordStockout(d, selection))
		require.NoError(t, health.recordPlacement(d, selection))
	}
	close(stop)
	<-done
	select {
	case err := <-errs:
		require.NoError(t, err)
	default:
	}
}

func TestPlacementHealthClassUsesEffectiveConfiguration(t *testing.T) {
	health, d, _ := newTestPlacementHealth(t)
	selection := flexSelection{MachineType: "n2d-standard-2"}
	d.DiskType = "pd-balanced"
	class := health.selectionClass(d, selection)
	d.DiskType = "hyperdisk-balanced"
	assert.False(t, samePlacementClass(class, health.selectionClass(d, selection)))
	d.DiskType, d.Accelerator = "pd-balanced", "count=1,type=nvidia-l4"
	assert.False(t, samePlacementClass(class, health.selectionClass(d, selection)))
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
