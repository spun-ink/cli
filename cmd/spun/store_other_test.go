//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestLoginFallsBackToAnOwnerOnlyFile(t *testing.T) {
	home := isolate(t)
	keyring.MockInitWithError(errors.New("no secret service"))
	server := fakeServer(t)
	value, f := run(t, "good\n", "login", "--token", "--profile", "dev", "--url", server.URL)
	if f != nil || value.(map[string]any)["token_stored_in"] != "file" {
		t.Fatalf("got %v, %v", value, f)
	}
	path := filepath.Join(home, "config", "spun", "credentials.json")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials.json: %v %v", info, err)
	}
	if _, f := run(t, "", "--profile", "dev", "call", "get_site"); f != nil {
		t.Fatalf("file token not used: %v", f)
	}
}

func TestAFileTokenIsNeverShadowedByAStaleKeyringOne(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	keyring.MockInitWithError(errors.New("keychain locked"))
	if _, f := run(t, "good\n", "login", "--token", "--profile", "dev", "--url", server.URL); f != nil {
		t.Fatal(f)
	}
	keyring.MockInit() // the keyring answers again, with an old token for the same name
	_ = keyring.Set(keyringService, credentialKey(server.URL, "dev"), "bad")
	if _, f := run(t, "", "--profile", "dev", "call", "get_site"); f != nil {
		t.Fatalf("the keyring's stale token answered: %v", f)
	}
}

func TestTheToolsCacheIsOwnerOnly(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	t.Setenv("SPUN_URL", server.URL)
	t.Setenv("SPUN_TOKEN", "good")
	run(t, "", "tools")
	path := (&Client{server.URL, "good"}).cachePath()
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cache file: %v %v", info, err)
	}
}
