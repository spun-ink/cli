package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func (a *app) loginCmd() *cobra.Command {
	var withToken, noSetup bool
	var server, email string
	cmd := &cobra.Command{
		Use:   "login [--email <address>] [--profile <name> --url <server>]",
		Short: "Sign this machine in to spun.ink (or a named server) with a mailed code, and set up your agents",
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
			var token string
			if (a.tty || isTerminal(a.stdin)) && !withToken {
				token, err = a.signInByCode(&Client{root, ""}, name, email)
			} else {
				token, err = a.readToken()
			}
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
	cmd.Flags().StringVar(&email, "email", "", "the account owner's address, where the sign-in code goes (asked if blank)")
	cmd.Flags().BoolVar(&withToken, "token", false, "paste an account or operator token at the prompt instead of signing in with a code")
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
		if start, ok := agentStart(row); ok && row.Action != "skipped" {
			started = append(started, start)
		}
	}
	if len(started) == 0 {
		next = append(next, "spun setup claude                # or codex: install the spun skill for your agent")
		started = []string{"ask your agent: " + firstPrompt}
	} else {
		next = append(next, "in your project folder:")
	}
	next = append(next, started...)
	return append(next, "or try `spun tools` yourself")
}

// firstPrompt is the first message on every way in, in the words of https://spun.ink/start.
const firstPrompt = "Build my website on spun.ink using what you already know about this project. Ask only for " +
	"missing essentials. Show me a working draft preview and wait for my approval before publishing."

// agentStart opens the agent on the spun skill with the first prompt: by its name `spun` where setup
// installed it, as `spun:spun` where the plugin brings it. Codex's `$` is single-quoted so the shell
// does not expand it.
func agentStart(row skillRow) (string, bool) {
	skill := "spun"
	if row.Action == "plugin" {
		skill = "spun:spun"
	}
	switch row.Agent {
	case "claude":
		return `claude "/` + skill + " " + firstPrompt + `"`, true
	case "codex":
		return `codex '$` + skill + " " + firstPrompt + `'`, true
	}
	return "", false
}

const loginExample = "spun login"

const tokenSource = "The owner signs in with `spun login` at their own terminal, with a code from their mail; " +
	"`spun signup` creates an account. A script pipes an operator token on stdin. A lost token is replaced at https://spun.ink/recover."

func tokenNotAnArgument() error {
	return usage("the token is never an argument (it would land in shell history) — run `%s` "+
		"and paste it at the prompt, or pipe it on stdin", loginExample)
}

// readToken takes the token from a pipe, or prompts without echo at a terminal (`--token`). It never
// prompts without one: an agent's shell has no human to answer.
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
		return "", usage("no token on stdin — %s", tokenSource)
	}
	return token, nil
}

// signInByCode is `spun login` at a terminal: /cli/login mails the owner a code, and the code typed
// here returns this machine's own credential. The server answers an address that cannot sign in
// exactly like one that can, so the prompt says what a missing mail means.
func (a *app) signInByCode(c *Client, profile, email string) (string, error) {
	input := bufio.NewReader(a.stdin)
	for email == "" {
		var err error
		if email, err = ask(input, "Email: "); err != nil {
			return "", usage("not signed in — the input ended")
		}
	}
	again := "spun login" + profileFlag(profile)
	missing := fail(exitNetwork, "login_unavailable", c.URL+" does not offer sign-in by code — run `"+again+
		" --token` and paste a token at the prompt")
	fields, err := c.cliRequest(http.MethodPost, "/cli/login", map[string]any{"email": email}, missing)
	if err != nil {
		return "", signInFailure(err, again)
	}
	handle, _ := fields["login"].(string)
	if fields["status"] != "code_sent" || handle == "" {
		return "", fail(exitNetwork, "bad_response", "/cli/login did not answer with a code_sent status")
	}
	fmt.Fprintf(os.Stderr, "If %s has a spun.ink account, a sign-in code is on its way — it is good for %d minutes. "+
		"If no mail comes, this address has no account that can sign in: `spun signup` creates one.\n",
		email, numberOr(fields["code_expires_in"], 600)/60)

	args := map[string]any{"login": handle}
	if machine := machineName(); machine != "" {
		args["machine"] = machine
	}
	for wrong := 0; wrong < maxCodeAttempts; {
		code, err := ask(input, "Code from the email: ")
		if err != nil {
			return "", usage("not signed in — the input ended")
		}
		args["code"] = normalizeCode(code)
		fields, err := c.cliRequest(http.MethodPost, "/cli/login/complete", args, missing)
		if err == nil {
			token, _ := fields["bearer_token"].(string)
			if token == "" {
				return "", fail(exitNetwork, "bad_response", "/cli/login/complete returned no bearer_token")
			}
			return token, nil
		}
		if failCode(err) != "invalid_code" {
			return "", signInFailure(err, again)
		}
		wrong++
		fmt.Fprintln(os.Stderr, "That code did not work — check the newest mail from spun.ink.")
	}
	return "", usage("not signed in — five wrong codes end the sign-in: run `%s` again for a new code", again)
}

