package google

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/docker/machine/libmachine/log"
)

const (
	placementHealthVersion     = 2
	placementHealthFilename    = "google-flex-stockout-health.json"
	placementHealthLockSuffix  = ".lock"
	placementHealthLockTimeout = time.Second
	placementHealthMaxFileSize = 128 * 1024
	placementHealthMaxAge      = 24 * time.Hour
	placementHealthMaxClasses  = 128
)

type placementClass struct {
	Project        string   `json:"project"`
	Region         string   `json:"region"`
	Zones          []string `json:"zones,omitempty"`
	MachineType    string   `json:"machine_type"`
	DiskType       string   `json:"disk_type,omitempty"`
	DiskIops       int64    `json:"disk_iops,omitempty"`
	DiskThroughput int64    `json:"disk_throughput,omitempty"`
	Accelerator    string   `json:"accelerator,omitempty"`
	MinCPUPlatform string   `json:"min_cpu_platform,omitempty"`
}

type placementClassHealth struct {
	Class         placementClass `json:"class"`
	CooldownUntil time.Time      `json:"cooldown_until,omitempty"`
	ProbeUntil    time.Time      `json:"probe_until,omitempty"`
	ProbeOwner    string         `json:"probe_owner,omitempty"`
	LastStockout  time.Time      `json:"last_stockout,omitempty"`
	LastPlaced    time.Time      `json:"last_placed,omitempty"`
}

type placementHealthFile struct {
	Version int                    `json:"version"`
	Classes []placementClassHealth `json:"classes"`
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
	return &placementHealth{statePath: statePath, lockPath: statePath + placementHealthLockSuffix, available: true, now: now}
}

func (h *placementHealth) selectionClass(d *Driver, selection flexSelection) placementClass {
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
	return placementClass{
		Project: d.Project, Region: d.Region, Zones: zones, MachineType: selection.MachineType,
		DiskType: diskType, DiskIops: diskIops, DiskThroughput: diskThroughput,
		Accelerator: d.Accelerator, MinCPUPlatform: d.MinCPUPlatform,
	}
}

func samePlacementClass(a, b placementClass) bool {
	if a.Project != b.Project || a.Region != b.Region || a.MachineType != b.MachineType ||
		a.DiskType != b.DiskType || a.DiskIops != b.DiskIops || a.DiskThroughput != b.DiskThroughput ||
		a.Accelerator != b.Accelerator || a.MinCPUPlatform != b.MinCPUPlatform || len(a.Zones) != len(b.Zones) {
		return false
	}
	for i := range a.Zones {
		if a.Zones[i] != b.Zones[i] {
			return false
		}
	}
	return true
}

func findPlacementClass(state *placementHealthFile, class placementClass) int {
	for i := range state.Classes {
		if samePlacementClass(state.Classes[i].Class, class) {
			return i
		}
	}
	return -1
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
	healthy := make([]flexSelection, 0, len(configured))
	cooling := make([]flexSelection, 0, len(configured))
	probePosition := -1
	probeClassPosition := -1

	for position, selection := range configured {
		class := h.selectionClass(d, selection)
		index := findPlacementClass(state, class)
		if index < 0 || state.Classes[index].CooldownUntil.IsZero() {
			healthy = append(healthy, selection)
			continue
		}
		entry := state.Classes[index]
		cooling = append(cooling, selection)
		if d.FlexStockoutProbeLease > 0 && !now.Before(entry.CooldownUntil) && !now.Before(entry.ProbeUntil) &&
			(probePosition < 0 || entry.CooldownUntil.Before(state.Classes[probeClassPosition].CooldownUntil)) {
			probePosition = position
			probeClassPosition = index
		}
	}

	if len(healthy) == 0 {
		// The lease limits priority recovery probes while another class is
		// healthy. When every class is cooling, preserve one bounded exhaustive
		// pass so capacity discovery cannot stop completely.
		return append([]flexSelection(nil), configured...)
	}

	if probePosition >= 0 {
		entry := state.Classes[probeClassPosition]
		entry.ProbeUntil = now.Add(d.FlexStockoutProbeLease)
		entry.ProbeOwner = d.MachineName
		state.Classes[probeClassPosition] = entry
		if err := h.save(state); err != nil {
			log.Warnf("Could not claim bulkInsert placement probe; using configured order: %v", err)
			return append([]flexSelection(nil), configured...)
		}
		// The priority probe must be attempted. Returning configured order can strand a
		// lower-ranked probe behind an earlier healthy selection that succeeds.
		ordered := []flexSelection{configured[probePosition]}
		for _, selection := range healthy {
			ordered = append(ordered, selection)
		}
		for _, selection := range cooling {
			if !samePlacementClass(h.selectionClass(d, selection), state.Classes[probeClassPosition].Class) {
				ordered = append(ordered, selection)
			}
		}
		return ordered
	}

	for _, selection := range cooling {
		log.Infof("Trying cooling bulkInsert flex selection machine-type=%q after non-cooling selections", selection.MachineType)
	}
	return append(healthy, cooling...)
}

