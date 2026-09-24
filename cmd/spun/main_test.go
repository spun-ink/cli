package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// fakeServer answers /mcp like the spun server: 401 for any token but "good", a tool reply for
// tools/call, a scoped tools/list per token.
func fakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, _ := io.ReadAll(r.Body)
			if string(body) == "refuse" {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"ok":false,"error":{"code":"checksum_mismatch","message":"no"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"key":"logo","bytes":` + jsonNumber(len(body)) + `}`))
			return
		}
		if _, has := r.Header["Authorization"]; !has {
			tokenless(w, r)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token != "good" && token != "scoped" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		reply := func(result any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
		}
		tool := func(payload any, isError bool) {
			text, _ := json.Marshal(payload)
			reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": string(text)}}, "isError": isError})
		}
		switch req.Method {
		case "ping":
			reply(map[string]any{})
		case "tools/list":
			tools := []any{map[string]any{"name": "get_site", "description": "The site. More."}}
			if token == "good" {
				tools = append(tools, map[string]any{"name": "delete_site", "description": "Trash it."})
			}
			reply(map[string]any{"tools": tools})
		case "tools/call":
			args := req.Params.Arguments
			switch req.Params.Name {
			case "get_template":
				if args["key"] == "nope" {
					tool(map[string]any{"ok": false, "error": map[string]any{"code": "not_found", "message": "no template"}}, true)
					return
				}
				if args["key"] == "bare" {
					tool(map[string]any{"key": args["key"]}, false)
					return
				}
				tool(map[string]any{"key": args["key"], "markup": "<h1>{{ page.title }}</h1>"}, false)
			case "update_template":
				tool(map[string]any{"key": args["key"], "markup": args["markup"]}, false)
			case "create_upload_link":
				if args["filename"] == "elsewhere.png" {
					tool(map[string]any{"url": "https://elsewhere.example/upload?signature=s3cret"}, false)
					return
				}
				tool(map[string]any{"url": server.URL + "/upload"}, false)
			case "site_map":
				tool(map[string]any{
					"pages": []any{map[string]any{"slug": "home", "status": "published"}, map[string]any{"slug": "about", "status": "draft"}},
					"posts": []any{map[string]any{"slug": "hello", "status": "draft", "blog": "news"}},
				}, false)
			case "plain_refusal":
				reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": "boom"}}, "isError": true})
			case "shapeless_refusal":
				tool(map[string]any{"why": "no"}, true)
			case "broken":
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": -32602, "message": "Tool not found: broken"}})
			case "crashed":
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": -32603, "message": "internal"}})
			case "textless":
				reply(map[string]any{"content": []any{map[string]any{}}})
			case "hollow":
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1}`))
			default:
				tool(args, false)
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

const testNotice = "By creating an account, you agree to our Terms of Service (https://x/legal/terms)."

// signUpCalls counts the tokenless sign_up calls fakeServer has answered.
var signUpCalls int

// tokenless answers like the bootstrap surface: tools/list names sign_up alone with its terms, and
// sign_up refuses the handle "taken". Anything else is a 401, so a leaked tokenless call fails loudly.
func tokenless(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string `json:"method"`
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	reply := func(result any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	}
	switch {
	case req.Method == "tools/list":
		terms := map[string]any{"notice": testNotice, "terms_version": "2026-09-01", "terms_content_hash": "abc123"}
		reply(map[string]any{"tools": []any{map[string]any{"name": "sign_up", "description": "Sign up.",
			"_meta": map[string]any{termsMetaKey: terms}}}})
	case req.Method == "tools/call" && req.Params.Name == "sign_up":
		signUpCalls++
		payload := map[string]any{"ok": true, "bearer_token": "good",
			"site":  map[string]any{"handle": "bakery", "url": "https://bakery.myspun.ink"},
			"legal": map[string]any{"terms_version": "2026-09-01", "terms_content_hash": "abc123"}}
		isError := req.Params.Arguments["handle"] == "taken"
		if isError {
			payload = map[string]any{"ok": false, "error": map[string]any{"code": "validation_failed", "message": "Handle has already been taken"}}
		}
		text, _ := json.Marshal(payload)
		reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": string(text)}}, "isError": isError})
	default:
		w.WriteHeader(http.StatusUnauthorized)
	}
}

func jsonNumber(n int) string { b, _ := json.Marshal(n); return string(b) }

// isolate points every file the CLI touches at a temp dir and clears the environment.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("CODEX_HOME", "")
	for _, key := range []string{"SPUN_URL", "SPUN_TOKEN", "SPUN_PROFILE"} {
		t.Setenv(key, "")
	}
	keyring.MockInit()
	// Nothing a test runs may reach the live site; a test that wants the default sets it.
	previous := defaultServer
	defaultServer = "http://127.0.0.1:1"
	t.Cleanup(func() { defaultServer = previous })
	return home
}

func stdinWith(t *testing.T, text string) *os.File {
	t.Helper()
	r, w, _ := os.Pipe()
	_, _ = w.WriteString(text)
	w.Close()
	t.Cleanup(func() { r.Close() })
	return r
}

func run(t *testing.T, stdin string, argv ...string) (any, *Fail) {
	t.Helper()
	a := &app{stdin: stdinWith(t, stdin), stdout: io.Discard}
	value, err := a.run(argv)
	if err != nil {
		return nil, asFail(err)
	}
	return value, nil
}

