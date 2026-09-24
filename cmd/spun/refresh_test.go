package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// asVersion runs the rest of the test as a binary of that version.
func asVersion(t *testing.T, v string) {
	t.Helper()
	previous := version
	version = v
	t.Cleanup(func() { version = previous })
}

func readSkill(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// staleSkill installs the skill for claude as setup would, then makes SKILL.md an older text.
func staleSkill(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "skills", "spun")
	if _, _, err := installSkill(dir, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRefreshRewritesAManagedSkill(t *testing.T) {
	home := isolate(t)
	asVersion(t, "1.2.0")
	dir := staleSkill(t, home)
	refreshSkills()
	if readSkill(t, dir) != skillText {
		t.Fatal("the managed skill was not refreshed")
	}
}

func TestRefreshCreatesNothing(t *testing.T) {
	home := isolate(t)
	asVersion(t, "1.2.0")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	refreshSkills()
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refresh installed a skill nobody set up")
	}
}

func TestRefreshLeavesAnUnmanagedSkillAlone(t *testing.T) {
	home := isolate(t)
	asVersion(t, "1.2.0")
	dir := filepath.Join(home, ".claude", "skills", "spun")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	refreshSkills()
	if readSkill(t, dir) != "mine" {
		t.Fatal("refresh overwrote a skill spun setup did not write")
	}
}

func TestRefreshRespectsARemovedSkill(t *testing.T) {
	home := isolate(t)
	asVersion(t, "1.2.0")
	dir := staleSkill(t, home)
	if _, _, err := installSkill(dir, true); err != nil {
		t.Fatal(err)
	}
	refreshSkills()
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refresh brought back a removed skill")
	}
}

func TestRefreshNeverGoesBackwards(t *testing.T) {
	home := isolate(t)
	asVersion(t, "1.3.0")
	dir := staleSkill(t, home)
	refreshSkills()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	version = "1.2.0" // an old process, still running after the upgrade
	refreshSkills()
	if readSkill(t, dir) != "new" {
		t.Fatal("an older binary wrote its skill over a newer one")
	}
}

func TestRefreshSkipsADevBuild(t *testing.T) {
	home := isolate(t)
	asVersion(t, "dev")
	dir := staleSkill(t, home)
	refreshSkills()
	if readSkill(t, dir) != "old" {
		t.Fatal("a dev build refreshed the skill")
	}
}

func TestAnUnchangedRunOnlyReads(t *testing.T) {
	home := isolate(t)
	asVersion(t, "1.2.0")
	dir := staleSkill(t, home)
	refreshSkills()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	refreshSkills()
	if readSkill(t, dir) != "edited" {
		t.Fatal("a run with the recorded version and homes wrote again")
	}
}

func TestRefreshRetriesAfterAFailure(t *testing.T) {
	home := isolate(t)
	asVersion(t, "1.2.0")
	dir := staleSkill(t, home)
	writeFile = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	refreshSkills()
	writeFile = writeAtomic
	if _, err := os.Stat(filepath.Join(home, "config", "spun", skillStateFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a failed refresh recorded itself as done")
	}
	refreshSkills()
	if readSkill(t, dir) != skillText {
		t.Fatal("the next run did not retry")
	}
}

func TestRefreshRerunsWhenCodexHomeMoves(t *testing.T) {
	home := isolate(t)
	asVersion(t, "1.2.0")
	refreshSkills()
	codex := filepath.Join(home, "elsewhere")
	dir := filepath.Join(codex, "skills", "spun")
	if _, _, err := installSkill(dir, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codex)
	refreshSkills()
	if readSkill(t, dir) != skillText {
		t.Fatal("a new CODEX_HOME was not refreshed")
	}
}

func TestRefreshNeverChangesTheOutcome(t *testing.T) {
	home := isolate(t)
	asVersion(t, "1.2.0")
	staleSkill(t, home)
	writeFile = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	t.Cleanup(func() { writeFile = writeAtomic })
	a := &app{stdin: stdinWith(t, ""), stdout: io.Discard}
	value, err := a.run([]string{"profiles"})
	a.afterCommand()
	if err != nil || value == nil {
		t.Fatalf("got %v, %v", value, err)
	}
}

func TestHelpAndVersionNeverRefresh(t *testing.T) {
	home := isolate(t)
	asVersion(t, "1.2.0")
	dir := staleSkill(t, home)
	for _, argv := range [][]string{{"--version"}, {"help"}, {"--help"}} {
		a := &app{stdin: stdinWith(t, ""), stdout: io.Discard}
		_, _ = a.run(argv)
		a.afterCommand()
	}
	if readSkill(t, dir) != "old" {
		t.Fatal("a side-effect-free command refreshed the skill")
	}
}
