package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

const testLogin = "calm-heron-2"

// sessionSteer steers the sign-in routes: noRoute makes a server without them (404), logout names
// the refusal /cli/logout answers with ("" signs out, "broken" is a bare 500). logins records each
// body that reached /cli/login(/complete), logouts the bearer of each sign-out, and authorized any
// sign-in request that carried an Authorization header. fakeServer resets it.
type sessionSteer struct {
	noRoute    bool
	logout     string
	authorized bool
	logins     []map[string]any
	logouts    []string
}

var session sessionSteer

// cliSession answers /cli/login, /cli/login/complete and /cli/logout like the server: the code is
// testCode, the credential "good".
func cliSession(w http.ResponseWriter, r *http.Request) {
	if session.noRoute {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/cli/logout" {
		session.logouts = append(session.logouts, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		switch session.logout {
		case "":
			sendJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "signed_out"})
		case "broken":
			w.WriteHeader(http.StatusInternalServerError)
		case "unauthenticated":
			refuseJSON(w, http.StatusUnauthorized, session.logout, "An unknown one is already signed out.")
		default:
			refuseJSON(w, http.StatusConflict, session.logout, "This is an account token or an operator token.")
		}
		return
	}
	if _, has := r.Header["Authorization"]; has {
		session.authorized = true
	}
	var args map[string]any
	_ = json.NewDecoder(r.Body).Decode(&args)
	session.logins = append(session.logins, args)
	switch {
	case r.URL.Path == "/cli/login":
		sendJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "code_sent", "login": testLogin,
			"email": args["email"], "code_expires_in": 600, "login_expires_in": 900})
	case args["login"] != testLogin || args["code"] != testCode:
		refuseJSON(w, http.StatusUnprocessableEntity, "invalid_code", "The code is wrong or no longer valid.")
	default:
		sendJSON(w, http.StatusCreated, map[string]any{"ok": true, "bearer_token": "good"})
	}
}

func TestLoginAtATerminalSignsInWithTheMailedCode(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	_, f := runTerminal(t, "482 913\n", "login", "--profile", "dev", "--url", server.URL, "--email", "o@example.com", "--no-setup")
	if f != nil || len(session.logins) != 2 {
		t.Fatalf("got %v after %v", f, session.logins)
	}
	if session.logins[0]["email"] != "o@example.com" || session.logins[1]["code"] != testCode ||
		session.logins[1]["machine"] != machineName() {
		t.Fatalf("sent %v", session.logins)
	}
	if session.authorized {
		t.Fatal("a sign-in request carried an Authorization header")
	}
	if token, _ := keyring.Get(keyringService, credentialKey(server.URL, "dev")); token != "good" {
		t.Fatalf("keyring holds %q", token)
	}
}

func TestLoginAtATerminalAsksForTheEmailAndAgainAfterAWrongCode(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	_, f := runTerminal(t, "o@example.com\n111111\n"+testCode+"\n", "login", "--profile", "dev", "--url", server.URL, "--no-setup")
	if f != nil || len(session.logins) != 3 || session.logins[0]["email"] != "o@example.com" {
		t.Fatalf("got %v after %v", f, session.logins)
	}
}

func TestLoginAtATerminalEndsAfterFiveWrongCodes(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	_, f := runTerminal(t, strings.Repeat("111111\n", 5), "login", "--profile", "dev", "--url", server.URL, "--email", "o@example.com")
	if exitCode(f) != exitUsage || !strings.Contains(errorMessage(f), "five wrong codes") ||
		!strings.Contains(errorMessage(f), "spun login --profile dev") {
		t.Fatalf("got %v", f)
	}
	if value, _ := run(t, "", "profiles"); len(value.([]any)) != 0 {
		t.Fatalf("stored: %v", value)
	}
}

func TestLoginWithTokenAtATerminalTakesAPastedToken(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	if _, f := runTerminal(t, "good\n", "login", "--token", "--profile", "dev", "--url", server.URL, "--no-setup"); f != nil {
		t.Fatal(f)
	}
	if len(session.logins) != 0 {
		t.Fatalf("a pasted token went through sign-in by code: %v", session.logins)
	}
}

func TestLoginOnAServerWithoutSignInByCodeNamesTheTokenWay(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	session.noRoute = true
	_, f := runTerminal(t, "", "login", "--profile", "dev", "--url", server.URL, "--email", "o@example.com")
	if exitCode(f) != exitNetwork || errorCode(f) != "login_unavailable" || !strings.Contains(errorMessage(f), "--token") {
		t.Fatalf("got %v", f)
	}
}

func TestSignupNamesTheMachineItSignsIn(t *testing.T) {
	server := newCodeSignup(t)
	if _, f := runTerminal(t, signUp, "signup", "--profile", "dev", "--url", server.URL, "--email", "o@example.com", "--no-setup"); f != nil {
		t.Fatal(f)
	}
	if last := fake.calls[len(fake.calls)-1]; last["machine"] != machineName() {
		t.Fatalf("complete sent %v", last)
	}
}