func exitCode(f *Fail) int {
	if f == nil {
		return 0
	}
	return f.Code
}

func errorCode(f *Fail) any {
	return f.Body.(map[string]any)["error"].(map[string]any)["code"]
}

func TestArgsAreStringsUnlessMarkedJSON(t *testing.T) {
	args, err := parseArgs([]string{"title=123", "position:=2", "meta:={\"a\":true}"}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"title": "123", "position": float64(2), "meta": map[string]any{"a": true}}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %v, want %v", args, want)
	}
}

func TestAtPathReadsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hero.liquid")
	_ = os.WriteFile(path, []byte("<h1>hi</h1>\n"), 0o644)
	args, err := parseArgs([]string{"markup=@" + path}, "")
	if err != nil || args["markup"] != "<h1>hi</h1>\n" {
		t.Fatalf("got %v, %v", args, err)
	}
}

func TestOnlyTheFirstEqualsSignSeparates(t *testing.T) {
	args, err := parseArgs([]string{"markup=x := 1", "title=a=b", "note=:=", "url=https://x.test/?a=b"}, "")
	want := map[string]any{"markup": "x := 1", "title": "a=b", "note": ":=", "url": "https://x.test/?a=b"}
	if err != nil || !reflect.DeepEqual(args, want) {
		t.Fatalf("got %v, %v", args, err)
	}
}

func TestColonEqualsAtPathReadsAJSONFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hero.json")
	_ = os.WriteFile(path, []byte(`{"title":"Hi"}`), 0o644)
	args, err := parseArgs([]string{"data:=@" + path, `handle:="@spun"`}, "")
	want := map[string]any{"data": map[string]any{"title": "Hi"}, "handle": "@spun"}
	if err != nil || !reflect.DeepEqual(args, want) {
		t.Fatalf("got %v, %v", args, err)
	}
}

func TestMalformedArgsAreUsageErrors(t *testing.T) {
	for _, pair := range []string{"position:=nope", "bare", "=value", ":=1", "markup=@/no/such/file"} {
		if _, err := parseArgs([]string{pair}, ""); exitCode(asFail(err)) != exitUsage {
			t.Errorf("%s: got %v", pair, err)
		}
	}
}

func TestSiteFlagBecomesTheSelector(t *testing.T) {
	args, _ := parseArgs(nil, "blog")
	if !reflect.DeepEqual(args, map[string]any{"site": "blog"}) {
		t.Fatalf("got %v", args)
	}
}

func TestSummaryIsTheFirstSentence(t *testing.T) {
	if got := summary("Fetch one template. Then more."); got != "Fetch one template." {
		t.Fatalf("got %q", got)
	}
}

func TestPipedJSONIsCompactAndKeepsMarkupReadable(t *testing.T) {
	if got := render(map[string]any{"markup": "<h1>&"}, false); got != `{"markup":"<h1>&"}` {
		t.Fatalf("got %s", got)
	}
}

func TestTerminalJSONIsIndented(t *testing.T) {
	if got := render(map[string]any{"ok": true}, true); got != "{\n  \"ok\": true\n}" {
		t.Fatalf("got %q", got)
	}
}

func TestTerminalToolRowsAreAligned(t *testing.T) {
	rows := toolTable{{"get_site", "The site."}}
	if got := render(rows, true); got != "get_site                   The site." {
		t.Fatalf("got %q", got)
	}
	if got := render(rows, false); got != `[{"name":"get_site","summary":"The site."}]` {
		t.Fatalf("piped: %q", got)
	}
}

func TestRenderTakesAnyJSONValue(t *testing.T) {
	for _, value := range []any{[]any{"a", 1.0}, []any{map[string]any{"name": "x", "summary": "y", "id": 1.0}}, []any{}, nil, "text"} {
		for _, tty := range []bool{true, false} {
			render(value, tty)
		}
	}
	if got := render([]any{map[string]any{"name": "x", "summary": "y", "id": 1.0}}, false); !strings.Contains(got, `"id":1`) {
		t.Fatalf("a row with a summary lost its fields: %s", got)
	}
	if got := render("line\n", false); got != `"line\n"` {
		t.Fatalf("a piped string is not JSON: %s", got)
	}
}

func TestErrorBodyIsTheSharedShape(t *testing.T) {
	got := render(usage("bad").Body, false)
	if got != `{"error":{"code":"usage","message":"bad"},"ok":false}` {
		t.Fatalf("got %s", got)
	}
}