func (h *placementHealth) recordStockout(d *Driver, selection flexSelection) error {
	return h.recordStockoutAt(d, selection, h.now())
}

func (h *placementHealth) recordStockoutAt(d *Driver, selection flexSelection, observedAt time.Time) error {
	if d.FlexStockoutCooldown <= 0 || !h.available {
		return nil
	}
	return h.update(func(state *placementHealthFile, now time.Time) bool {
		class := h.selectionClass(d, selection)
		index := findPlacementClass(state, class)
		entry := placementClassHealth{Class: class}
		if index >= 0 {
			entry = state.Classes[index]
			if observedAt.Before(placementHealthLatestObservation(entry)) {
				return false
			}
		}
		entry.CooldownUntil = observedAt.Add(d.FlexStockoutCooldown)
		entry.ProbeUntil = time.Time{}
		entry.ProbeOwner = ""
		entry.LastStockout = observedAt
		if index >= 0 {
			state.Classes[index] = entry
		} else {
			state.Classes = append(state.Classes, entry)
		}
		return true
	})
}

func (h *placementHealth) recordPlacement(d *Driver, selection flexSelection) error {
	return h.recordPlacementAt(d, selection, h.now())
}

func (h *placementHealth) recordPlacementAt(d *Driver, selection flexSelection, observedAt time.Time) error {
	if d.FlexStockoutCooldown <= 0 || !h.available {
		return nil
	}
	return h.update(func(state *placementHealthFile, now time.Time) bool {
		index := findPlacementClass(state, h.selectionClass(d, selection))
		if index < 0 {
			return false
		}
		entry := state.Classes[index]
		if observedAt.Before(placementHealthLatestObservation(entry)) {
			return false
		}
		if entry.CooldownUntil.IsZero() && entry.ProbeUntil.IsZero() {
			return false
		}
		entry.CooldownUntil = time.Time{}
		entry.ProbeUntil = time.Time{}
		entry.ProbeOwner = ""
		entry.LastPlaced = observedAt
		state.Classes[index] = entry
		return true
	})
}

func (h *placementHealth) releaseProbe(d *Driver, selection flexSelection) error {
	if d.FlexStockoutCooldown <= 0 || !h.available {
		return nil
	}
	return h.update(func(state *placementHealthFile, _ time.Time) bool {
		index := findPlacementClass(state, h.selectionClass(d, selection))
		if index < 0 || state.Classes[index].ProbeOwner != d.MachineName {
			return false
		}
		entry := state.Classes[index]
		entry.ProbeUntil = time.Time{}
		entry.ProbeOwner = ""
		state.Classes[index] = entry
		return true
	})
}

