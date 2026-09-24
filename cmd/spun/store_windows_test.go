//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestWindowsNeverFallsBackToAFile(t *testing.T) {
	home := isolate(t)
	keyring.MockInitWithError(errors.New("credential manager unavailable"))
	server := fakeServer(t)
	_, f := run(t, "good\n", "login", "--token", "--profile", "dev", "--url", server.URL)
	if exitCode(f) != exitConfig || errorCode(f) != "keyring_unavailable" {
		t.Fatalf("got %v", f)
	}
	if _, err := os.Stat(filepath.Join(home, "config", "spun", "credentials.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the token was written to a file: %v", err)
	}
}
