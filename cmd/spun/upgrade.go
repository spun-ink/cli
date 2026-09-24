package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

// Seams a test replaces to find the binary, run it or fail a step of the swap.
var (
	executablePath = os.Executable
	elevated       = runningElevated
	probe          = probeVersion
	linkFile       = os.Link
	renameFile     = os.Rename
)

const probeTimeout = 10 * time.Second

func (a *app) upgradeCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "upgrade [version]",
		Short: "Update spun to the latest release, or to a named one; --check only reports",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			a.quiet = true
			ctx := context.Background()
			var target string
			if len(args) == 1 {
				var ok bool
				if target, ok = canonicalVersion(args[0]); !ok {
					return usage("%q is not a version — spun upgrade X.Y.Z, or no argument for the latest", args[0])
				}
				if check {
					return usage("--check reports the latest release and takes no version")
				}
			}
			if check {
				latest, err := latestRelease(ctx)
				if err != nil {
					return upgradeFailed("%v", err)
				}
				current, ok := canonicalVersion(version)
				return a.emit(map[string]any{
					"current": version, "latest": strings.TrimPrefix(latest, "v"),
					"available": !ok || semver.Compare(latest, current) > 0,
				})
			}
			exe, err := selfUpdatable()
			if err != nil {
				return err
			}
			if target == "" {
				if target, err = latestRelease(ctx); err != nil {
					return upgradeFailed("%v", err)
				}
			}
			result, err := upgrade(ctx, exe, target)
			if err != nil {
				return err
			}
			return a.emit(result)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only report the running and the latest version; change nothing")
	return cmd
}

func upgradeFailed(format string, a ...any) *Fail {
	return fail(exitNetwork, "upgrade_failed", fmt.Sprintf(format, a...)+" — nothing was changed")
}

func upgradeBroken(exe, backup string, err error) *Fail {
	return fail(exitNetwork, "upgrade_broken", fmt.Sprintf(
		"the upgrade failed and the previous binary could not be put back (%v) — it is kept at %s: move it to %s",
		err, backup, exe))
}

func upgradeRequired(message string) *Fail { return fail(exitConfig, "upgrade_required", message) }

// installer is the one-line install command for this system.
func installer() string {
	if runtime.GOOS == "windows" {
		return "irm https://raw.githubusercontent.com/spun-ink/cli/main/scripts/install.ps1 | iex"
	}
	return "curl -fsSL https://raw.githubusercontent.com/spun-ink/cli/main/scripts/install.sh | sh"
}

// installerDir is where the installer puts spun by default.
func installerDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "spun")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin")
}

// selfUpdatable answers the binary a self-update may replace, or why this one may not: only a
// release build, run as the user, from inside the user's home directory — where the installers put
// it. Anything else belongs to whoever installed it, and an unclear case refuses.
func selfUpdatable() (string, error) {
	switch buildSource {
	case "release":
	case "module":
		return "", upgradeRequired("this spun was built by go install — update it the same way: " +
			"go install github.com/spun-ink/cli/cmd/spun@latest (or @vX.Y.Z)")
	default:
		return "", upgradeRequired(fmt.Sprintf("this is a %s build, not a release, so it cannot update itself — install a release: %s", buildSource, installer()))
	}
	if elevated() {
		return "", upgradeRequired("spun upgrade does not run as root or administrator — run it as the user who installed spun")
	}
	exe, err := executablePath()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return "", upgradeRequired(fmt.Sprintf("cannot tell where this spun is installed (%v) — reinstall it: %s", err, installer()))
	}
	home, err := os.UserHomeDir()
	if err == nil {
		home, err = filepath.EvalSymlinks(home)
	}
	if err != nil || !within(home, exe) {
		message := fmt.Sprintf("%s is outside your home directory, so spun leaves it to whoever installed it — "+
			"update it there, or install your own copy: %s", exe, installer())
		if filepath.Dir(exe) != installerDir() {
			message += fmt.Sprintf(" (that installs a second spun in %s: remove this one or put that first on PATH)", installerDir())
		}
		return "", upgradeRequired(message)
	}
	return exe, nil
}

// within: path lies inside dir. macOS and Windows compare names without case; the original path
// is what the file operations use.
func within(dir, path string) bool {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		dir, path = strings.ToLower(dir), strings.ToLower(path)
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// parseVersionLine reads the version out of `spun --version`: "spun version X (source)", or
// "spun version X" from a release that predates the source.
func parseVersionLine(out string) (string, bool) {
	fields := strings.Fields(out)
	if len(fields) < 3 || fields[0] != "spun" || fields[1] != "version" {
		return "", false
	}
	return canonicalVersion(fields[2])
}

// probeVersion runs a binary's --version, which neither refreshes the skill nor checks for updates.
func probeVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("%s --version: %v", path, err)
	}
	v, ok := parseVersionLine(string(out))
	if !ok {
		return "", fmt.Errorf("%s --version answered %q", path, strings.TrimSpace(string(out)))
	}
	return v, nil
}