func TestExitCodes(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	cases := []struct {
		name  string
		env   map[string]string
		argv  []string
		want  int
		error string
	}{
		{"ok", map[string]string{"SPUN_URL": server.URL, "SPUN_TOKEN": "good"}, []string{"call", "get_site"}, 0, ""},
		{"tool refused", map[string]string{"SPUN_URL": server.URL, "SPUN_TOKEN": "good"}, []string{"call", "get_template", "key=nope"}, 1, "not_found"},
		{"tool refused in plain text", map[string]string{"SPUN_URL": server.URL, "SPUN_TOKEN": "good"}, []string{"call", "plain_refusal"}, 1, "tool_error"},
		{"unknown command", nil, []string{"frobnicate"}, 2, "usage"},
		{"unknown flag", nil, []string{"tools", "--nope"}, 2, "usage"},
		{"missing argument", nil, []string{"call"}, 2, "usage"},
		{"token rejected", map[string]string{"SPUN_URL": server.URL, "SPUN_TOKEN": "bad"}, []string{"call", "get_site"}, 3, "unauthorized"},
		{"token missing", map[string]string{"SPUN_URL": server.URL}, []string{"call", "get_site"}, 3, "unauthorized"},
		{"server down", map[string]string{"SPUN_URL": "http://127.0.0.1:1", "SPUN_TOKEN": "good"}, []string{"call", "get_site"}, 4, "network"},
		{"unknown tool", map[string]string{"SPUN_URL": server.URL, "SPUN_TOKEN": "good"}, []string{"call", "broken"}, 2, ""},
		{"json-rpc error", map[string]string{"SPUN_URL": server.URL, "SPUN_TOKEN": "good"}, []string{"call", "crashed"}, 4, ""},
		{"tool reply without text", map[string]string{"SPUN_URL": server.URL, "SPUN_TOKEN": "good"}, []string{"call", "textless"}, 4, "bad_response"},
		{"reply without result", map[string]string{"SPUN_URL": server.URL, "SPUN_TOKEN": "good"}, []string{"call", "hollow"}, 4, "bad_response"},
		{"refusal without error field", map[string]string{"SPUN_URL": server.URL, "SPUN_TOKEN": "good"}, []string{"call", "shapeless_refusal"}, 1, "tool_error"},
		{"cleartext to a remote host", map[string]string{"SPUN_URL": "http://spun.example", "SPUN_TOKEN": "good"}, []string{"call", "get_site"}, 5, "config_invalid"},
		{"not logged in", nil, []string{"call", "get_site"}, 5, "config_missing"},
		{"unknown profile", nil, []string{"--profile", "nope", "tools"}, 5, "config_missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			_, f := run(t, "", tc.argv...)
			if exitCode(f) != tc.want {
				t.Fatalf("exit %d, want %d (%v)", exitCode(f), tc.want, f)
			}
			if tc.error != "" && errorCode(f) != tc.error {
				t.Fatalf("error code %v, want %s", errorCode(f), tc.error)
			}
		})
	}
}

func TestADevProfileIsNeverTheDefault(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	if _, f := run(t, "good\n", "login", "--profile", "dev", "--url", server.URL); f != nil {
		t.Fatal(f)
	}
	if _, f := run(t, "", "call", "get_site"); exitCode(f) != exitConfig {
		t.Fatalf("got %v", f)
	}
}

func TestTheSpunInkProfileIsTheDefault(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	defaultServer = server.URL
	value, f := run(t, "good\n", "login")
	if f != nil || value.(map[string]any)["profile"] != defaultProfile || value.(map[string]any)["url"] != server.URL {
		t.Fatalf("got %v, %v", value, f)
	}
	if _, f := run(t, "", "call", "get_site"); f != nil {
		t.Fatalf("unnamed command: %v", f)
	}
	if _, f := run(t, "", "logout"); f != nil {
		t.Fatalf("logout without --profile: %v", f)
	}
	if value, _ := run(t, "", "profiles"); len(value.([]any)) != 0 {
		t.Fatalf("logout kept the default: %v", value)
	}
}

func TestSpunURLNeverFallsThroughToTheDefault(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	defaultServer = server.URL
	run(t, "good\n", "login")
	t.Setenv("SPUN_URL", "http://127.0.0.1:1")
	if _, f := run(t, "", "call", "get_site"); exitCode(f) != exitUnauthorized {
		t.Fatalf("SPUN_URL without SPUN_TOKEN: %v", f)
	}
}

func TestSpunTokenAloneGoesToTheDefaultServer(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	defaultServer = server.URL
	t.Setenv("SPUN_TOKEN", "good")
	if _, f := run(t, "", "call", "get_site"); f != nil {
		t.Fatalf("got %v", f)
	}
}

func TestProfileResolution(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	if _, f := run(t, "good\n", "login", "--token", "--profile", "dev", "--url", server.URL+"/"); f != nil {
		t.Fatal(f)
	}
	if _, f := run(t, "", "--profile", "dev", "call", "get_site"); f != nil {
		t.Fatalf("--profile: %v", f)
	}
	t.Setenv("SPUN_PROFILE", "dev")
	if _, f := run(t, "", "call", "get_site"); f != nil {
		t.Fatalf("SPUN_PROFILE: %v", f)
	}
	t.Setenv("SPUN_TOKEN", "bad")
	if _, f := run(t, "", "call", "get_site"); exitCode(f) != exitUnauthorized {
		t.Fatalf("SPUN_TOKEN should override the stored token: %v", f)
	}
	t.Setenv("SPUN_TOKEN", "")
	t.Setenv("SPUN_URL", server.URL+"/mcp")
	if _, f := run(t, "", "call", "get_site"); f != nil {
		t.Fatalf("SPUN_URL naming the profile's own server: %v", f)
	}
}

func TestAStoredTokenNeverGoesToAnotherServer(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	if _, f := run(t, "good\n", "login", "--token", "--profile", "dev", "--url", server.URL); f != nil {
		t.Fatal(f)
	}
	reached := false
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	t.Cleanup(other.Close)
	t.Setenv("SPUN_URL", other.URL)
	_, f := run(t, "", "--profile", "dev", "call", "get_site")
	if exitCode(f) != exitConfig || errorCode(f) != "config_conflict" || reached {
		t.Fatalf("got %v, other server reached: %v", f, reached)
	}
}

