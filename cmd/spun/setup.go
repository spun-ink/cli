package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
)

//go:embed skill/SKILL.md
var skillText string

// managedMarker names every directory `spun setup` wrote. Setup overwrites and removes only a
// directory that carries it, so it can never touch a skill someone else put there.
const managedMarker = ".managed-by-spun-cli"

var agents = []string{"claude", "codex"}

// agentHome is the directory an agent keeps its configuration in; the agent counts as installed
// when it exists.
func agentHome(agent string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fail(exitConfig, "config_missing", "no home directory: "+err.Error())
	}
	switch agent {
	case "claude":
		return filepath.Join(home, ".claude"), nil
	case "codex":
		if base := os.Getenv("CODEX_HOME"); base != "" {
			return base, nil
		}
		return filepath.Join(home, ".codex"), nil
	}
	return "", usage("unknown agent %q — spun setup claude | codex", agent)
}

// pluginID is the spun plugin as both agents name it: plugin spun from the marketplace spun-ink.
const pluginID = "spun@spun-ink"

// pluginServes reports whether the agent has the spun plugin enabled, read where the agent reads it.
// Claude Code's nearest scope decides: the project folder's local settings, then its shared ones,
// then the user's. Codex enables plugins in its own config.toml. A served agent has the skill from
// the plugin, and a second copy from setup would only compete with it.
func pluginServes(agent string) bool {
	home, err := agentHome(agent)
	if err != nil {
		return false
	}
	switch agent {
	case "claude":
		for _, path := range []string{filepath.Join(".claude", "settings.local.json"),
			filepath.Join(".claude", "settings.json"), filepath.Join(home, "settings.json")} {
			if on, set := claudePlugin(path); set {
				return on
			}
		}
	case "codex":
		on, _ := codexPlugin(filepath.Join(home, "config.toml"))
		return on
	}
	return false
}

func claudePlugin(path string) (on, set bool) {
	var settings struct {
		EnabledPlugins map[string]any `json:"enabledPlugins"`
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &settings) != nil {
		return false, false
	}
	on, set = settings.EnabledPlugins[pluginID].(bool)
	return on, set
}

func codexPlugin(path string) (on, set bool) {
	var config struct {
		Plugins map[string]struct {
			Enabled *bool `toml:"enabled"`
		} `toml:"plugins"`
	}
	if _, err := toml.DecodeFile(path, &config); err != nil {
		return false, false
	}
	plugin, ok := config.Plugins[pluginID]
	if !ok || plugin.Enabled == nil {
		return false, false
	}
	return *plugin.Enabled, true
}

// servedByPlugin leaves the skill to the plugin and takes back a copy setup wrote earlier.
func servedByPlugin(agent, path string) (action, note string) {
	note = "the spun plugin is enabled for " + agent + " and brings the skill"
	removed, _, err := installSkill(path, true)
	switch {
	case err != nil:
		note += "; " + asFail(err).message()
	case removed == "removed":
		note += "; the copy spun setup wrote earlier is removed"
	}
	return "plugin", note
}

func skillDir(agent string) (string, error) {
	home, err := agentHome(agent)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "skills", "spun"), nil
}

func detectedAgents() []string {
	var found []string
	for _, agent := range agents {
		if home, err := agentHome(agent); err == nil {
			if info, err := os.Stat(home); err == nil && info.IsDir() {
				found = append(found, agent)
			}
		}
	}
	return found
}

// skillRow is one agent's outcome when setup runs for every agent found. A directory setup may not
// touch is a row with a note, not a failure: login has already stored the token by then.
type skillRow struct {
	Agent  string `json:"agent"`
	Path   string `json:"path"`
	Action string `json:"action"`
	Note   string `json:"note,omitempty"`
}

func (r skillRow) line() string {
	if r.Note != "" {
		return r.Agent + " " + r.Action + " — " + r.Note
	}
	return r.Agent + " " + r.Action + " (" + r.Path + ")"
}

