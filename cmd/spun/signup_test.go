package main

import (
	"bytes"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func TestSignupAgainstAServerBeforeTheCodeSwitchStoresTheTokenFromOneCallAndTheNextCommandReachesIt(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	fake.legacy = true
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
	if out := render(value, false); strings.Contains(out, "good") || strings.Contains(out, "confirm the email") {
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
	fake.legacy = true
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
	fake.legacy = true
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

// newCodeSignup starts fakeServer as a server after the switch and returns it.
func newCodeSignup(t *testing.T) *httptest.Server {
	t.Helper()
	isolate(t)
	return fakeServer(t)
}

// runTerminal runs one command with stdin as a terminal answering with input.
func runTerminal(t *testing.T, input string, argv ...string) (any, *Fail) {
	t.Helper()
	a := &app{stdin: stdinWith(t, input), stdout: io.Discard, tty: true}
	value, err := a.run(argv)
	if err != nil {
		return nil, asFail(err)
	}
	return value, nil
}

func pendingFor(t *testing.T, profile string) (pendingSignup, bool) {
	t.Helper()
	all, err := readPending()
	if err != nil {
		t.Fatal(err)
	}
	p, ok := all[profile]
	return p, ok
}

func TestSignupAtATerminalAsksTheCodeAndAcceptsItSpaced(t *testing.T) {
	server := newCodeSignup(t)
	value, f := runTerminal(t, "y\n482 913\n", "signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com", "--no-setup")
	if f != nil || signUpCalls != 2 {
		t.Fatalf("got %v, %v after %d calls", value, f, signUpCalls)
	}
	if fake.calls[0]["email"] != "o@example.com" || fake.calls[1]["signup"] != testSignup || fake.calls[1]["code"] != testCode {
		t.Fatalf("calls: %v", fake.calls)
	}
	reply := value.(map[string]any)
	if reply["ok"] != true || reply["email"] != "o@example.com" || strings.Contains(render(value, false), "good") {
		t.Fatalf("reply: %v", reply)
	}
	if token, _ := keyring.Get(keyringService, credentialKey(server.URL, "dev")); token != "good" {
		t.Fatalf("keyring holds %q", token)
	}
	if _, held := pendingFor(t, "dev"); held {
		t.Fatal("the pending sign-up outlived its success")
	}
}

func TestSignupAtATerminalDeclinedSendsNothing(t *testing.T) {
	server := newCodeSignup(t)
	_, f := runTerminal(t, "n\n", "signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com")
	if exitCode(f) != exitUsage || signUpCalls != 0 {
		t.Fatalf("got %v after %d calls", f, signUpCalls)
	}
}

func TestSignupAtATerminalLetsTheHumanRetryAWrongCode(t *testing.T) {
	server := newCodeSignup(t)
	_, f := runTerminal(t, "y\n111111\n482913\n", "signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com", "--no-setup")
	if f != nil || signUpCalls != 3 {
		t.Fatalf("got %v after %d calls", f, signUpCalls)
	}
	if fake.calls[1]["code"] != "111111" || fake.calls[2]["code"] != testCode {
		t.Fatalf("calls: %v", fake.calls)
	}
}

func TestSignupAtATerminalCorrectsARefusedHandleWithoutANewCode(t *testing.T) {
	server := newCodeSignup(t)
	_, f := runTerminal(t, "y\n482913\n\nfresh\n", "signup", "--profile", "dev", "--url", server.URL,
		"--email", "o@example.com", "--handle", "late", "--no-setup")
	if f != nil || signUpCalls != 3 {
		t.Fatalf("got %v after %d calls", f, signUpCalls)
	}
	last := fake.calls[2]
	if _, hasCode := last["code"]; hasCode || last["handle"] != "fresh" || last["signup"] != testSignup {
		t.Fatalf("the correction should carry no code: %v", last)
	}
}

func TestSignupWithoutATerminalStopsAtCodeSentWithItsOwnStatusAndContinuesWithCode(t *testing.T) {
	server := newCodeSignup(t)
	a := &app{stdin: stdinWith(t, ""), stdout: io.Discard}
	value, err := a.run([]string{"signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com", "--accept-terms", "--no-setup"})
	if err != nil {
		t.Fatal(err)
	}
	reply := value.(map[string]any)
	if a.exit != exitCodeSent || reply["status"] != "code_sent" || reply["email"] != "o@example.com" ||
		!strings.Contains(reply["next"].(string), "spun signup --code <code> --profile dev") {
		t.Fatalf("exit %d, reply %v", a.exit, reply)
	}
	if profiles, _ := run(t, "", "profiles"); len(profiles.([]any)) != 0 {
		t.Fatalf("a token was stored before the code: %v", profiles)
	}
	if p, held := pendingFor(t, "dev"); !held || p.Signup != testSignup || !sameServer(p.URL, server.URL) || p.Email != "o@example.com" {
		t.Fatalf("pending: %v %v", p, held)
	}

	// The continuation needs no email and no --url: the pending state names the server.
	value, f := run(t, "", "signup", "--profile", "dev", "--code", "482913", "--no-setup")
	if f != nil || value.(map[string]any)["email"] != "o@example.com" {
		t.Fatalf("got %v, %v", value, f)
	}
	if _, held := pendingFor(t, "dev"); held {
		t.Fatal("pending survived the success")
	}
	if token, _ := keyring.Get(keyringService, credentialKey(server.URL, "dev")); token != "good" {
		t.Fatalf("keyring holds %q", token)
	}
}

func TestSignupCodeKeepsThePendingStateOnAWrongCodeAndACorrectableRefusal(t *testing.T) {
	server := newCodeSignup(t)
	base := []string{"signup", "--profile", "dev", "--url", server.URL}
	run(t, "", append(base, "--email", "o@example.com", "--accept-terms")...)

	if _, f := run(t, "", append(base, "--code", "000000")...); exitCode(f) != exitToolError || errorCode(f) != "invalid_code" {
		t.Fatalf("got %v", f)
	}
	if _, f := run(t, "", append(base, "--code", testCode, "--handle", "late")...); errorCode(f) != "validation_failed" {
		t.Fatalf("got %v", f)
	}
	if _, held := pendingFor(t, "dev"); !held {
		t.Fatal("a correctable refusal dropped the pending sign-up")
	}
	if _, f := run(t, "", append(base, "--code", testCode, "--handle", "fresh", "--no-setup")...); f != nil {
		t.Fatalf("the correction failed: %v", f)
	}
	if _, held := pendingFor(t, "dev"); held {
		t.Fatal("pending survived the success")
	}
}

func TestSignupCodeClearsThePendingStateOnATerminalRefusal(t *testing.T) {
	server := newCodeSignup(t)
	base := []string{"signup", "--profile", "dev", "--url", server.URL}
	run(t, "", append(base, "--email", "o@example.com", "--accept-terms")...)
	if _, f := run(t, "", append(base, "--code", "exists")...); errorCode(f) != "account_exists" {
		t.Fatalf("got %v", f)
	}
	if _, held := pendingFor(t, "dev"); held {
		t.Fatal("a refusal nothing can continue from left the pending sign-up behind")
	}
}

func TestSignupCodeRateLimitSaysToRepeatTheSameCall(t *testing.T) {
	server := newCodeSignup(t)
	base := []string{"signup", "--profile", "dev", "--url", server.URL}
	run(t, "", append(base, "--email", "o@example.com", "--accept-terms")...)

	fake.rateLimits = 1
	_, f := run(t, "", append(base, "--code", testCode)...)
	if errorCode(f) != "rate_limited" || !strings.Contains(errorMessage(f), "no new code") ||
		!strings.Contains(errorMessage(f), "spun signup --code <code> --profile dev") {
		t.Fatalf("got %v", f)
	}
	if _, held := pendingFor(t, "dev"); !held {
		t.Fatal("a rate limit dropped the pending sign-up")
	}
	if _, f := run(t, "", append(base, "--code", testCode, "--no-setup")...); f != nil {
		t.Fatalf("the repeat failed: %v", f)
	}
}

func TestSignupPendingStateIsBoundToItsServerAndProfile(t *testing.T) {
	server := newCodeSignup(t)
	other := fakeServer(t)
	run(t, "", "signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com", "--accept-terms")

	if _, f := run(t, "", "signup", "--profile", "dev", "--url", other.URL, "--code", testCode); exitCode(f) != exitUsage ||
		!strings.Contains(errorMessage(f), server.URL) {
		t.Fatalf("another server reached the pending sign-up: %v", f)
	}
	for _, argv := range [][]string{
		{"signup", "--code", testCode},
		{"signup", "--profile", "staging", "--url", server.URL, "--code", testCode},
	} {
		if _, f := run(t, "", argv...); exitCode(f) != exitUsage || !strings.Contains(errorMessage(f), "no sign-up is waiting") {
			t.Errorf("%v: got %v", argv, f)
		}
	}
	if _, f := run(t, "", "signup", "--profile", "dev", "--code", testCode, "--email", "x@example.com"); exitCode(f) != exitUsage {
		t.Errorf("--code with --email: got %v", f)
	}
	if signUpCalls != 1 {
		t.Fatalf("a refused continuation still sent a call: %d", signUpCalls)
	}
}

func TestSignupCodeAfterTheSignUpExpired(t *testing.T) {
	server := newCodeSignup(t)
	if err := savePending("dev", pendingSignup{Signup: testSignup, URL: server.URL, Email: "o@example.com",
		Expires: time.Now().Add(-time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	if _, f := run(t, "", "signup", "--profile", "dev", "--code", testCode); exitCode(f) != exitUsage ||
		!strings.Contains(errorMessage(f), "expired") || signUpCalls != 0 {
		t.Fatalf("got %v", f)
	}
	if _, held := pendingFor(t, "dev"); held {
		t.Fatal("an expired sign-up stayed")
	}
}
