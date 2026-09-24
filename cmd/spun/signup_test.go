package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestSignupStoresTheSpunInkProfileAndTheNextCommandReachesIt(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	defaultServer = server.URL
	signUpCalls = 0

	value, f := run(t, "", "signup", "--email", "owner@example.com", "--accept-terms", "--no-setup")
	if f != nil || signUpCalls != 1 {
		t.Fatalf("got %v, %v after %d calls", value, f, signUpCalls)
	}
	reply := value.(map[string]any)
	if reply["profile"] != defaultProfile || reply["token_stored_in"] != "keyring" ||
		reply["terms_version"] != "2026-09-01" || reply["terms_content_hash"] != "abc123" {
		t.Fatalf("got %v", reply)
	}
	if out := render(value, false); strings.Contains(out, "good") || !strings.Contains(out, "confirm the email we sent to owner@example.com") {
		t.Fatalf("reply: %s", out)
	}
	if token, _ := keyring.Get(keyringService, credentialKey(server.URL, defaultProfile)); token != "good" {
		t.Fatalf("keyring holds %q", token)
	}
	if _, f := run(t, "", "call", "get_site"); f != nil {
		t.Fatalf("the next unnamed command did not reach the new profile: %v", f)
	}
}

func TestSignupNeverShowsTheToken(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	var out bytes.Buffer
	a := &app{stdin: stdinWith(t, ""), stdout: &out, bobbin: true}
	if _, err := a.run([]string{"signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com", "--accept-terms", "--no-setup"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "good") || !strings.Contains(out.String(), "Signed up.") {
		t.Fatalf("view: %s", out.String())
	}
}

func TestSignupRefusesOverAnExistingProfile(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	defaultServer = server.URL
	if _, f := run(t, "good\n", "login", "--no-setup"); f != nil {
		t.Fatal(f)
	}
	signUpCalls = 0
	_, f := run(t, "", "signup", "--email", "owner@example.com", "--accept-terms")
	if exitCode(f) != exitConfig || signUpCalls != 0 || !strings.Contains(errorMessage(f), "spun logout") ||
		!strings.Contains(errorMessage(f), "--profile") {
		t.Fatalf("got %v after %d calls", f, signUpCalls)
	}
}

func TestSignupWithoutATerminalNeedsTheTermsAccepted(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	signUpCalls = 0
	_, f := run(t, "", "signup", "--profile", "dev", "--url", server.URL, "--email", "owner@example.com")
	if exitCode(f) != exitUsage || signUpCalls != 0 || !strings.Contains(errorMessage(f), testNotice) ||
		!strings.Contains(errorMessage(f), "--accept-terms") {
		t.Fatalf("got %v after %d calls", f, signUpCalls)
	}
	if value, _ := run(t, "", "profiles"); len(value.([]any)) != 0 {
		t.Fatalf("stored: %v", value)
	}
}

func TestSignupWithoutATerminalNeedsTheEmail(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	if _, f := run(t, "", "signup", "--profile", "dev", "--url", server.URL, "--accept-terms"); exitCode(f) != exitUsage ||
		!strings.Contains(errorMessage(f), "--email") {
		t.Fatalf("got %v", f)
	}
}

func TestSignupSurfacesAValidationFailure(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	_, f := run(t, "", "signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com", "--handle", "taken", "--accept-terms")
	if exitCode(f) != exitToolError || errorCode(f) != "validation_failed" || !strings.Contains(errorMessage(f), "already been taken") {
		t.Fatalf("got %v", f)
	}
	if value, _ := run(t, "", "profiles"); len(value.([]any)) != 0 {
		t.Fatalf("stored: %v", value)
	}
}

func TestSignupKeepsADevServerOutOfTheDefaultProfile(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	for _, argv := range [][]string{
		{"signup", "--url", server.URL, "--email", "o@example.com", "--accept-terms"},
		{"signup", "--profile", defaultProfile, "--url", server.URL, "--email", "o@example.com", "--accept-terms"},
	} {
		if _, f := run(t, "", argv...); exitCode(f) != exitUsage {
			t.Errorf("%v: got %v", argv, f)
		}
	}
	value, f := run(t, "", "signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com", "--accept-terms", "--no-setup")
	if f != nil || value.(map[string]any)["profile"] != "dev" {
		t.Fatalf("got %v, %v", value, f)
	}
}

func TestOnlySignupGoesOutWithoutAToken(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	t.Setenv("SPUN_URL", server.URL)
	if _, f := run(t, "", "call", "sign_up", "email=o@example.com"); exitCode(f) != exitUnauthorized {
		t.Fatalf("got %v", f)
	}
}
