package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// version and buildSource are stamped by GoReleaser: buildSource is "release", or "snapshot" for a
// local `goreleaser --snapshot`. `go install …@<version>` reports the module version and counts as
// "module"; any other build says "dev". Only a release updates itself or checks for updates.
var (
	version     = "dev"
	buildSource = "dev"
)

func init() {
	if info, ok := debug.ReadBuildInfo(); ok && buildSource == "dev" && strings.HasPrefix(info.Main.Version, "v") {
		version, buildSource = info.Main.Version, "module"
	}
}

// versionLine is what `spun --version` prints; parseVersionLine reads it back from another binary.
func versionLine() string { return "spun version " + version + " (" + buildSource + ")" }

const longHelp = `spun — operate a Spun site from the shell

Every command is one call to the site's MCP server (POST /mcp), with the same token and the same
tools an MCP client sees. The server is https://spun.ink — the live site — once ` + "`spun signup`" + ` (a new
account) or ` + "`spun login`" + ` (an existing token) has stored your token; --profile, SPUN_PROFILE or
SPUN_URL name another (a dev server).

args:   key=value (string)   key:=json (raw JSON)   key=@path (file contents)   key:=@path (JSON file)
env:    SPUN_PROFILE  profile to use when --profile is not given
        SPUN_URL      server root, e.g. https://spun.ink — with SPUN_TOKEN, instead of a profile
        SPUN_TOKEN    bearer token — overrides the stored one; alone, it goes to https://spun.ink
output: human-readable at a terminal, JSON when piped; errors on stderr as {ok:false,error:{code,message}}
exit:   0 ok · 1 tool refused · 2 usage or unknown tool · 3 unauthorized · 4 network · 5 config`

var gettingStarted = []string{
	"Getting started:",
	"  spun signup    # a new account: asks your email, stores the token",
	"  spun login     # a token you already have: paste it",
	"  Both set up Claude Code and Codex where installed.",
}

// firstRun: nothing stored and no SPUN_URL, so no command can reach a server yet.
func firstRun() bool {
	config, err := loadConfig()
	return err == nil && len(config.Profiles) == 0 && os.Getenv("SPUN_URL") == ""
}

// app holds what one invocation shares across its commands: the global flags, stdin for login,
// and the value the command produced, which main renders.
type app struct {
	site    string
	profile string
	stdin   *os.File
	stdout  io.Writer
	bobbin  bool // draw the mascot: stdout is a terminal that takes colour
	result  any
	ran     bool // a command ran (not --version or --help)
	quiet   bool // the command leaves no trace: no skill refresh, no update check
}

func (a *app) client() (*Client, error) { return resolve(a.profile) }

func (a *app) emit(value any) error {
	a.result = value
	return nil
}

func (a *app) root() *cobra.Command {
	root := &cobra.Command{
		Use:           "spun",
		Short:         "Operate a Spun site from the shell",
		Long:          longHelp,
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetVersionTemplate(versionLine() + "\n")
	root.PersistentPreRun = func(*cobra.Command, []string) { a.ran = true }
	root.SetOut(a.stdout)
	root.SetErr(a.stdout)
	root.PersistentFlags().StringVar(&a.site, "site", "", "target site `handle` (the tools' site selector)")
	root.PersistentFlags().StringVar(&a.profile, "profile", "", "`name` of a server stored by spun login (or SPUN_PROFILE)")
	root.SetHelpCommand(a.helpCmd(root))
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd == root {
			fresh := firstRun()
			hint := []string{"Run `spun help <tool>` before a tool you have not used."}
			if fresh {
				hint = gettingStarted
			}
			if a.bobbin {
				fmt.Fprint(a.stdout, beside("digging", append([]string{"spun " + version, "https://spun.ink/docs", ""}, hint...)...))
				fmt.Fprintln(a.stdout)
			} else if fresh {
				fmt.Fprintln(a.stdout, strings.Join(hint, "\n")+"\n")
			}
		}
		defaultHelp(cmd, args)
	})
	root.AddCommand(
		a.toolsCmd(), a.callCmd(), a.assetCmd(), a.templateCmd(), a.contentCmd(),
		a.signupCmd(), a.loginCmd(), a.logoutCmd(), a.profilesCmd(), a.setupCmd(),
	)
	return root
}

// run executes one command line and returns the value to print, or the Fail to report.
func (a *app) run(argv []string) (any, error) {
	root := a.root()
	root.SetArgs(argv)
	if err := root.Execute(); err != nil {
		return nil, asFail(err)
	}
	return a.result, nil
}

func newApp() *app { return &app{stdin: os.Stdin, stdout: os.Stdout, bobbin: bobbinWanted()} }

// afterCommand runs once the command's output is written: it can neither change that output nor
// the exit code, and it writes to stderr only.
func (a *app) afterCommand() {
	if !a.ran || a.quiet {
		return
	}
}
