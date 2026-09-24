package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// A test binary run with SPUN_TEST_VERSION answers --version like a release of that version.
func TestMain(m *testing.M) {
	if v := os.Getenv("SPUN_TEST_VERSION"); v != "" {
		fmt.Println("spun version " + v + " (release)")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A fake binary is its own --version line, which fakeProbe reads instead of running it.
func fakeBinary(v string) []byte { return []byte("spun version " + v + " (release)\n") }

func fakeProbe(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if v, ok := parseVersionLine(string(data)); ok {
		return v, nil
	}
	return "", fmt.Errorf("%s does not answer", path)
}

// installed puts a release of version v at ~/.local/bin, as the installer would, and runs the rest
// of the test as that binary.
func installed(t *testing.T, v string) string {
	t.Helper()
	home := isolate(t)
	asRelease(t, v)
	dir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, binaryName)
	if err := os.WriteFile(exe, fakeBinary(v), 0o755); err != nil {
		t.Fatal(err)
	}
	previous := []any{executablePath, elevated, probe, linkFile, renameFile}
	executablePath = func() (string, error) { return exe, nil }
	elevated = func() bool { return false }
	probe = fakeProbe
	t.Cleanup(func() {
		executablePath = previous[0].(func() (string, error))
		elevated = previous[1].(func() bool)
		probe = previous[2].(func(string) (string, error))
		linkFile = previous[3].(func(string, string) error)
		renameFile = previous[4].(func(string, string) error)
	})
	return exe
}

type entry struct {
	name, body string
	link, dir  bool
}

func releaseEntries(v string) []entry {
	return []entry{{name: "LICENSE", body: "MIT"}, {name: "README.md", body: "#"}, {name: binaryName, body: string(fakeBinary(v))}}
}

func tarGz(t *testing.T, entries []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		header := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		switch {
		case e.link:
			header.Typeflag, header.Linkname, header.Size = tar.TypeSymlink, "/etc/passwd", 0
		case e.dir:
			header.Typeflag, header.Size = tar.TypeDir, 0
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil && !e.link && !e.dir {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func zipped(t *testing.T, entries []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		header := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		header.SetMode(0o755)
		switch {
		case e.link:
			header.SetMode(os.ModeSymlink | 0o777)
		case e.dir:
			header.SetMode(os.ModeDir | 0o755)
		}
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(e.body))
	}
	zw.Close()
	return buf.Bytes()
}

// release is one published version on the fake server; a test may spoil any of its files.
type release struct {
	archive   []byte
	checksums []byte
}

func archiveName(v string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("spun_%s_windows_%s.zip", v, runtime.GOARCH)
	}
	return fmt.Sprintf("spun_%s_%s_%s.tar.gz", v, runtime.GOOS, runtime.GOARCH)
}

func nativeArchive(t *testing.T, entries []entry) []byte {
	if runtime.GOOS == "windows" {
		return zipped(t, entries)
	}
	return tarGz(t, entries)
}

func checksums(name string, data []byte) []byte {
	sum := sha256.Sum256(data)
	return []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
}

// publish serves releases like GitHub: releases/latest redirects to latest's tag, and every
// version's archive and checksums.txt lie under releases/download.
func publish(t *testing.T, latest string, versions ...string) map[string]*release {
	t.Helper()
	releases := map[string]*release{}
	var mu sync.Mutex
	for _, v := range append(versions, latest) {
		archive := nativeArchive(t, releaseEntries(v))
		releases[v] = &release{archive, checksums(archiveName(v), archive)}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("a release request carried a credential")
		}
		if r.URL.Path == "/releases/latest" {
			http.Redirect(w, r, "/releases/tag/v"+latest, http.StatusFound)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for v, rel := range releases {
			switch r.URL.Path {
			case "/releases/download/v" + v + "/" + archiveName(v):
				if rel.archive != nil {
					_, _ = w.Write(rel.archive)
					return
				}
			case "/releases/download/v" + v + "/checksums.txt":
				if rel.checksums != nil {
					_, _ = w.Write(rel.checksums)
					return
				}
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	releaseURL = server.URL
	return releases
}

func contents(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// leftovers is every file the upgrade left beside the binary, bar the lock.
func leftovers(t *testing.T, exe string) []string {
	t.Helper()
	names, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".spun-*"))
	return names
}

func TestUpgradeReplacesTheBinary(t *testing.T) {
	exe := installed(t, "1.2.0")
	publish(t, "1.4.0")
	t.Setenv("SPUN_TOKEN", "good") // a stored or given token never goes to GitHub
	value, f := run(t, "", "upgrade")
	if f != nil {
		t.Fatal(f)
	}
	want := map[string]any{"status": "upgraded", "from": "1.2.0", "to": "1.4.0"}
	if fmt.Sprint(value) != fmt.Sprint(want) {
		t.Fatalf("got %v", value)
	}
	if contents(t, exe) != string(fakeBinary("1.4.0")) {
		t.Fatal("the binary was not replaced")
	}
	if left := leftovers(t, exe); len(left) != 0 {
		t.Fatalf("left behind %v", left)
	}
}

func TestUpgradeReachesANamedPrerelease(t *testing.T) {
	exe := installed(t, "1.2.0")
	publish(t, "1.4.0", "1.5.0-rc.1")
	if _, f := run(t, "", "upgrade", "v1.5.0-rc.1"); f != nil {
		t.Fatal(f)
	}
	if contents(t, exe) != string(fakeBinary("1.5.0-rc.1")) {
		t.Fatal("the prerelease was not installed")
	}
}

func TestUpgradeNeverGoesBackwards(t *testing.T) {
	exe := installed(t, "1.4.0")
	publish(t, "1.4.0", "1.2.0")
	for _, argv := range [][]string{{"upgrade"}, {"upgrade", "1.2.0"}} {
		value, f := run(t, "", argv...)
		if f != nil {
			t.Fatal(f)
		}
		if got := value.(map[string]any); got["status"] != "up_to_date" || got["to"] != "1.4.0" {
			t.Fatalf("%v: got %v", argv, got)
		}
	}
	if contents(t, exe) != string(fakeBinary("1.4.0")) {
		t.Fatal("the binary changed")
	}
}

func TestUpgradeRefusesAMalformedVersion(t *testing.T) {
	installed(t, "1.2.0")
	for _, v := range []string{"1.2", "latest", "v1.2.3+build", "01.2.3"} {
		if _, f := run(t, "", "upgrade", v); exitCode(f) != exitUsage {
			t.Fatalf("%s: exit %d", v, exitCode(f))
		}
	}
}

func TestUpgradeCheckOnlyReports(t *testing.T) {
	exe := installed(t, "1.2.0")
	publish(t, "1.4.0")
	value, f := run(t, "", "upgrade", "--check")
	if f != nil {
		t.Fatal(f)
	}
	want := map[string]any{"current": "1.2.0", "latest": "1.4.0", "available": true}
	if fmt.Sprint(value) != fmt.Sprint(want) {
		t.Fatalf("got %v", value)
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 || contents(t, exe) != string(fakeBinary("1.2.0")) {
		t.Fatalf("--check changed the install dir: %v", entries)
	}
}

func TestUpgradeRefusesWhatItMayNotReplace(t *testing.T) {
	cases := map[string]func(t *testing.T){
		"go install": func(t *testing.T) { buildSource = "module" },
		"dev build":  func(t *testing.T) { buildSource = "dev" },
		"snapshot":   func(t *testing.T) { buildSource = "snapshot" },
		"elevated":   func(t *testing.T) { elevated = func() bool { return true } },
		"unresolvable": func(t *testing.T) {
			executablePath = func() (string, error) { return "", errors.New("no /proc") }
		},
		"outside home": func(t *testing.T) {
			outside := filepath.Join(t.TempDir(), binaryName)
			if err := os.WriteFile(outside, fakeBinary("1.2.0"), 0o755); err != nil {
				t.Fatal(err)
			}
			executablePath = func() (string, error) { return outside, nil }
		},
		"a symlink out of home": func(t *testing.T) {
			outside := filepath.Join(t.TempDir(), binaryName)
			if err := os.WriteFile(outside, fakeBinary("1.2.0"), 0o755); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(os.Getenv("HOME"), "spun-link")
			if err := os.Symlink(outside, link); err != nil {
				t.Skip("no symlinks here:", err)
			}
			executablePath = func() (string, error) { return link, nil }
		},
	}
	for name, refuse := range cases {
		t.Run(name, func(t *testing.T) {
			exe := installed(t, "1.2.0")
			publish(t, "1.4.0")
			refuse(t)
			_, f := run(t, "", "upgrade")
			if exitCode(f) != exitConfig || errorCode(f) != "upgrade_required" {
				t.Fatalf("got %v", f)
			}
			if contents(t, exe) != string(fakeBinary("1.2.0")) {
				t.Fatal("the binary changed")
			}
		})
	}
}

func TestAHintNamesTheWayToUpdate(t *testing.T) {
	installed(t, "1.2.0")
	buildSource = "module"
	_, f := run(t, "", "upgrade")
	if !strings.Contains(f.message(), "go install github.com/spun-ink/cli/cmd/spun@latest") {
		t.Fatalf("got %q", f.message())
	}
	buildSource = "dev"
	_, f = run(t, "", "upgrade")
	if !strings.Contains(f.message(), "scripts/install.") {
		t.Fatalf("got %q", f.message())
	}
}

func TestABadReleaseChangesNothing(t *testing.T) {
	cases := map[string]func(t *testing.T, rel *release){
		"checksum mismatch":  func(t *testing.T, rel *release) { rel.archive = append(rel.archive, 0) },
		"malformed checksum": func(t *testing.T, rel *release) { rel.checksums = []byte("abc  " + archiveName("1.4.0") + "\n") },
		"archive not listed": func(t *testing.T, rel *release) { rel.checksums = checksums("other.tar.gz", rel.archive) },
		"archive listed twice": func(t *testing.T, rel *release) {
			rel.checksums = append(rel.checksums, rel.checksums...)
		},
		"missing archive":   func(t *testing.T, rel *release) { rel.archive = nil },
		"missing checksums": func(t *testing.T, rel *release) { rel.checksums = nil },
		"a link": func(t *testing.T, rel *release) {
			resign(rel, nativeArchive(t, append(releaseEntries("1.4.0"), entry{name: "evil", link: true})))
		},
		"the binary twice": func(t *testing.T, rel *release) {
			resign(rel, nativeArchive(t, append(releaseEntries("1.4.0"), entry{name: "./" + binaryName, body: "x"})))
		},
		"a path out of the archive": func(t *testing.T, rel *release) {
			resign(rel, nativeArchive(t, append(releaseEntries("1.4.0"), entry{name: "../escape", body: "x"})))
		},
		"no binary": func(t *testing.T, rel *release) {
			resign(rel, nativeArchive(t, releaseEntries("1.4.0")[:2]))
		},
		"a binary that answers wrongly": func(t *testing.T, rel *release) {
			entries := releaseEntries("1.4.0")
			entries[2].body = string(fakeBinary("1.3.0"))
			resign(rel, nativeArchive(t, entries))
		},
		"not an archive": func(t *testing.T, rel *release) { resign(rel, []byte("<html>")) },
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			exe := installed(t, "1.2.0")
			spoil(t, publish(t, "1.4.0")["1.4.0"])
			_, f := run(t, "", "upgrade")
			if exitCode(f) != exitNetwork || errorCode(f) != "upgrade_failed" {
				t.Fatalf("got %v", f)
			}
			if contents(t, exe) != string(fakeBinary("1.2.0")) {
				t.Fatal("the binary changed")
			}
			if left := leftovers(t, exe); len(left) != 0 {
				t.Fatalf("left behind %v", left)
			}
		})
	}
}

func resign(rel *release, archive []byte) {
	rel.archive, rel.checksums = archive, checksums(archiveName("1.4.0"), archive)
}

func TestANetworkFailureChangesNothing(t *testing.T) {
	exe := installed(t, "1.2.0")
	_, f := run(t, "", "upgrade") // releaseURL points at a closed port
	if exitCode(f) != exitNetwork || errorCode(f) != "upgrade_failed" {
		t.Fatalf("got %v", f)
	}
	if contents(t, exe) != string(fakeBinary("1.2.0")) {
		t.Fatal("the binary changed")
	}
}

func TestOversizedArchivesAreRefused(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "spun", Mode: 0o755, Size: maxBinary + 1, Typeflag: tar.TypeReg})
	tw.Flush()
	gz.Close()
	if _, err := binaryFrom(buf.Bytes(), false, "spun"); err == nil {
		t.Fatal("a tarball claiming more than the ceiling was read")
	}

	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	header := &zip.FileHeader{Name: "spun.exe", Method: zip.Store, UncompressedSize64: maxBinary + 1, CompressedSize64: 1}
	w, _ := zw.CreateRaw(header)
	_, _ = w.Write([]byte("x"))
	zw.Close()
	if _, err := binaryFrom(zbuf.Bytes(), true, "spun.exe"); err == nil {
		t.Fatal("a zip claiming more than the ceiling was read")
	}
}