func TestServerRoot(t *testing.T) {
	for raw, want := range map[string]string{
		"https://spun.ink":           "https://spun.ink",
		"https://Spun.INK/":          "https://spun.ink",
		"https://spun.ink/mcp":       "https://spun.ink",
		"https://spun.ink/mcp/":      "https://spun.ink",
		"http://spun.localhost:3002": "http://spun.localhost:3002",
		"http://127.0.0.1:3002":      "http://127.0.0.1:3002",
		"http://[::1]:3002":          "http://[::1]:3002",
		"https://spun.ink:443":       "https://spun.ink",
		"https://x.example/api%3Ft":  "https://x.example/api%3Ft",
	} {
		if got, err := serverRoot(raw); got != want || err != nil {
			t.Errorf("%s: got %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{
		"spun.ink", "ftp://spun.ink", "http://spun.ink", "http://localhost.example",
		"https://user:pw@spun.ink", "https://spun.ink/?x=1", "https://spun.ink/#frag", "https://spun.ink?",
	} {
		if got, err := serverRoot(raw); err == nil {
			t.Errorf("%s: accepted as %q", raw, got)
		}
	}
}

func TestARedirectIsNeverFollowed(t *testing.T) {
	isolate(t)
	sawToken := false
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		sawToken = r.Header.Get("Authorization") != ""
	}))
	t.Cleanup(target.Close)
	redirect := httptest.NewServer(http.RedirectHandler(target.URL+"/mcp", http.StatusTemporaryRedirect))
	t.Cleanup(redirect.Close)
	t.Setenv("SPUN_URL", redirect.URL)
	t.Setenv("SPUN_TOKEN", "good")
	_, f := run(t, "", "call", "get_site")
	if exitCode(f) != exitNetwork || errorCode(f) != "redirected" || sawToken {
		t.Fatalf("got %v, token followed the redirect: %v", f, sawToken)
	}
}

func TestLoginStoresInTheKeyring(t *testing.T) {
	home := isolate(t)
	server := fakeServer(t)
	value, f := run(t, "good\n", "login", "--token", "--profile", "dev", "--url", server.URL)
	if f != nil || value.(map[string]any)["token_stored_in"] != "keyring" {
		t.Fatalf("got %v, %v", value, f)
	}
	if token, _ := keyring.Get(keyringService, credentialKey(server.URL, "dev")); token != "good" {
		t.Fatalf("keyring holds %q", token)
	}
	if _, err := os.Stat(filepath.Join(home, "config", "spun", "credentials.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credentials.json written although the keyring worked: %v", err)
	}
	config, _ := os.ReadFile(filepath.Join(home, "config", "spun", "config.json"))
	if strings.Contains(string(config), "good") {
		t.Fatalf("config.json holds the token: %s", config)
	}
}

func TestLoginStoresNothingForARejectedToken(t *testing.T) {
	home := isolate(t)
	server := fakeServer(t)
	if _, f := run(t, "bad\n", "login", "--token", "--profile", "dev", "--url", server.URL); exitCode(f) != exitUnauthorized {
		t.Fatalf("got %v", f)
	}
	if _, err := os.Stat(filepath.Join(home, "config", "spun")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("something was stored: %v", err)
	}
}

func TestLoginNeverTakesTheTokenAsAnArgument(t *testing.T) {
	isolate(t)
	if _, f := run(t, "", "login", "--token", "--profile", "dev", "--url", "http://x", "secret"); exitCode(f) != exitUsage {
		t.Fatalf("got %v", f)
	}
}

func TestLoginKeepsADevServerOutOfTheDefaultProfile(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	for _, argv := range [][]string{
		{"login", "--url", server.URL},
		{"login", "--profile", defaultProfile, "--url", server.URL},
		{"login", "--token", "--profile", "dev", "--url", "spun.ink"},
	} {
		if _, f := run(t, "good", argv...); exitCode(f) != exitUsage {
			t.Errorf("%v: got %v", argv, f)
		}
	}
	if value, _ := run(t, "", "profiles"); len(value.([]any)) != 0 {
		t.Fatalf("stored: %v", value)
	}
}

