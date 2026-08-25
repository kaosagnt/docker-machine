package google

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/docker/machine/libmachine/log"
)

const (
	placementHealthVersion     = 1
	placementHealthFilename    = "google-flex-stockout-health.json"
	placementHealthLockSuffix  = ".lock"
	placementHealthLockTimeout = time.Second
	placementHealthMaxClasses  = 128
	placementHealthMaxFileSize = 128 * 1024
)

type placementClassHealth struct {
	CooldownUntil time.Time `json:"cooldown_until,omitempty"`
	ProbeUntil    time.Time `json:"probe_until,omitempty"`
	ProbeOwner    string    `json:"probe_owner,omitempty"`
	LastStockout  time.Time `json:"last_stockout,omitempty"`
	LastPlaced    time.Time `json:"last_placed,omitempty"`
}

type placementHealthFile struct {
	Version int                             `json:"version"`
	Classes map[string]placementClassHealth `json:"classes"`
}

type placementHealth struct {
	statePath string
	lockPath  string
	available bool
	now       func() time.Time
}

func newPlacementHealth(d *Driver, now func() time.Time) *placementHealth {
	if d.BaseDriver == nil || d.StorePath == "" {
		return &placementHealth{now: now}
	}
	statePath := filepath.Join(d.StorePath, placementHealthFilename)
	return &placementHealth{
		statePath: statePath,
		lockPath:  statePath + placementHealthLockSuffix,
		available: true,
		now:       now,
	}
}

func (h *placementHealth) selectionKey(d *Driver, selection flexSelection) string {
	zones := append([]string(nil), d.LocationZones...)
	sort.Strings(zones)
	diskType := selection.DiskType
	if diskType == "" {
		diskType = d.DiskType
	}
	diskIops := selection.DiskIops
	if diskIops == 0 {
		diskIops = int64(d.ProvisionedIops)
	}
	diskThroughput := selection.DiskThroughput
	if diskThroughput == 0 {
		diskThroughput = int64(d.ProvisionedThroughput)
	}
	value := fmt.Sprintf("project=%s\nregion=%s\nzones=%s\nmachine=%s\ndisk=%s\niops=%d\nthroughput=%d\naccelerator=%s\nmin_cpu_platform=%s",
		d.Project, d.Region, strings.Join(zones, ","), selection.MachineType, diskType,
		diskIops, diskThroughput, d.Accelerator, d.MinCPUPlatform)
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (h *placementHealth) order(d *Driver, configured []flexSelection) []flexSelection {
	if d.FlexStockoutCooldown <= 0 || len(configured) < 2 || !h.available {
		return append([]flexSelection(nil), configured...)
	}

	unlock, err := acquirePlacementHealthLock(h.lockPath, placementHealthLockTimeout)
	if err != nil {
		log.Warnf("Could not lock bulkInsert placement health; using configured order: %v", err)
		return append([]flexSelection(nil), configured...)
	}
	defer unlock()

	state, err := h.load()
	if err != nil {
		log.Warnf("Could not read bulkInsert placement health; using configured order: %v", err)
		return append([]flexSelection(nil), configured...)
	}

	now := h.now()
	healthy := make(map[string]struct{}, len(configured))
	type probeCandidate struct {
		key      string
		position int
		expires  time.Time
	}
	var candidates []probeCandidate

	for i, selection := range configured {
		key := h.selectionKey(d, selection)
		entry, found := state.Classes[key]
		if !found || entry.CooldownUntil.IsZero() {
			healthy[key] = struct{}{}
			continue
		}
		if now.Before(entry.CooldownUntil) || now.Before(entry.ProbeUntil) {
			continue
		}
		candidates = append(candidates, probeCandidate{key: key, position: i, expires: entry.CooldownUntil})
	}

	// When every class is constrained, preserve the existing bounded ladder
	// pass. Suppressing every selection here would require a retry-after contract
	// with Runner to avoid local create/remove churn.
	if len(healthy) == 0 {
		return append([]flexSelection(nil), configured...)
	}

	probeKey := ""
	if len(candidates) > 0 {
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].expires.Equal(candidates[j].expires) {
				return candidates[i].position < candidates[j].position
			}
			return candidates[i].expires.Before(candidates[j].expires)
		})
		probe := candidates[0]
		entry := state.Classes[probe.key]
		entry.ProbeUntil = now.Add(d.FlexStockoutProbeLease)
		entry.ProbeOwner = d.MachineName
		state.Classes[probe.key] = entry
		if err := h.save(state); err != nil {
			log.Warnf("Could not claim bulkInsert placement probe; using configured order: %v", err)
			return append([]flexSelection(nil), configured...)
		}
		probeKey = probe.key
		log.Infof("Probing bulkInsert flex selection machine-type=%q after stockout cooldown", configured[probe.position].MachineType)
	}

	ordered := make([]flexSelection, 0, len(configured))
	for _, selection := range configured {
		if _, ok := healthy[h.selectionKey(d, selection)]; ok {
			ordered = append(ordered, selection)
		}
	}
	if probeKey != "" {
		for _, selection := range configured {
			if h.selectionKey(d, selection) == probeKey {
				ordered = append(ordered, selection)
				break
			}
		}
	}
	for _, selection := range configured {
		key := h.selectionKey(d, selection)
		if _, ok := healthy[key]; ok || key == probeKey {
			continue
		}
		log.Infof("Skipping cooling bulkInsert flex selection machine-type=%q while another selection is eligible", selection.MachineType)
	}
	return ordered
}