// signInFailure words a refused sign-in for the terminal. Nothing a refusal leaves behind can be
// continued: every way on is a new `spun login`, with a new code.
func signInFailure(err error, again string) error {
	if isBusy(err) {
		code := "rate_limited"
		if failCode(err) == "unavailable" {
			code = "unavailable"
		}
		return fail(exitNetwork, code, "the server is busy or rate limited — wait a minute, then run `"+again+"` again")
	}
	if failCode(err) == "already_completed" {
		return worded(err, "this sign-in already issued its credential, and a code is never reused — run `"+again+"` again for a new code")
	}
	return err
}

// machineName names this machine's credential in list_connections: the host name, cut to the
// server's 255 characters. Without one the server names it "spun CLI".
func machineName() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	if runes := []rune(strings.TrimSpace(name)); len(runes) > 255 {
		return string(runes[:255])
	}
	return strings.TrimSpace(name)
}

func (a *app) logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout [--profile <name>]",
		Short: "Sign this machine out of a profile (spun.ink unless named) and forget its stored credential",
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
			reply := map[string]any{"ok": true, "profile": name, "removed": true}
			if profile.Credential == "" {
				forgetUnboundToken(name)
				reply["server"], reply["note"] = "local_only", "a login from before server-bound credentials: only the local copy is deleted"
			} else {
				token, err := loadToken(profile.Credential, profile.Store)
				if err != nil {
					return err
				}
				if token != "" {
					var note string
					if reply["server"], note = signOut(&Client{profile.URL, token}); note != "" {
						reply["note"] = note
					}
				}
				if err := deleteToken(profile.Credential, profile.Store); err != nil {
					return err
				}
			}
			delete(config.Profiles, name)
			if err := config.save(); err != nil {
				return err
			}
			return a.emit(reply)
		},
	}
}

// signOut revokes the presenting credential on its server. The local copy goes whatever the answer,
// so this only reports what the server did: "signed_out", or "local_only" with why.
func signOut(c *Client) (string, string) {
	missing := fail(exitNetwork, "logout_unavailable", "")
	_, err := c.cliRequest(http.MethodPost, "/cli/logout", nil, missing)
	switch {
	case err == nil, failCode(err) == "unauthenticated":
		return "signed_out", ""
	case failCode(err) == "not_a_cli_credential":
		return "local_only", "this profile held an account or operator token, not a CLI sign-in, so only the local copy is " +
			"deleted and the token still works — replace an account token at " + c.URL + "/recover, end an operator token with revoke_operator_token"
	case err == missing:
		return "local_only", c.URL + " has no sign-out from the terminal, so only the local copy is deleted"
	}
	return "local_only", "the server did not confirm the sign-out (" + asFail(err).message() + "), so this machine's " +
		"sign-in may still be live there — end it with revoke_connection from a connected agent"
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
