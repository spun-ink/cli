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

const signUp = "y\n" + testCode + "\n" // the owner says yes to the terms, then types the code

func TestSignupStoresTheTokenAfterTheCodeAndTheNextCommandReachesIt(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	defaultServer = server.URL

	value, f := runTerminal(t, signUp, "signup", "--email", "owner@example.com", "--no-setup")
	if f != nil || signUpCalls != 2 {
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

func TestSignupSendsTheTermsStampItWasShownAndNeverAToken(t *testing.T) {
	server := newCodeSignup(t)
	if _, f := runTerminal(t, signUp, "signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com", "--no-setup"); f != nil {
		t.Fatal(f)
	}
	if fake.gets != 1 || fake.calls[0]["terms_version"] != "2026-09-01" || fake.calls[0]["terms_content_hash"] != "abc123" {
		t.Fatalf("gets %d, calls %v", fake.gets, fake.calls)
	}
	if fake.authorized {
		t.Fatal("a sign-up request carried an Authorization header")
	}
}

func TestSignupWithoutATerminalIsTheOwnersAndSendsNothing(t *testing.T) {
	server := newCodeSignup(t)
	for _, argv := range [][]string{
		{"signup", "--profile", "dev", "--url", server.URL},
		{"signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com"},
		// what an agent following an older skill runs
		{"signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com", "--accept-terms"},
		{"signup", "--profile", "dev", "--code", testCode},
	} {
		_, f := run(t, "", argv...)
		if exitCode(f) != exitUsage || errorCode(f) != "owner_only" || !strings.Contains(errorMessage(f), "own terminal") ||
			!strings.Contains(errorMessage(f), "Never ask them for that code") {
			t.Errorf("%v: got %v", argv, f)
		}
	}
	if fake.gets != 0 || signUpCalls != 0 {
		t.Fatalf("a refused sign-up reached the server: %d gets, %d calls", fake.gets, signUpCalls)
	}
	if value, _ := run(t, "", "profiles"); len(value.([]any)) != 0 {
		t.Fatalf("stored: %v", value)
	}
}

func TestSignupAtATerminalRefusesTheRetiredAgentFlags(t *testing.T) {
	server := newCodeSignup(t)
	for _, flag := range [][]string{{"--accept-terms"}, {"--code", testCode}} {
		argv := append([]string{"signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com"}, flag...)
		if _, f := runTerminal(t, signUp, argv...); errorCode(f) != "owner_only" {
			t.Errorf("%v: got %v", flag, f)
		}
	}
	if fake.gets != 0 || signUpCalls != 0 {
		t.Fatalf("a refused sign-up reached the server: %d gets, %d calls", fake.gets, signUpCalls)
	}
}

func TestSignupAgainstAServerWithoutTheRouteSaysSoAndSendsNothing(t *testing.T) {
	server := newCodeSignup(t)
	fake.noRoute = true
	_, f := runTerminal(t, "", "signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com")
	if exitCode(f) != exitNetwork || errorCode(f) != "signup_unavailable" || signUpCalls != 0 ||
		!strings.Contains(errorMessage(f), server.URL+"/signup") || !strings.Contains(errorMessage(f), "spun login") {
		t.Fatalf("got %v after %d calls", f, signUpCalls)
	}
	if _, held := pendingFor(t, "dev"); held {
		t.Fatal("a server without the route left a pending sign-up")
	}
}

func TestSignupNeverShowsTheToken(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	var out bytes.Buffer
	a := &app{stdin: stdinWith(t, signUp), stdout: &out, bobbin: true, tty: true}
	if _, err := a.run([]string{"signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com", "--no-setup"}); err != nil {
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
	_, f := runTerminal(t, signUp, "signup", "--email", "owner@example.com")
	if exitCode(f) != exitConfig || signUpCalls != 0 || !strings.Contains(errorMessage(f), "spun logout") ||
		!strings.Contains(errorMessage(f), "--profile") {
		t.Fatalf("got %v after %d calls", f, signUpCalls)
	}
}

func TestSignupSurfacesAValidationFailure(t *testing.T) {
	server := newCodeSignup(t)
	_, f := runTerminal(t, "y\n", "signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com", "--handle", "taken")
	if exitCode(f) != exitToolError || errorCode(f) != "validation_failed" || !strings.Contains(errorMessage(f), "already been taken") ||
		!strings.Contains(errorMessage(f), "No code was sent") || strings.Contains(errorMessage(f), "sign_up") {
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
		{"signup", "--url", server.URL, "--email", "o@example.com"},
		{"signup", "--profile", defaultProfile, "--url", server.URL, "--email", "o@example.com"},
	} {
		if _, f := runTerminal(t, signUp, argv...); exitCode(f) != exitUsage {
			t.Errorf("%v: got %v", argv, f)
		}
	}
	value, f := runTerminal(t, signUp, "signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com", "--no-setup")
	if f != nil || value.(map[string]any)["profile"] != "dev" {
		t.Fatalf("got %v, %v", value, f)
	}
}

func TestATokenlessMcpRequestIsRefused(t *testing.T) {
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

func TestSignupAtATerminalEndsTheSignUpAfterFiveWrongCodes(t *testing.T) {
	server := newCodeSignup(t)
	_, f := runTerminal(t, "y\n111111\n222222\n333333\n444444\n555555\n", "signup", "--profile", "dev", "--url", server.URL,
		"--email", "o@example.com", "--no-setup")
	if exitCode(f) != exitUsage || signUpCalls != 6 {
		t.Fatalf("got %v after %d calls", f, signUpCalls)
	}
	if !strings.Contains(errorMessage(f), "five wrong codes end the sign-up") || strings.Contains(errorMessage(f), "--code") {
		t.Fatalf("message: %s", errorMessage(f))
	}
	if _, held := pendingFor(t, "dev"); held {
		t.Fatal("a sign-up the server ended is still pending")
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

// startSignup sends the code for profile dev and stops at the code prompt, the way an owner who
// closes the terminal there leaves it.
func startSignup(t *testing.T, server *httptest.Server, input string) {
	t.Helper()
	if _, f := runTerminal(t, "y\n"+input, "signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com"); errorCode(f) != "usage" {
		t.Fatalf("got %v", f)
	}
	if _, held := pendingFor(t, "dev"); !held {
		t.Fatal("the sign-up is not pending")
	}
}

func TestSignupAtATerminalContinuesAnOpenSignUpWithoutANewCode(t *testing.T) {
	server := newCodeSignup(t)
	startSignup(t, server, "000000\n") // a wrong code, then the terminal closes

	// No email and no --url: the open sign-up names its server.
	value, f := runTerminal(t, testCode+"\n", "signup", "--profile", "dev", "--no-setup")
	if f != nil || value.(map[string]any)["email"] != "o@example.com" {
		t.Fatalf("got %v, %v", value, f)
	}
	if fake.gets != 1 || signUpCalls != 3 || fake.calls[2]["signup"] != testSignup || fake.calls[2]["code"] != testCode {
		t.Fatalf("the continuation sent another code: %d gets, calls %v", fake.gets, fake.calls)
	}
	if _, held := pendingFor(t, "dev"); held {
		t.Fatal("pending survived the success")
	}
	if token, _ := keyring.Get(keyringService, credentialKey(server.URL, "dev")); token != "good" {
		t.Fatalf("keyring holds %q", token)
	}
}

func TestSignupAtATerminalStartsOverOnEnter(t *testing.T) {
	server := newCodeSignup(t)
	startSignup(t, server, "")

	_, f := runTerminal(t, "\n"+signUp, "signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com", "--no-setup")
	if f != nil || fake.gets != 2 || signUpCalls != 3 || fake.calls[1]["email"] != "o@example.com" {
		t.Fatalf("got %v; %d gets, calls %v", f, fake.gets, fake.calls)
	}
}

func TestSignupStartsOverWhenTheOpenSignUpIsForAnotherServerOrProfile(t *testing.T) {
	server := newCodeSignup(t)
	other := fakeServer(t)
	startSignup(t, server, "")

	for _, argv := range [][]string{
		{"signup", "--profile", "staging", "--url", server.URL, "--email", "s@example.com", "--no-setup"},
		{"signup", "--profile", "dev", "--url", other.URL, "--email", "x@example.com", "--no-setup"},
	} {
		calls := len(fake.calls)
		if _, f := runTerminal(t, signUp, argv...); f != nil || fake.calls[calls]["email"] != argv[6] {
			t.Fatalf("%v: got %v, calls %v", argv, f, fake.calls)
		}
	}
}

func TestSignupAfterTheOpenSignUpExpiredStartsOver(t *testing.T) {
	server := newCodeSignup(t)
	if err := savePending("dev", pendingSignup{Signup: testSignup, URL: server.URL, Email: "o@example.com",
		Expires: time.Now().Add(-time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	if _, f := runTerminal(t, signUp, "signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com", "--no-setup"); f != nil ||
		signUpCalls != 2 || fake.calls[0]["email"] != "o@example.com" {
		t.Fatalf("got %v, calls %v", f, fake.calls)
	}
}

func TestSignupClearsThePendingStateOnARefusalNothingCanContinueFrom(t *testing.T) {
	for code, want := range map[string]string{"exists": "account_exists", "done": "already_completed", "changed": "terms_changed"} {
		t.Run(want, func(t *testing.T) {
			server := newCodeSignup(t)
			_, f := runTerminal(t, "y\n"+code+"\n", "signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com")
			if errorCode(f) != want || strings.Contains(errorMessage(f), "sign_up") {
				t.Fatalf("got %v", f)
			}
			if want == "terms_changed" && (!strings.Contains(errorMessage(f), testNotice) || !strings.Contains(errorMessage(f), "spun signup --profile dev")) {
				t.Fatalf("message: %s", errorMessage(f))
			}
			if _, held := pendingFor(t, "dev"); held {
				t.Fatal("the refusal left the pending sign-up behind")
			}
		})
	}
}

func TestSignupBusyAnswersSayToRepeatAndKeepThePendingState(t *testing.T) {
	for name, arm := range map[string]struct {
		set  func()
		code string
	}{
		"rate limit":  {func() { fake.rateLimits = 1 }, "rate_limited"},
		"unavailable": {func() { fake.unavailable = 1 }, "unavailable"},
		"bare 429":    {func() { fake.plain429 = 1 }, "rate_limited"},
	} {
		t.Run(name, func(t *testing.T) {
			server := newCodeSignup(t)
			startSignup(t, server, "")

			arm.set()
			_, f := runTerminal(t, testCode+"\n", "signup", "--profile", "dev")
			if errorCode(f) != arm.code || exitCode(f) != exitNetwork || !strings.Contains(errorMessage(f), "no new code") ||
				!strings.Contains(errorMessage(f), "repeat `spun signup --profile dev`") {
				t.Fatalf("got %v", f)
			}
			if _, held := pendingFor(t, "dev"); !held {
				t.Fatal("a busy answer dropped the pending sign-up")
			}
			if _, f := runTerminal(t, testCode+"\n", "signup", "--profile", "dev", "--no-setup"); f != nil {
				t.Fatalf("the repeat failed: %v", f)
			}
		})
	}
}

func TestSignupFirstCallBusyAnswerSaysToRepeatTheSameCommandAndStoresNothing(t *testing.T) {
	server := newCodeSignup(t)
	fake.rateLimits = 1
	_, f := runTerminal(t, "y\n", "signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com")
	if errorCode(f) != "rate_limited" || !strings.Contains(errorMessage(f), "spun signup --profile dev with the same flags") {
		t.Fatalf("got %v", f)
	}
	if _, held := pendingFor(t, "dev"); held {
		t.Fatal("a refused first call left a pending sign-up")
	}
}