func TestLoginSetsUpEveryAgentFound(t *testing.T) {
	home := isolate(t)
	server := fakeServer(t)
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	mine := filepath.Join(home, ".codex", "skills", "spun")
	_ = os.MkdirAll(mine, 0o755)
	_ = os.WriteFile(filepath.Join(mine, "SKILL.md"), []byte("mine"), 0o644)

	value, f := run(t, "good\n", "login", "--profile", "dev", "--url", server.URL)
	if f != nil {
		t.Fatal(f)
	}
	rows := value.(map[string]any)["skills"].([]skillRow)
	if len(rows) != 2 || rows[0].Agent != "claude" || rows[0].Action != "installed" ||
		rows[1].Agent != "codex" || rows[1].Action != "skipped" || !strings.Contains(rows[1].Note, managedMarker) {
		t.Fatalf("got %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "spun", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(mine, "SKILL.md")); string(data) != "mine" {
		t.Fatalf("overwritten: %s", data)
	}
}

func TestLoginWithNoSetupWritesNoSkill(t *testing.T) {
	home := isolate(t)
	server := fakeServer(t)
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	value, f := run(t, "good\n", "login", "--no-setup", "--profile", "dev", "--url", server.URL)
	if _, has := value.(map[string]any)["skills"]; f != nil || has {
		t.Fatalf("got %v, %v", value, f)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a skill was written: %v", err)
	}
}

func errorMessage(f *Fail) string { return f.message() }

func TestBareLoginSaysWhereTheTokenComesFrom(t *testing.T) {
	isolate(t)
	_, f := run(t, "", "login")
	message := errorMessage(f)
	for _, want := range []string{"no token on stdin", "spun signup", "/recover"} {
		if !strings.Contains(message, want) {
			t.Errorf("message lacks %q: %s", want, message)
		}
	}
}

func TestLoginWithTheTokenInTheFlagNeverEchoesIt(t *testing.T) {
	isolate(t)
	_, f := run(t, "", "login", "--token=s3cret", "--profile", "dev", "--url", "http://x")
	if exitCode(f) != exitUsage || strings.Contains(errorMessage(f), "s3cret") || !strings.Contains(errorMessage(f), "never an argument") {
		t.Fatalf("got %v", f)
	}
}

func TestLoginSuggestsTheSchemeForABareHost(t *testing.T) {
	isolate(t)
	_, f := run(t, "", "login", "--token", "--profile", "dev", "--url", "spun.ink")
	if !strings.Contains(errorMessage(f), "did you mean https://spun.ink") {
		t.Fatalf("got %v", f)
	}
}

func TestLoginNamesTheNextSteps(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	value, f := run(t, "good\n", "login", "--token", "--profile", "dev", "--url", server.URL)
	if out := render(value, false); f != nil || !strings.Contains(out, "export SPUN_PROFILE=dev") || !strings.Contains(out, "spun setup claude") {
		t.Fatalf("got %s, %v", out, f)
	}
	defaultServer = server.URL
	value, _ = run(t, "good\n", "login")
	if out := render(value, false); strings.Contains(out, "SPUN_PROFILE") {
		t.Fatalf("the default needs no profile: %s", out)
	}
}

func TestARejectedTokenPointsToRecover(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	_, f := run(t, "bad\n", "login", "--token", "--profile", "dev", "--url", server.URL)
	if !strings.Contains(errorMessage(f), server.URL+"/recover") {
		t.Fatalf("got %v", f)
	}
}

func TestNoServerNamedListsTheStoredProfiles(t *testing.T) {
	isolate(t)
	if _, f := run(t, "", "tools"); !strings.Contains(errorMessage(f), "not logged in — run `spun signup` for a new account or `spun login`") {
		t.Fatalf("empty store: %v", f)
	}
	server := fakeServer(t)
	run(t, "good\n", "login", "--token", "--profile", "dev", "--url", server.URL)
	run(t, "good\n", "login", "--token", "--profile", "production", "--url", server.URL)
	if _, f := run(t, "", "tools"); exitCode(f) != exitConfig || !strings.Contains(errorMessage(f), "stored profiles: dev, production") {
		t.Fatalf("got %v", f)
	}
}

func TestBareSpunShowsGettingStartedUntilAProfileExists(t *testing.T) {
	isolate(t)
	help := func() string {
		var out strings.Builder
		a := &app{stdin: stdinWith(t, ""), stdout: &out}
		if _, err := a.run(nil); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if !strings.Contains(help(), "Getting started:") {
		t.Fatal("no getting-started block on a fresh machine")
	}
	server := fakeServer(t)
	run(t, "good\n", "login", "--token", "--profile", "dev", "--url", server.URL)
	if strings.Contains(help(), "Getting started:") {
		t.Fatal("getting-started block shown although a profile is stored")
	}
}

func TestProfilesNeverPrintATokenAndLogoutForgets(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	run(t, "good\n", "login", "--token", "--profile", "dev", "--url", server.URL)
	value, _ := run(t, "", "profiles")
	if out := render(value, false); strings.Contains(out, "good") || !strings.Contains(out, `"name":"dev"`) {
		t.Fatalf("got %s", out)
	}
	if _, f := run(t, "", "--profile", "dev", "logout"); f != nil {
		t.Fatal(f)
	}
	if _, err := keyring.Get(keyringService, credentialKey(server.URL, "dev")); err == nil {
		t.Fatal("token survived logout")
	}
	if _, f := run(t, "", "--profile", "dev", "tools"); exitCode(f) != exitConfig {
		t.Fatalf("got %v", f)
	}
}

func TestToolsCacheIsPerToken(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	t.Setenv("SPUN_URL", server.URL)
	t.Setenv("SPUN_TOKEN", "good")
	full, _ := run(t, "", "tools")
	t.Setenv("SPUN_TOKEN", "scoped")
	scoped, _ := run(t, "", "tools")
	if len(full.(toolTable)) != 2 || len(scoped.(toolTable)) != 1 {
		t.Fatalf("full %v, scoped %v", full, scoped)
	}
	path := (&Client{server.URL, "good"}).cachePath()
	if strings.Contains(path, "good") {
		t.Fatalf("token in the cache path: %s", path)
	}
}

func TestHelpForAToolAndForACommand(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	t.Setenv("SPUN_URL", server.URL)
	t.Setenv("SPUN_TOKEN", "good")
	value, f := run(t, "", "help", "get_site")
	if f != nil || value.(map[string]any)["name"] != "get_site" {
		t.Fatalf("got %v %v", value, f)
	}
	if _, f := run(t, "", "help", "nope"); exitCode(f) != exitUsage {
		t.Fatalf("got %v", f)
	}
	if value, f := run(t, "", "help", "template"); value != nil || f != nil {
		t.Fatalf("command help should print, not return: %v %v", value, f)
	}
}

func TestTemplatePullIsExactAndPushDropsTheEcho(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	t.Setenv("SPUN_URL", server.URL)
	t.Setenv("SPUN_TOKEN", "good")
	value, _ := run(t, "", "template", "pull", "hero")
	if value != raw("<h1>{{ page.title }}</h1>") {
		t.Fatalf("got %#v", value)
	}
	path := filepath.Join(t.TempDir(), "hero.liquid")
	_ = os.WriteFile(path, []byte("<h2>x</h2>"), 0o644)
	value, f := run(t, "", "template", "push", "hero", path)
	if f != nil {
		t.Fatal(f)
	}
	if _, echoed := value.(map[string]any)["markup"]; echoed {
		t.Fatalf("markup echoed: %v", value)
	}
}

func TestAssetUpload(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	t.Setenv("SPUN_URL", server.URL)
	t.Setenv("SPUN_TOKEN", "good")
	dir := t.TempDir()
	ok, refused := filepath.Join(dir, "logo.png"), filepath.Join(dir, "bad.png")
	_ = os.WriteFile(ok, []byte("png!"), 0o644)
	_ = os.WriteFile(refused, []byte("refuse"), 0o644)
	if value, f := run(t, "", "asset", "upload", ok); f != nil || value.(map[string]any)["bytes"] != float64(4) {
		t.Fatalf("got %v %v", value, f)
	}
	if _, f := run(t, "", "asset", "upload", refused); exitCode(f) != exitToolError || errorCode(f) != "checksum_mismatch" {
		t.Fatalf("got %v", f)
	}
}

func TestContentListIdsOnly(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	t.Setenv("SPUN_URL", server.URL)
	t.Setenv("SPUN_TOKEN", "good")
	value, _ := run(t, "", "content", "list", "--ids-only")
	if value != raw("home\nabout\nhello\n") {
		t.Fatalf("got %#v", value)
	}
	value, _ = run(t, "", "content", "list", "--ids-only", "--status", "draft", "--kind", "post")
	if value != raw("hello\n") {
		t.Fatalf("got %#v", value)
	}
	value, _ = run(t, "", "content", "list")
	if row := value.([]any)[2].(map[string]any); row["kind"] != "post" || row["blog"] != "news" {
		t.Fatalf("got %v", row)
	}
}

func TestSetupWritesAMarkedSkillAndRemovesOnlyItsOwn(t *testing.T) {
	home := isolate(t)
	value, f := run(t, "", "setup", "claude")
	path := filepath.Join(home, ".claude", "skills", "spun")
	if f != nil || value.(map[string]any)["path"] != path {
		t.Fatalf("got %v %v", value, f)
	}
	for _, name := range []string{"SKILL.md", managedMarker} {
		if _, err := os.Stat(filepath.Join(path, name)); err != nil {
			t.Fatal(err)
		}
	}
	if value, _ := run(t, "", "setup", "claude"); value.(map[string]any)["action"] != "updated" {
		t.Fatalf("rerun: %v", value)
	}
	if value, _ := run(t, "", "setup", "claude", "--remove"); value.(map[string]any)["action"] != "removed" {
		t.Fatalf("remove: %v", value)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("marked directory survived --remove")
	}
}

func TestSetupNeverTouchesAnUnmarkedDirectory(t *testing.T) {
	home := isolate(t)
	path := filepath.Join(home, ".codex", "skills", "spun")
	_ = os.MkdirAll(path, 0o755)
	_ = os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("mine"), 0o644)
	for _, argv := range [][]string{{"setup", "codex"}, {"setup", "codex", "--remove"}} {
		if _, f := run(t, "", argv...); exitCode(f) != exitConfig || errorCode(f) != "not_managed" {
			t.Fatalf("%v: got %v", argv, f)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(path, "SKILL.md")); string(data) != "mine" {
		t.Fatalf("overwritten: %s", data)
	}
}

func TestSetupHonoursCodexHome(t *testing.T) {
	home := isolate(t)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	value, _ := run(t, "", "setup", "codex")
	if value.(map[string]any)["path"] != filepath.Join(home, "codex", "skills", "spun") {
		t.Fatalf("got %v", value)
	}
	if _, f := run(t, "", "setup", "cursor"); exitCode(f) != exitUsage {
		t.Fatalf("got %v", f)
	}
}

func TestSetupWithoutAnAgentCoversEveryAgentFound(t *testing.T) {
	home := isolate(t)
	if _, f := run(t, "", "setup"); exitCode(f) != exitConfig || errorCode(f) != "no_agent" {
		t.Fatalf("no agent installed: %v", f)
	}
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	_ = os.MkdirAll(filepath.Join(home, "codex"), 0o755)
	value, f := run(t, "", "setup")
	if rows := value.([]skillRow); f != nil || len(rows) != 1 || rows[0].Agent != "codex" || rows[0].Action != "installed" {
		t.Fatalf("got %v, %v", value, f)
	}
	if value, _ := run(t, "", "setup", "--remove"); value.([]skillRow)[0].Action != "removed" {
		t.Fatalf("remove: %v", value)
	}
	if _, f := run(t, "", "setup", "--dir", home); exitCode(f) != exitUsage {
		t.Fatalf("--dir without an agent: %v", f)
	}
}

func TestTemplatePullWithoutMarkupFails(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	t.Setenv("SPUN_URL", server.URL)
	t.Setenv("SPUN_TOKEN", "good")
	if _, f := run(t, "", "template", "pull", "bare"); exitCode(f) != exitNetwork || errorCode(f) != "bad_response" {
		t.Fatalf("got %v", f)
	}
}

func TestUploadStaysOnTheServerAndUnderTheCap(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	t.Setenv("SPUN_URL", server.URL)
	t.Setenv("SPUN_TOKEN", "good")
	dir := t.TempDir()
	elsewhere, big := filepath.Join(dir, "elsewhere.png"), filepath.Join(dir, "big.png")
	_ = os.WriteFile(elsewhere, []byte("png!"), 0o644)
	_, f := run(t, "", "asset", "upload", elsewhere)
	if exitCode(f) != exitNetwork || strings.Contains(errorMessage(f), "s3cret") {
		t.Fatalf("got %v", f)
	}
	file, _ := os.Create(big)
	_ = file.Truncate(maxUpload + 1)
	file.Close()
	if _, f := run(t, "", "asset", "upload", big); exitCode(f) != exitUsage || !strings.Contains(errorMessage(f), "25 MB") {
		t.Fatalf("got %v", f)
	}
}

func TestContentListRejectsAnUnknownFilter(t *testing.T) {
	isolate(t)
	for _, argv := range [][]string{{"content", "list", "--kind", "pages"}, {"content", "list", "--status", "live"}} {
		if _, f := run(t, "", argv...); exitCode(f) != exitUsage {
			t.Errorf("%v: got %v", argv, f)
		}
	}
}

func TestLoginFromDevNullSaysThereIsNoToken(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { null.Close() })
	a := &app{stdin: null, stdout: io.Discard}
	_, err = a.run([]string{"login", "--token", "--profile", "dev", "--url", server.URL})
	if f := asFail(err); exitCode(f) != exitUsage || !strings.Contains(errorMessage(f), "no token on stdin") {
		t.Fatalf("got %v", f)
	}
}

func TestLogoutReportsAKeyringThatKeptTheToken(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	if _, f := run(t, "good\n", "login", "--token", "--profile", "dev", "--url", server.URL); f != nil {
		t.Fatal(f)
	}
	keyring.MockInitWithError(errors.New("keychain locked"))
	if _, f := run(t, "", "--profile", "dev", "logout"); exitCode(f) != exitConfig || errorCode(f) != "keyring_unavailable" {
		t.Fatalf("got %v", f)
	}
	if value, _ := run(t, "", "profiles"); len(value.([]any)) != 1 {
		t.Fatalf("a failed logout forgot the profile: %v", value)
	}
}

func TestSetupTargetIsInsideTheTestHome(t *testing.T) {
	home := isolate(t)
	for _, agent := range []string{"claude", "codex"} {
		if path, err := skillDir(agent); err != nil || !strings.HasPrefix(path, home) {
			t.Fatalf("%s would write to %s — outside the test home", agent, path)
		}
	}
}

func TestSetupRemoveLeavesFilesItDidNotWrite(t *testing.T) {
	home := isolate(t)
	path := filepath.Join(home, ".claude", "skills", "spun")
	run(t, "", "setup", "claude")
	_ = os.WriteFile(filepath.Join(path, "notes.md"), []byte("mine"), 0o644)
	value, f := run(t, "", "setup", "claude", "--remove")
	if f != nil || value.(map[string]any)["note"] == nil {
		t.Fatalf("got %v %v", value, f)
	}
	if data, _ := os.ReadFile(filepath.Join(path, "notes.md")); string(data) != "mine" {
		t.Fatal("a foreign file went with --remove")
	}
	if _, err := os.Stat(filepath.Join(path, "SKILL.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("SKILL.md survived --remove")
	}
}

func TestSetupNeverFollowsASymlink(t *testing.T) {
	home := isolate(t)
	outside := filepath.Join(home, "outside.md")
	_ = os.WriteFile(outside, []byte("precious"), 0o644)
	path := filepath.Join(home, ".claude", "skills", "spun")
	run(t, "", "setup", "claude")
	_ = os.Remove(filepath.Join(path, "SKILL.md"))
	if err := os.Symlink(outside, filepath.Join(path, "SKILL.md")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if _, f := run(t, "", "setup", "claude"); f != nil {
		t.Fatal(f)
	}
	if data, _ := os.ReadFile(outside); string(data) != "precious" {
		t.Fatal("setup wrote through the symlink")
	}

	linked := filepath.Join(home, ".codex", "skills", "spun")
	_ = os.MkdirAll(filepath.Dir(linked), 0o755)
	if err := os.Symlink(path, linked); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if _, f := run(t, "", "setup", "codex", "--remove"); exitCode(f) != exitConfig || errorCode(f) != "not_managed" {
		t.Fatalf("a symlinked skill directory was accepted: %v", f)
	}
}

func TestCacheIsKeyedByTheWholeServerURL(t *testing.T) {
	if (&Client{"https://a-b.example", "t"}).cachePath() == (&Client{"https://a.b.example", "t"}).cachePath() {
		t.Fatal("two servers share one cache file")
	}
}

func TestUploadNeverReadsADevice(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	t.Setenv("SPUN_URL", server.URL)
	t.Setenv("SPUN_TOKEN", "good")
	if _, f := run(t, "", "asset", "upload", os.DevNull); exitCode(f) != exitUsage {
		t.Fatalf("got %v", f)
	}
}

func TestOneServerSpelledTwiceIsOneOrigin(t *testing.T) {
	if !sameOrigin("https://spun.ink/uploads/x?sig=1", "https://SPUN.ink:443") || sameOrigin("https://spun.ink", "http://spun.ink") {
		t.Fatal("origins compared by spelling")
	}
}

func TestSetupTrailingSlashStillSeesTheSymlink(t *testing.T) {
	home := isolate(t)
	path := filepath.Join(home, ".claude", "skills", "spun")
	run(t, "", "setup", "claude")
	linked := filepath.Join(home, "linked")
	if err := os.Symlink(path, linked); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if _, f := run(t, "", "setup", "claude", "--dir", linked+string(filepath.Separator), "--remove"); exitCode(f) != exitConfig {
		t.Fatalf("got %v", f)
	}
	if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
		t.Fatal("removed through the symlink")
	}
}

func TestAProfileWithoutACredentialMustLogInAgain(t *testing.T) {
	home := isolate(t)
	server := fakeServer(t)
	dir := filepath.Join(home, "config", "spun")
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"profiles":{"dev":{"url":"`+server.URL+`"}}}`), 0o600)
	_ = keyring.Set(keyringService, "dev", "good")
	if _, f := run(t, "", "--profile", "dev", "call", "get_site"); exitCode(f) != exitUnauthorized {
		t.Fatalf("a token stored under the bare profile name answered: %v", f)
	}
	if _, f := run(t, "", "--profile", "dev", "logout"); f != nil {
		t.Fatal(f)
	}
	if _, err := keyring.Get(keyringService, "dev"); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("logout left the unbound token: %v", err)
	}
}

func TestACredentialOfAnotherServerIsRefused(t *testing.T) {
	home := isolate(t)
	server := fakeServer(t)
	dir := filepath.Join(home, "config", "spun")
	_ = os.MkdirAll(dir, 0o700)
	other := credentialKey("https://elsewhere.example", "dev")
	_ = os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"profiles":{"dev":{"url":"`+server.URL+
		`","store":"keyring","credential":"`+other+`"}}}`), 0o600)
	_ = keyring.Set(keyringService, other, "good")
	if _, f := run(t, "", "--profile", "dev", "call", "get_site"); exitCode(f) != exitConfig || errorCode(f) != "config_invalid" {
		t.Fatalf("got %v", f)
	}
}