func TestBothArchiveFormatsYieldTheBinary(t *testing.T) {
	for name, archive := range map[string][]byte{"tar.gz": tarGz(t, releaseEntries("1.4.0")), "zip": zipped(t, releaseEntries("1.4.0"))} {
		got, err := binaryFrom(archive, name == "zip", binaryName)
		if err != nil || string(got) != string(fakeBinary("1.4.0")) {
			t.Fatalf("%s: got %q, %v", name, got, err)
		}
		dir := append(releaseEntries("1.4.0"), entry{name: "docs/", dir: true}, entry{name: "docs/x.md", body: "x"})
		if name == "zip" {
			archive = zipped(t, dir)
		} else {
			archive = tarGz(t, dir)
		}
		if _, err := binaryFrom(archive, name == "zip", binaryName); err != nil {
			t.Fatalf("%s: a plain subdirectory refused the archive: %v", name, err)
		}
	}
}

func TestALinkRefusalFallsBackToACopy(t *testing.T) {
	exe := installed(t, "1.2.0")
	publish(t, "1.4.0")
	linkFile = func(string, string) error { return errors.New("links not supported") }
	if _, f := run(t, "", "upgrade"); f != nil {
		t.Fatal(f)
	}
	if contents(t, exe) != string(fakeBinary("1.4.0")) {
		t.Fatal("the binary was not replaced")
	}
}