func (h *placementHealth) recordStockout(d *Driver, selection flexSelection) error {
	if d.FlexStockoutCooldown <= 0 || !h.available {
		return nil
	}
	return h.update(func(state *placementHealthFile, now time.Time) {
		key := h.selectionKey(d, selection)
		entry := state.Classes[key]
		entry.CooldownUntil = now.Add(d.FlexStockoutCooldown)
		entry.ProbeUntil = time.Time{}
		entry.ProbeOwner = ""
		entry.LastStockout = now
		state.Classes[key] = entry
	})
}

func (h *placementHealth) recordPlacement(d *Driver, selection flexSelection) error {
	if d.FlexStockoutCooldown <= 0 || !h.available {
		return nil
	}
	return h.update(func(state *placementHealthFile, now time.Time) {
		key := h.selectionKey(d, selection)
		entry := state.Classes[key]
		entry.CooldownUntil = time.Time{}
		entry.ProbeUntil = time.Time{}
		entry.ProbeOwner = ""
		entry.LastPlaced = now
		state.Classes[key] = entry
	})
}

func (h *placementHealth) update(change func(*placementHealthFile, time.Time)) error {
	unlock, err := acquirePlacementHealthLock(h.lockPath, placementHealthLockTimeout)
	if err != nil {
		return err
	}
	defer unlock()

	state, err := h.load()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		state = newPlacementHealthFile()
	}
	change(state, h.now())
	h.prune(state)
	return h.save(state)
}

func newPlacementHealthFile() *placementHealthFile {
	return &placementHealthFile{Version: placementHealthVersion, Classes: make(map[string]placementClassHealth)}
}

func (h *placementHealth) load() (*placementHealthFile, error) {
	file, err := os.Open(h.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return newPlacementHealthFile(), nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, placementHealthMaxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > placementHealthMaxFileSize {
		return nil, fmt.Errorf("placement health file exceeds %d bytes", placementHealthMaxFileSize)
	}
	var state placementHealthFile
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.Version != placementHealthVersion {
		return nil, fmt.Errorf("unsupported placement health version %d", state.Version)
	}
	if state.Classes == nil {
		state.Classes = make(map[string]placementClassHealth)
	}
	return &state, nil
}

func (h *placementHealth) save(state *placementHealthFile) error {
	if err := os.MkdirAll(filepath.Dir(h.statePath), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(h.statePath), placementHealthFilename+".tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replacePlacementHealthFile(tmpName, h.statePath)
}

func (h *placementHealth) prune(state *placementHealthFile) {
	if len(state.Classes) <= placementHealthMaxClasses {
		return
	}
	type item struct {
		key  string
		time time.Time
	}
	items := make([]item, 0, len(state.Classes))
	for key, entry := range state.Classes {
		latest := entry.LastStockout
		if entry.LastPlaced.After(latest) {
			latest = entry.LastPlaced
		}
		items = append(items, item{key: key, time: latest})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].time.Before(items[j].time) })
	for i := 0; i < len(items)-placementHealthMaxClasses; i++ {
		delete(state.Classes, items[i].key)
	}
}