// failConfigWrites makes every write of config.json fail, as a full disk or a read-only directory would.
func failConfigWrites(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { writeFile = writeAtomic })
	writeFile = func(path string, data []byte, perm os.FileMode) error {
		if filepath.Base(path) == "config.json" {
			return errors.New("disk full")
		}
		return writeAtomic(path, data, perm)
	}
}

func TestAFailedReassignmentKeepsTheOldServerAndItsToken(t *testing.T) {
	isolate(t)
	a, b := fakeServer(t), fakeServer(t)
	if _, f := run(t, "good\n", "login", "--profile", "dev", "--url", a.URL); f != nil {
		t.Fatal(f)
	}
	bCalls := 0
	inner := b.Config.Handler
	b.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bCalls++
		inner.ServeHTTP(w, r)
	})
	failConfigWrites(t)
	if _, f := run(t, "scoped\n", "login", "--profile", "dev", "--url", b.URL); exitCode(f) != exitConfig {
		t.Fatalf("the config write should have failed: %v", f)
	}
	writeFile = writeAtomic

	value, f := run(t, "", "--profile", "dev", "tools")
	if f != nil || len(value.(toolTable)) != 2 {
		t.Fatalf("dev should still reach the first server with its own token: %v, %v", value, f)
	}
	if _, err := keyring.Get(keyringService, credentialKey(b.URL, "dev")); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("the unreferenced token for the second server stayed: %v", err)
	}
	if bCalls != 1 {
		t.Fatalf("the second server was called %d times, want only the login ping", bCalls)
	}
}

