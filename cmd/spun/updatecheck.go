package main

import (
	"context"
	"os"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// updateCheck is the cache that keeps the check to one request a day: when it last asked, and the
// latest release it found then.
type updateCheck struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest,omitempty"`
}

const (
	updateCheckFile     = "update-check.json"
	updateCheckInterval = 24 * time.Hour
)

var updateCheckTimeout = 1500 * time.Millisecond

// checkForUpdate tells a person at a terminal that a newer release exists. It only ever checks:
// installing is `spun upgrade`. Agents and scripts pipe the output and would never see the notice,
// so they skip the request too and ask `spun upgrade --check` when they want to know.
func checkForUpdate() {
	if buildSource != "release" || os.Getenv("CI") != "" || os.Getenv("SPUN_NO_UPDATE_CHECK") != "" || !interactive() {
		return
	}
	running, ok := canonicalVersion(version)
	if !ok {
		return
	}
	if _, err := configDir(); err != nil {
		return
	}
	var cache updateCheck
	if readJSON(updateCheckFile, &cache) != nil {
		cache = updateCheck{}
	}
	if age := time.Since(cache.CheckedAt); age < 0 || age >= updateCheckInterval {
		ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
		latest, err := latestRelease(ctx)
		cancel()
		cache.CheckedAt = time.Now()
		if err == nil {
			cache.Latest = latest
		}
		_ = writeJSON(updateCheckFile, cache)
	}
	if latest, ok := canonicalVersion(cache.Latest); ok && semver.Compare(latest, running) > 0 {
		notify("spun %s is available — run spun upgrade", strings.TrimPrefix(latest, "v"))
	}
}
