package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// runSignup runs `spun signup` with stdout captured and the browser stubbed; opened holds the
// addresses it was asked to open.
func runSignup(t *testing.T, atTerminal bool, argv ...string) (out string, opened []string, err error) {
	t.Helper()
	previous := openBrowser
	openBrowser = func(url string) error { opened = append(opened, url); return nil }
	t.Cleanup(func() { openBrowser = previous })
	var buf bytes.Buffer
	a := &app{stdin: stdinWith(t, ""), stdout: &buf, tty: atTerminal}
	_, err = a.run(append([]string{"signup"}, argv...))
	return buf.String(), opened, err
}

func TestSignupPrintsThePageAndTheLoginLineAndOpensTheBrowserAtATerminal(t *testing.T) {
	isolate(t)
	out, opened, err := runSignup(t, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Sign up in the browser: "+defaultServer+"/signup\n") ||
		!strings.Contains(out, "Then store your token: spun login\n") {
		t.Fatalf("got %q", out)
	}
	if len(opened) != 1 || opened[0] != defaultServer+"/signup" {
		t.Fatalf("opened %v", opened)
	}
}

func TestSignupWithoutATerminalPrintsTheSameLinesAndOpensNothing(t *testing.T) {
	isolate(t)
	out, opened, err := runSignup(t, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Sign up in the browser: "+defaultServer+"/signup\n") ||
		!strings.Contains(out, "Then store your token: spun login\n") {
		t.Fatalf("got %q", out)
	}
	if len(opened) != 0 {
		t.Fatalf("opened %v", opened)
	}
}

func TestSignupNamesTheServerAndProfileInTheLoginLine(t *testing.T) {
	isolate(t)
	out, _, err := runSignup(t, false, "--profile", "dev", "--url", "http://spun.localhost:3002")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Sign up in the browser: http://spun.localhost:3002/signup\n") ||
		!strings.Contains(out, "Then store your token: spun login --profile dev --url http://spun.localhost:3002\n") {
		t.Fatalf("got %q", out)
	}
}

func TestSignupKeepsADevServerOutOfTheDefaultProfile(t *testing.T) {
	isolate(t)
	for _, argv := range [][]string{
		{"--url", "http://spun.localhost:3002"},
		{"--profile", defaultProfile, "--url", "http://spun.localhost:3002"},
	} {
		if _, f := run(t, "", append([]string{"signup"}, argv...)...); exitCode(f) != exitUsage {
			t.Errorf("%v: got %v", argv, f)
		}
	}
}

func TestSignupAcceptsTheRetiredFlagsAndSaysOnceThatTheyAreIgnored(t *testing.T) {
	isolate(t)
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = stderr
	t.Cleanup(func() { os.Stderr = previous })

	out, opened, runErr := runSignup(t, false, "--email", "o@example.com", "--name", "Rosa", "--handle", "rosa",
		"--code", "482913", "--accept-terms", "--no-setup")
	os.Stderr = previous
	if runErr != nil {
		t.Fatal(runErr)
	}
	notice, _ := os.ReadFile(stderr.Name())
	if strings.Count(string(notice), "\n") != 1 || !strings.Contains(string(notice), "happens in the browser") {
		t.Fatalf("notice %q", notice)
	}
	if !strings.Contains(out, "/signup") || len(opened) != 0 {
		t.Fatalf("got %q, opened %v", out, opened)
	}
}

func TestSignupNeverReachesTheServer(t *testing.T) {
	isolate(t)
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("signup called the server")
	}))
	t.Cleanup(server.Close)
	if _, _, err := runSignup(t, false, "--profile", "dev", "--url", server.URL, "--email", "o@example.com"); err != nil {
		t.Fatal(err)
	}
}