func placementHealthLatestObservation(entry placementClassHealth) time.Time {
	if entry.LastPlaced.After(entry.LastStockout) {
		return entry.LastPlaced
	}
	return entry.LastStockout
}

func (h *placementHealth) update(change func(*placementHealthFile, time.Time) bool) error {
	unlock, err := acquirePlacementHealthLock(h.lockPath, placementHealthLockTimeout)
	if err != nil {
		return err
	}
	defer unlock()
	state, err := h.load()
	if err != nil {
		var versionErr *placementHealthVersionError
		if errors.As(err, &versionErr) {
			if versionErr.version != 1 {
				return err
			}
			log.Warn("Rebuilding legacy bulkInsert placement health state")
			state = newPlacementHealthFile()
		} else {
			var corruptErr *placementHealthCorruptError
			if !errors.As(err, &corruptErr) {
				return err
			}
			log.Warnf("Rebuilding invalid bulkInsert placement health state: %v", err)
			state = newPlacementHealthFile()
		}
	}
	now := h.now()
	changed := change(state, now)
	changed = h.prune(state, now) || changed
	if !changed {
		return nil
	}
	return h.save(state)
}

func newPlacementHealthFile() *placementHealthFile {
	return &placementHealthFile{Version: placementHealthVersion, Classes: []placementClassHealth{}}
}

type placementHealthVersionError struct{ version int }

func (e *placementHealthVersionError) Error() string {
	return fmt.Sprintf("unsupported placement health version %d", e.version)
}

type placementHealthCorruptError struct{ err error }

func (e *placementHealthCorruptError) Error() string { return e.err.Error() }
func (e *placementHealthCorruptError) Unwrap() error { return e.err }

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
	var header struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, &placementHealthCorruptError{err: err}
	}
	if header.Version == 0 {
		return nil, &placementHealthCorruptError{err: errors.New("placement health version is missing")}
	}
	if header.Version != placementHealthVersion {
		return nil, &placementHealthVersionError{version: header.Version}
	}
	var state placementHealthFile
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, &placementHealthCorruptError{err: err}
	}
	return &state, nil
}

func (h *placementHealth) save(state *placementHealthFile) error {
	if err := os.MkdirAll(filepath.Dir(h.statePath), 0700); err != nil {
		return err
	}
	h.cleanupTempFiles()
	data, err := json.MarshalIndent(state, "", "  ")
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

func (h *placementHealth) prune(state *placementHealthFile, now time.Time) bool {
	kept := state.Classes[:0]
	for _, entry := range state.Classes {
		latest := placementHealthLastActivity(entry)
		active := now.Before(entry.CooldownUntil) || now.Before(entry.ProbeUntil)
		if active || (!latest.IsZero() && now.Sub(latest) <= placementHealthMaxAge) {
			kept = append(kept, entry)
		}
	}
	changed := len(kept) != len(state.Classes)
	if len(kept) > placementHealthMaxClasses {
		// The state file is an optimization, not an authority. Keep a hard bound
		// so continuously changing configurations cannot disable the feature by
		// exceeding its own read limit. The most recently active entries win.
		sort.SliceStable(kept, func(i, j int) bool {
			return placementHealthLastActivity(kept[i]).After(placementHealthLastActivity(kept[j]))
		})
		kept = kept[:placementHealthMaxClasses]
		changed = true
	}
	state.Classes = kept
	return changed
}

func placementHealthLastActivity(entry placementClassHealth) time.Time {
	latest := entry.LastStockout
	if entry.LastPlaced.After(latest) {
		latest = entry.LastPlaced
	}
	if entry.ProbeUntil.After(latest) {
		latest = entry.ProbeUntil
	}
	return latest
}

func (h *placementHealth) cleanupTempFiles() {
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(h.statePath), placementHealthFilename+".tmp-*"))
	cutoff := h.now().Add(-placementHealthMaxAge)
	for _, match := range matches {
		if info, err := os.Stat(match); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(match)
		}
	}
}