func TestAReassignedProfileForgetsItsOldToken(t *testing.T) {
	isolate(t)
	a, b := fakeServer(t), fakeServer(t)
	run(t, "good\n", "login", "--profile", "dev", "--url", a.URL)
	if _, f := run(t, "scoped\n", "login", "--profile", "dev", "--url", b.URL); f != nil {
		t.Fatal(f)
	}
	if _, err := keyring.Get(keyringService, credentialKey(a.URL, "dev")); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("the first server's token stayed: %v", err)
	}
	if value, f := run(t, "", "--profile", "dev", "tools"); f != nil || len(value.(toolTable)) != 1 {
		t.Fatalf("got %v, %v", value, f)
	}
}

func TestTwoProfilesOnOneServerKeepTheirOwnTokens(t *testing.T) {
	isolate(t)
	server := fakeServer(t)
	run(t, "good\n", "login", "--profile", "agency", "--url", server.URL)
	run(t, "scoped\n", "login", "--profile", "client", "--url", server.URL)
	if _, f := run(t, "", "--profile", "agency", "logout"); f != nil {
		t.Fatal(f)
	}
	if value, f := run(t, "", "--profile", "client", "tools"); f != nil || len(value.(toolTable)) != 1 {
		t.Fatalf("logout of one profile touched the other: %v, %v", value, f)
	}
}