func TestAFailedSwapChangesNothing(t *testing.T) {
	exe := installed(t, "1.2.0")
	publish(t, "1.4.0")
	renameFile = func(from, to string) error {
		if strings.HasSuffix(from, ".new") {
			return errors.New("sharing violation")
		}
		return os.Rename(from, to)
	}
	_, f := run(t, "", "upgrade")
	if errorCode(f) != "upgrade_failed" {
		t.Fatalf("got %v", f)
	}
	if contents(t, exe) != string(fakeBinary("1.2.0")) {
		t.Fatal("the binary changed")
	}
	if left := leftovers(t, exe); len(left) != 0 {
		t.Fatalf("left behind %v", left)
	}
}

// failFinalProbe makes the installed binary fail its last check, the one after the swap.
func failFinalProbe(exe string) {
	calls := 0
	probe = func(path string) (string, error) {
		if filepath.Base(path) == filepath.Base(exe) {
			if calls++; calls == 3 {
				return "", errors.New("exec format error")
			}
		}
		return fakeProbe(path)
	}
}

func TestANewBinaryThatDoesNotAnswerIsRolledBack(t *testing.T) {
	exe := installed(t, "1.2.0")
	publish(t, "1.4.0")
	failFinalProbe(exe)
	_, f := run(t, "", "upgrade")
	if exitCode(f) != exitNetwork || errorCode(f) != "upgrade_rolled_back" {
		t.Fatalf("got %v", f)
	}
	if contents(t, exe) != string(fakeBinary("1.2.0")) {
		t.Fatal("the previous binary is not back")
	}
}