func setupAll(remove bool) []skillRow {
	rows := []skillRow{}
	for _, agent := range detectedAgents() {
		path, err := skillDir(agent)
		if err != nil {
			continue
		}
		row := skillRow{Agent: agent, Path: path}
		if !remove && pluginServes(agent) {
			row.Action, row.Note = servedByPlugin(agent, path)
		} else if row.Action, row.Note, err = installSkill(path, remove); err != nil {
			row.Action, row.Note = "skipped", asFail(err).message()
		}
		rows = append(rows, row)
	}
	return rows
}

func (a *app) setupCmd() *cobra.Command {
	var remove bool
	var dir string
	cmd := &cobra.Command{
		Use:       "setup [claude|codex]",
		Short:     "Install (or --remove) the spun skill for one agent, or every agent found",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: agents,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				if dir != "" {
					return usage("--dir needs an agent: spun setup claude|codex --dir <path>")
				}
				rows := setupAll(remove)
				if len(rows) == 0 {
					return fail(exitConfig, "no_agent", "no agent found (~/.claude, ~/.codex or $CODEX_HOME) — name one: spun setup claude|codex")
				}
				return a.emit(rows)
			}
			path, err := skillDir(args[0])
			if err != nil {
				return err
			}
			var action, note string
			switch {
			case dir != "":
				action, note, err = installSkill(dir, remove)
				path = dir
			case !remove && pluginServes(args[0]):
				action, note = servedByPlugin(args[0], path)
			default:
				action, note, err = installSkill(path, remove)
			}
			if err != nil {
				return err
			}
			reply := map[string]any{"ok": true, "agent": args[0], "path": path, "action": action}
			if note != "" {
				reply["note"] = note
			}
			return a.emit(reply)
		},
	}
	cmd.Flags().BoolVar(&remove, "remove", false, "remove the skill (only a directory spun setup wrote)")
	cmd.Flags().StringVar(&dir, "dir", "", "install into this directory instead of the agent's default")
	return cmd
}

// installSkill touches only what it owns: a real directory carrying a regular marker file, and
// in it the marker and SKILL.md. Files are replaced, never written through, so a symlink planted
// in the directory cannot redirect a write; --remove leaves anything else in place.
func installSkill(path string, remove bool) (action, note string, err error) {
	path = filepath.Clean(path) // a trailing slash would make Lstat follow a symlinked directory
	info, statErr := os.Lstat(path)
	exists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", "", fail(exitConfig, "config_unwritable", statErr.Error())
	}
	if exists {
		if !info.IsDir() {
			return "", "", fail(exitConfig, "not_managed", fmt.Sprintf(
				"%s is not a plain directory (a symlink or a file) — left untouched", path))
		}
		if marker, err := os.Lstat(filepath.Join(path, managedMarker)); err != nil || !marker.Mode().IsRegular() {
			return "", "", fail(exitConfig, "not_managed", fmt.Sprintf(
				"%s exists and was not written by spun setup (no %s) — left untouched", path, managedMarker))
		}
	}

	if remove {
		if !exists {
			return "absent", "", nil
		}
		// The marker goes last, so a failure halfway leaves a directory setup still owns.
		for _, name := range []string{"SKILL.md", managedMarker} {
			if err := os.Remove(filepath.Join(path, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", "", fail(exitConfig, "config_unwritable", err.Error())
			}
		}
		if os.Remove(path) != nil {
			return "removed", path + " holds files spun setup did not write, so the directory stays", nil
		}
		return "removed", "", nil
	}

	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", "", fail(exitConfig, "config_unwritable", err.Error())
	}
	// The marker goes first, so a failure halfway leaves a directory setup can finish.
	marker := "Written by `spun setup`. Remove with `spun setup <agent> --remove`.\n"
	for _, file := range []struct{ name, body string }{{managedMarker, marker}, {"SKILL.md", skillText}} {
		if err := writeAtomic(filepath.Join(path, file.name), []byte(file.body), 0o644); err != nil {
			return "", "", fail(exitConfig, "config_unwritable", err.Error())
		}
	}
	if exists {
		return "updated", "", nil
	}
	return "installed", "", nil
}
