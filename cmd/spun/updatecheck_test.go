package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// releaseServer answers releases/latest with a redirect to latest's tag, like GitHub, and counts
// the requests it saw.
func releaseServer(t *testing.T, latest string) *atomic.Int32 {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Errorf("a release request carried a credential")
		}
		if r.URL.Path == "/releases/latest" {
			http.Redirect(w, r, "/releases/tag/"+latest, http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	releaseURL = server.URL
	return &hits
}

// atTerminal makes the run look interactive and captures its notices.
func atTerminal(t *testing.T) *strings.Builder {
	t.Helper()
	var out strings.Builder
	previous, previousNotices := interactive, notices
	interactive, notices = func() bool { return true }, &out
	t.Cleanup(func() { interactive, notices = previous, previousNotices })
	return &out
}

func asRelease(t *testing.T, v string) {
	t.Helper()
	asVersion(t, v)
	previous := buildSource
	buildSource = "release"
	t.Cleanup(func() { buildSource = previous })
}

func TestLatestReleaseFollowsTheRedirect(t *testing.T) {
	isolate(t)
	releaseServer(t, "v1.4.0")
	latest, err := latestRelease(t.Context())
	if err != nil || latest != "v1.4.0" {
		t.Fatalf("got %q, %v", latest, err)
	}
}

func TestLatestReleaseRejectsAnythingButAVersionTag(t *testing.T) {
	isolate(t)
	for _, tag := range []string{"nightly", "1.4.0", "v1.4", "v1.4.0+build"} {
		releaseServer(t, tag)
		if latest, err := latestRelease(t.Context()); err == nil {
			t.Fatalf("%s: got %q", tag, latest)
		}
	}
}

func TestUpdateNoticeAtMostOnceADay(t *testing.T) {
	isolate(t)
	asRelease(t, "1.2.0")
	hits := releaseServer(t, "v1.4.0")
	out := atTerminal(t)
	checkForUpdate()
	checkForUpdate()
	if hits.Load() != 1 {
		t.Fatalf("%d requests, want 1", hits.Load())
	}
	if want := "spun 1.4.0 is available — run spun upgrade\n"; out.String() != want+want {
		t.Fatalf("got %q", out.String())
	}
}

func TestUpdateCheckAsksAgainAfterADay(t *testing.T) {
	home := isolate(t)
	asRelease(t, "1.2.0")
	hits := releaseServer(t, "v1.4.0")
	atTerminal(t)
	stale := `{"checked_at":"` + time.Now().Add(-25*time.Hour).Format(time.RFC3339) + `","latest":"v1.3.0"}`
	if err := os.MkdirAll(filepath.Join(home, "config", "spun"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "spun", updateCheckFile), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	checkForUpdate()
	if hits.Load() != 1 {
		t.Fatalf("%d requests, want 1", hits.Load())
	}
}

func TestNoNoticeWhenUpToDate(t *testing.T) {
	isolate(t)
	asRelease(t, "1.4.0")
	releaseServer(t, "v1.4.0")
	out := atTerminal(t)
	checkForUpdate()
	if out.Len() != 0 {
		t.Fatalf("got %q", out.String())
	}
}

func TestUpdateCheckIsOff(t *testing.T) {
	cases := map[string]func(t *testing.T){
		"piped":      func(t *testing.T) { interactive = func() bool { return false } },
		"dev build":  func(t *testing.T) { buildSource = "dev" },
		"go install": func(t *testing.T) { buildSource = "module" },
		"snapshot":   func(t *testing.T) { buildSource = "snapshot" },
		"CI":         func(t *testing.T) { t.Setenv("CI", "true") },
		"opted out":  func(t *testing.T) { t.Setenv("SPUN_NO_UPDATE_CHECK", "1") },
	}
	for name, off := range cases {
		t.Run(name, func(t *testing.T) {
			isolate(t)
			asRelease(t, "1.2.0")
			hits := releaseServer(t, "v1.4.0")
			out := atTerminal(t)
			off(t)
			checkForUpdate()
			if hits.Load() != 0 || out.Len() != 0 {
				t.Fatalf("%d requests, notice %q", hits.Load(), out.String())
			}
		})
	}
}

func TestASlowServerIsGivenUp(t *testing.T) {
	isolate(t)
	asRelease(t, "1.2.0")
	atTerminal(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	releaseURL = server.URL
	previous := updateCheckTimeout
	updateCheckTimeout = 50 * time.Millisecond
	t.Cleanup(func() { updateCheckTimeout = previous })
	start := time.Now()
	checkForUpdate()
	if time.Since(start) > 2*time.Second {
		t.Fatal("the update check waited past its timeout")
	}
}
