package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func (a *app) loginCmd() *cobra.Command {
	var withToken, noSetup bool
	var server string
	cmd := &cobra.Command{
		Use:   "login [--profile <name> --url <server>]",
		Short: "Store your token for spun.ink (or a named server) and set up your agents",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return tokenNotAnArgument()
			}
			return nil
		},
		RunE: func(*cobra.Command, []string) error {
			name, root, err := loginTarget(a.profile, server)
			if err != nil {
				return err
			}
			token, err := a.readToken()
			if err != nil {
				return err
			}

			// Stored only once the server has accepted it: ping carries the bearer like any call,
			// and an unknown token is a 401.
			c := &Client{root, token}
			if _, err := c.rpc("ping", map[string]any{}); err != nil {
				return err
			}
			where, err := storeProfile(name, c.URL, token)
			if err != nil {
				return err
			}
			reply := map[string]any{"ok": true, "profile": name, "url": c.URL, "token_stored_in": where}
			return a.reportStored(reply, "Logged in.", nil, nil, noSetup)
		},
	}
	cmd.Flags().BoolVar(&withToken, "token", false, "log in with a pasted account or operator token (the default)")
	_ = cmd.Flags().MarkHidden("token")
	cmd.Flags().StringVar(&server, "url", "", "a dev server's root, e.g. http://spun.localhost:3002 (with --profile)")
	cmd.Flags().BoolVar(&noSetup, "no-setup", false, "do not install the spun skill for the agents found")
	// `--token=<value>` is the first guess, and cobra's own parse error would echo the token.
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		if strings.Contains(err.Error(), `"--token" flag`) {
			return tokenNotAnArgument()
		}
		return err
	})
	return cmd
}

// loginTarget names the profile and server a login stores. Nothing given is spun.ink. Another server
// needs its own profile name, and the name spun.ink only ever means spun.ink — so a dev URL can
// never become the server every unnamed command reaches.
func loginTarget(profile, server string) (string, string, error) {
	if profile == "" {
		profile = defaultProfile
	}
	if server == "" {
		server = defaultServer
	}
	root, err := serverRoot(server)
	if err != nil {
		return "", "", usage("--url %v", err)
	}
	if profile == defaultProfile && !sameServer(root, defaultServer) {
		return "", "", usage("%s is not %s — give this server its own profile: `spun login --profile <name> --url %s`",
			root, defaultServer, root)
	}
	return profile, root, nil
}

// storeProfile keeps the token under a key bound to its server before the profile points at it, and
// removes a replaced credential only once the profile no longer names it: a failure at any step
// leaves the profile paired with its old server's token, never the new one.
func storeProfile(name, root, token string) (string, error) {
	config, err := loadConfig()
	if err != nil {
		return "", err
	}
	previous, had := config.Profiles[name]
	key := credentialKey(root, name)
	where, err := storeToken(key, token)
	if err != nil {
		return "", err
	}
	config.Profiles[name] = Profile{URL: root, Store: where, Credential: key}
	if err := config.save(); err != nil {
		if !had || previous.Credential != key {
			_ = deleteToken(key, where)
		}
		return "", err
	}
	if had && previous.Credential != "" && previous.Credential != key {
		_ = deleteToken(previous.Credential, previous.Store)
	}
	return where, nil
}

// reportStored finishes login and signup alike: the skill for every agent found unless noSetup,
// then the reply with its next steps — first, then the login ones.
func (a *app) reportStored(reply map[string]any, headline string, lines, first []string, noSetup bool) error {
	name := reply["profile"].(string)
	var skills []skillRow
	if !noSetup {
		skills = setupAll(false)
		reply["skills"] = skills
	}
	next := append(first, loginNext(name, noSetup, skills)...)
	reply["next"] = next
	if !a.bobbin {
		return a.emit(reply)
	}
	view := append([]string{headline, "", "profile  " + name, "server   " + reply["url"].(string),
		"token    in the " + reply["token_stored_in"].(string)}, lines...)
	for _, row := range skills {
		view = append(view, "skill    "+row.line())
	}
	view = append(view, "", "Next:")
	for _, step := range next {
		view = append(view, "  "+step)
	}
	fmt.Fprint(a.stdout, beside("shipping", view...))
	return nil
}

func loginNext(profile string, noSetup bool, skills []skillRow) []string {
	var next []string
	if profile != defaultProfile {
		next = append(next, "export SPUN_PROFILE="+profile+"   # or --profile "+profile+" on every command")
	}
	var started []string
	for _, row := range skills {
		if start, ok := agentStart[row.Agent]; ok && row.Action != "skipped" {
			started = append(started, start)
		}
	}
	if len(started) == 0 {
		next = append(next, "spun setup claude                # or codex: install the spun skill for your agent")
		started = []string{"ask your agent to work on your site"}
	}
	next = append(next, started...)
	return append(next, "or try `spun tools` yourself")
}

// agentStart opens each agent on the skill setup installed, by its name `spun`. Codex's `$spun` is
// single-quoted so the shell does not expand it.
var agentStart = map[string]string{
	"claude": `claude "/spun build my site"`,
	"codex":  `codex '$spun build my site'`,
}

const loginExample = "spun login"

const tokenSource = "No account yet? `spun signup` creates one and stores its token. A lost token is replaced at https://spun.ink/recover."

func tokenNotAnArgument() error {
	return usage("the token is never an argument (it would land in shell history) — run `%s` "+
		"and paste it at the prompt, or pipe it on stdin", loginExample)
}

// readToken takes the token from a pipe, or prompts without echo at a terminal. It never prompts
// without one: an agent's shell has no human to answer.
func (a *app) readToken() (string, error) {
	var token string
	if isTerminal(a.stdin) {
		fmt.Fprint(os.Stderr, "Token: ")
		bytes, err := term.ReadPassword(int(a.stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", usage("reading the token: %v", err)
		}
		token = string(bytes)
	} else {
		bytes, err := io.ReadAll(a.stdin)
		if err != nil {
			return "", usage("reading the token from stdin: %v", err)
		}
		token = string(bytes)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", usage("no token on stdin — paste it at the prompt, or pipe it on stdin. %s", tokenSource)
	}
	return token, nil
}

func (a *app) logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout [--profile <name>]",
		Short: "Forget a profile (spun.ink unless named) and its stored token",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			name := a.profile
			if name == "" {
				name = defaultProfile
			}
			config, err := loadConfig()
			if err != nil {
				return err
			}
			profile, ok := config.Profiles[name]
			if !ok {
				return fail(exitConfig, "config_missing", fmt.Sprintf("no profile named %q", name))
			}
			if profile.Credential == "" {
				forgetUnboundToken(name)
			} else if err := deleteToken(profile.Credential, profile.Store); err != nil {
				return err
			}
			delete(config.Profiles, name)
			if err := config.save(); err != nil {
				return err
			}
			return a.emit(map[string]any{"ok": true, "profile": name, "removed": true})
		},
	}
}

func (a *app) profilesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "profiles",
		Short: "List stored profiles (name and server, never the token)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			config, err := loadConfig()
			if err != nil {
				return err
			}
			rows := []any{}
			for _, name := range config.names() {
				rows = append(rows, map[string]any{"name": name, "url": config.Profiles[name].URL})
			}
			return a.emit(rows)
		},
	}
}