func TestAFailedRollbackNamesTheBackup(t *testing.T) {
	exe := installed(t, "1.2.0")
	publish(t, "1.4.0")
	failFinalProbe(exe)
	renameFile = func(from, to string) error {
		if strings.HasSuffix(from, ".backup") && filepath.Base(to) == binaryName {
			return errors.New("access denied")
		}
		return os.Rename(from, to)
	}
	_, f := run(t, "", "upgrade")
	if errorCode(f) != "upgrade_broken" {
		t.Fatalf("got %v", f)
	}
	backups, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".spun-*.backup"))
	if len(backups) != 1 || !strings.Contains(f.message(), backups[0]) {
		t.Fatalf("message %q does not name the backup %v", f.message(), backups)
	}
	if contents(t, backups[0]) != string(fakeBinary("1.2.0")) {
		t.Fatal("the backup is not the previous binary")
	}
}

func TestAnEarlierBackupIsKeptAndDisposablesGo(t *testing.T) {
	exe := installed(t, "1.2.0")
	publish(t, "1.4.0")
	dir := filepath.Dir(exe)
	keep, old := filepath.Join(dir, ".spun-earlier.backup"), filepath.Join(dir, ".spun-earlier.old")
	for _, path := range []string{keep, old} {
		if err := os.WriteFile(path, []byte("earlier"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, f := run(t, "", "upgrade"); f != nil {
		t.Fatal(f)
	}
	if contents(t, keep) != "earlier" {
		t.Fatal("a recovery backup was touched")
	}
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a disposable binary survived")
	}
}

func TestParallelUpgradesInstallOnce(t *testing.T) {
	exe := installed(t, "1.2.0")
	publish(t, "1.4.0")
	statuses := make([]any, 2)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := upgrade(t.Context(), exe, "v1.4.0")
			if err != nil {
				statuses[i] = err
				return
			}
			statuses[i] = result["status"]
		}()
	}
	wg.Wait()
	got := fmt.Sprint(statuses)
	if got != "[upgraded up_to_date]" && got != "[up_to_date upgraded]" {
		t.Fatalf("got %s", got)
	}
	if contents(t, exe) != string(fakeBinary("1.4.0")) {
		t.Fatal("the binary was not replaced")
	}
}