// loggedOut logs in with the token "good", sets the sign-out answer, then logs out.
func loggedOut(t *testing.T, answer string) map[string]any {
	t.Helper()
	isolate(t)
	server := fakeServer(t)
	if _, f := run(t, "good\n", "login", "--profile", "dev", "--url", server.URL, "--no-setup"); f != nil {
		t.Fatal(f)
	}
	session.logout = answer
	if answer == "unreachable" {
		server.Close()
	}
	value, f := run(t, "", "--profile", "dev", "logout")
	if f != nil {
		t.Fatalf("logout failed: %v", f)
	}
	if token, _ := keyring.Get(keyringService, credentialKey(server.URL, "dev")); token != "" {
		t.Fatalf("the local copy survived: %q", token)
	}
	if profiles, _ := run(t, "", "profiles"); len(profiles.([]any)) != 0 {
		t.Fatalf("the profile survived: %v", profiles)
	}
	return value.(map[string]any)
}

func TestLogoutRevokesTheSignInOnItsServer(t *testing.T) {
	reply := loggedOut(t, "")
	if reply["server"] != "signed_out" || len(session.logouts) != 1 || session.logouts[0] != "good" {
		t.Fatalf("got %v after %v", reply, session.logouts)
	}
}

func TestLogoutOfAnAlreadyRevokedSignInSucceeds(t *testing.T) {
	if reply := loggedOut(t, "unauthenticated"); reply["server"] != "signed_out" || reply["note"] != nil {
		t.Fatalf("got %v", reply)
	}
}

func TestLogoutOfATokenDeletesOnlyTheLocalCopyAndSaysSo(t *testing.T) {
	reply := loggedOut(t, "not_a_cli_credential")
	if note, _ := reply["note"].(string); reply["server"] != "local_only" || !strings.Contains(note, "still works") {
		t.Fatalf("got %v", reply)
	}
}

func TestLogoutTheServerDidNotConfirmNamesRevokeConnection(t *testing.T) {
	for _, answer := range []string{"broken", "unreachable"} {
		reply := loggedOut(t, answer)
		if note, _ := reply["note"].(string); reply["server"] != "local_only" || !strings.Contains(note, "revoke_connection") {
			t.Errorf("%s: got %v", answer, reply)
		}
	}
}

// pluginHome isolates, works from an empty project folder, and creates ~/.claude.
func pluginHome(t *testing.T) string {
	t.Helper()
	home := isolate(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func putFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const pluginOn = `{"enabledPlugins": {"spun@spun-ink": true, "other@x": true}}`

func TestSetupLeavesAPluginServedClaudeToThePluginAndTakesBackItsCopy(t *testing.T) {
	home := pluginHome(t)
	dir := filepath.Join(home, ".claude", "skills", "spun")
	if _, f := run(t, "", "setup", "claude"); f != nil {
		t.Fatal(f)
	}
	putFile(t, filepath.Join(home, ".claude", "settings.json"), pluginOn)
	value, f := run(t, "", "setup")
	if f != nil {
		t.Fatal(f)
	}
	row := value.([]skillRow)[0]
	if row.Action != "plugin" || !strings.Contains(row.Note, "removed") {
		t.Fatalf("got %+v", row)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the CLI's copy survived beside the plugin: %v", err)
	}
	if value, _ := run(t, "", "setup", "claude"); value.(map[string]any)["action"] != "plugin" {
		t.Fatalf("a named agent got a second copy: %v", value)
	}
}

func TestTheProjectFoldersSettingsDecideOverTheUsers(t *testing.T) {
	home := pluginHome(t)
	putFile(t, filepath.Join(home, ".claude", "settings.json"), pluginOn)
	putFile(t, filepath.Join(".claude", "settings.local.json"), `{"enabledPlugins": {"spun@spun-ink": false}}`)
	if value, _ := run(t, "", "setup"); value.([]skillRow)[0].Action != "installed" {
		t.Fatalf("got %v", value)
	}
}

func TestLoginStartsEachAgentOnTheSkillItHasWithTheFirstPrompt(t *testing.T) {
	home := pluginHome(t)
	codex := filepath.Join(home, "codex")
	t.Setenv("CODEX_HOME", codex)
	putFile(t, filepath.Join(codex, "config.toml"), "model = \"x\"\n\n[plugins.\"spun@spun-ink\"]\nenabled = true\n")
	server := fakeServer(t)
	value, f := run(t, "good\n", "login", "--profile", "dev", "--url", server.URL)
	if f != nil {
		t.Fatal(f)
	}
	next := strings.Join(value.(map[string]any)["next"].([]string), "\n")
	for _, want := range []string{`claude "/spun ` + firstPrompt + `"`, `codex '$spun:spun ` + firstPrompt + `'`, "in your project folder"} {
		if !strings.Contains(next, want) {
			t.Errorf("next lacks %q:\n%s", want, next)
		}
	}
}
