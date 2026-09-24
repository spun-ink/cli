package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"golang.org/x/mod/semver"
)

// skillState is what the last complete refresh saw: the version whose SKILL.md is in place, and the
// agent homes it looked in. An unchanged invocation reads this one file and stops.
type skillState struct {
	Version string   `json:"version"`
	Homes   []string `json:"homes"`
}

const skillStateFile = "skill-state.json"

// refreshSkills keeps every SKILL.md that `spun setup` wrote at an agent's standard location in step
// with this binary. It never creates a directory, so a removed skill stays removed, and it never
// writes a version older than one already recorded, so an old process still running after an
// upgrade cannot put old instructions back. A failure leaves the state alone, and the next run retries.
func refreshSkills() {
	running, ok := canonicalVersion(version)
	if !ok {
		return // a dev build: `spun setup` writes its skill
	}
	if _, err := configDir(); err != nil {
		return
	}
	now := skillState{Version: running}
	for _, agent := range agents {
		home, err := agentHome(agent)
		if err != nil {
			return
		}
		now.Homes = append(now.Homes, home)
	}
	var seen skillState
	if readJSON(skillStateFile, &seen) != nil {
		seen = skillState{} // unreadable state is no state: refresh and write it anew
	}
	if seen.Version == now.Version && slices.Equal(seen.Homes, now.Homes) {
		return
	}
	if recorded, ok := canonicalVersion(seen.Version); ok && semver.Compare(recorded, running) > 0 {
		return
	}

	updated := false
	for _, agent := range agents {
		path, _ := skillDir(agent)
		changed, err := refreshSkill(path)
		if err != nil {
			return
		}
		updated = updated || changed
	}
	if writeJSON(skillStateFile, now) != nil {
		return
	}
	if updated {
		notify("spun skill updated to match spun %s", version)
	}
}

// refreshSkill replaces SKILL.md in a directory `spun setup` owns — a real directory with a regular
// marker — and reports whether it changed. Anything else is not managed and is left alone; only an
// I/O failure is an error.
func refreshSkill(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}
	marker, err := os.Lstat(filepath.Join(path, managedMarker))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !marker.Mode().IsRegular() {
		return false, nil
	}
	skill := filepath.Join(path, "SKILL.md")
	if current, err := os.ReadFile(skill); err == nil && bytes.Equal(current, []byte(skillText)) {
		return false, nil
	}
	return true, writeFile(skill, []byte(skillText), 0o644)
}