func TestAWaitingRunNeverInstallsAnOlderVersion(t *testing.T) {
	exe := installed(t, "1.2.0")
	publish(t, "1.5.0", "1.4.0")
	if _, err := upgrade(t.Context(), exe, "v1.5.0"); err != nil {
		t.Fatal(err)
	}
	result, err := upgrade(t.Context(), exe, "v1.4.0") // started as 1.2.0, reaches the lock after 1.5.0
	if err != nil || result["status"] != "up_to_date" || result["to"] != "1.5.0" {
		t.Fatalf("got %v, %v", result, err)
	}
}

func TestProbeRunsTheBinary(t *testing.T) {
	t.Setenv("SPUN_TEST_VERSION", "1.4.0")
	got, err := probeVersion(os.Args[0])
	if err != nil || got != "v1.4.0" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestParseVersionLine(t *testing.T) {
	for line, want := range map[string]string{
		"spun version 1.4.0 (release)\n":    "v1.4.0",
		"spun version 1.4.0\n":              "v1.4.0", // a release from before the build source
		"spun version v1.5.0-rc.1 (module)": "v1.5.0-rc.1",
		"spun version dev (dev)":            "",
		"something else":                    "",
	} {
		if got, _ := parseVersionLine(line); got != want && !(want == "" && got != "") {
			t.Fatalf("%q: got %q, want %q", line, got, want)
		}
		if _, ok := parseVersionLine(line); ok != (want != "") {
			t.Fatalf("%q: ok %v", line, ok)
		}
	}
}

func TestWithin(t *testing.T) {
	home := filepath.Join(string(filepath.Separator)+"home", "ada")
	for path, want := range map[string]bool{
		filepath.Join(home, ".local", "bin", "spun"):                     true,
		filepath.Join(string(filepath.Separator)+"home", "ada2", "spun"): false,
		filepath.Join(string(filepath.Separator)+"usr", "local", "bin"):  false,
		home: false,
	} {
		if within(home, path) != want {
			t.Fatalf("%s: want %v", path, want)
		}
	}
}