// sibling is a new name beside path that nothing holds yet: .spun-<random><suffix>.
func sibling(path, suffix string) (string, error) {
	for range 8 {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		name := filepath.Join(filepath.Dir(path), ".spun-"+hex.EncodeToString(b)+suffix)
		if _, err := os.Lstat(name); errors.Is(err, os.ErrNotExist) {
			return name, nil
		}
	}
	return "", fmt.Errorf("no free name beside %s", path)
}

// narrate tells a person at a terminal what the upgrade is doing; stdout keeps only the result.
func narrate(format string, a ...any) {
	if isTerminal(os.Stdout) {
		fmt.Fprintf(notices, format+"\n", a...)
	}
}

// upgrade replaces the binary at exe with release target. Nothing on disk changes before the new
// binary has been downloaded, checked against checksums.txt, unpacked beside exe and has answered
// with the target's version. The swap runs under a lock and keeps the old binary until the new one
// answers from its final place.
func upgrade(ctx context.Context, exe, target string) (map[string]any, error) {
	running := strings.TrimPrefix(version, "v")
	if current, ok := canonicalVersion(version); ok && semver.Compare(target, current) <= 0 {
		return map[string]any{"status": "up_to_date", "from": running, "to": running}, nil
	}
	installed, err := probe(exe)
	if err != nil {
		return nil, upgradeFailed("the installed spun does not answer: %v", err)
	}
	if semver.Compare(installed, target) >= 0 {
		return upToDate(installed), nil
	}

	number := strings.TrimPrefix(target, "v")
	archive := fmt.Sprintf("spun_%s_%s_%s.tar.gz", number, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		archive = strings.TrimSuffix(archive, ".tar.gz") + ".zip"
	}
	base := releaseURL + "/releases/download/" + target + "/"
	narrate("Downloading spun %s", number)
	data, err := fetch(ctx, base+archive, maxArchive)
	if err != nil {
		return nil, upgradeFailed("%v", err)
	}
	sums, err := fetch(ctx, base+"checksums.txt", maxChecksums)
	if err != nil {
		return nil, upgradeFailed("%v", err)
	}
	want, err := checksumFor(sums, archive)
	if err != nil {
		return nil, upgradeFailed("%v", err)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != want {
		return nil, upgradeFailed("checksum mismatch for %s", archive)
	}
	binary, err := binaryFrom(data, strings.HasSuffix(archive, ".zip"), binaryName)
	if err != nil {
		return nil, upgradeFailed("%s: %v", archive, err)
	}

	staged, err := stage(exe, binary)
	if err != nil {
		return nil, upgradeFailed("could not stage the new binary beside %s: %v", exe, err)
	}
	defer os.Remove(staged) // gone by then unless the swap never happened
	if got, err := probe(staged); err != nil || got != target {
		return nil, upgradeFailed("the downloaded binary does not answer as %s: %v", number, orVersion(err, got))
	}

	unlock, err := lockInstallDir(filepath.Dir(exe))
	if err != nil {
		return nil, upgradeFailed("could not lock %s: %v", filepath.Dir(exe), err)
	}
	defer unlock()
	removeDisposable(exe)
	// Another run may have finished while this one waited for the lock.
	if installed, err = probe(exe); err != nil {
		return nil, upgradeFailed("the installed spun does not answer: %v", err)
	}
	if semver.Compare(installed, target) >= 0 {
		return upToDate(installed), nil
	}

	narrate("Installing spun %s", number)
	backup, err := sibling(exe, ".backup")
	if err != nil {
		return nil, upgradeFailed("%v", err)
	}
	if err := swapIn(exe, staged, backup); err != nil {
		return nil, err
	}
	if got, err := probe(exe); err != nil || got != target {
		if restoreErr := renameFile(backup, exe); restoreErr != nil {
			return nil, upgradeBroken(exe, backup, restoreErr)
		}
		return nil, fail(exitNetwork, "upgrade_rolled_back", fmt.Sprintf(
			"the new binary did not answer as %s (%v) — spun %s is back in place", number, orVersion(err, got), strings.TrimPrefix(installed, "v")))
	}
	if old, err := sibling(exe, ".old"); err == nil && renameFile(backup, old) == nil {
		_ = os.Remove(old) // Windows keeps the running exe: the next upgrade removes it
	}
	return map[string]any{"status": "upgraded", "from": strings.TrimPrefix(installed, "v"), "to": number}, nil
}

func upToDate(installed string) map[string]any {
	v := strings.TrimPrefix(installed, "v")
	return map[string]any{"status": "up_to_date", "from": v, "to": v}
}

func orVersion(err error, got string) any {
	if err != nil {
		return err
	}
	return "it answered " + strings.TrimPrefix(got, "v")
}

// stage writes the new binary beside exe, executable and synced, so the swap is one rename.
func stage(exe string, binary []byte) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(exe), ".spun-*.new")
	if err != nil {
		return "", err
	}
	_, err = f.Write(binary)
	if err == nil {
		err = f.Chmod(0o755)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// removeDisposable deletes the binaries earlier upgrades verified as replaced — `.old` only, never a
// `.backup`, which is a recovery file.
func removeDisposable(exe string) {
	old, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".spun-*.old"))
	for _, path := range old {
		_ = os.Remove(path)
	}
}
