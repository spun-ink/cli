package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// termsMetaKey names the terms sign_up carries in its tools/list _meta: the sentence the human
// accepts is the server's, never a copy in this binary.
const termsMetaKey = "ink.spun/terms"

// exitCodeSent: sign-up is half done — the code is in the owner's mail, and `spun signup --code`
// finishes it. Distinct from every failure, so an agent's shell can tell "ask the owner" from "broken".
const exitCodeSent = 6

// maxCodeAttempts bounds the prompt loop at a terminal: past it the code is probably not coming.
const maxCodeAttempts = 5

type signupDetails struct{ email, name, handle string }

func (a *app) signupCmd() *cobra.Command {
	var details signupDetails
	var server, code string
	var acceptTerms, noSetup bool
	cmd := &cobra.Command{
		Use:   "signup [--email <address>] [--name <name>] [--handle <handle>]  |  signup --code <code>",
		Short: "Create a new spun.ink account and store its token (asks for what is missing)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			profile := a.profile
			if profile == "" {
				profile = defaultProfile
			}
			continuing := cmd.Flags().Changed("code")
			var pending pendingSignup
			if continuing {
				if details.email != "" {
					return usage("--code continues the sign-up already started; it takes no --email")
				}
				var err error
				if pending, err = loadPending(profile); err != nil {
					return err
				}
				if server == "" {
					server = pending.URL
				}
			}
			name, root, err := loginTarget(a.profile, server)
			if err != nil {
				return err
			}
			// Checked before anything is sent: a second sign_up is a second, separate account.
			config, err := loadConfig()
			if err != nil {
				return err
			}
			if _, stored := config.Profiles[name]; stored {
				return fail(exitConfig, "config_conflict", fmt.Sprintf(
					"profile %q already holds an account's token, and signing up again creates a second, "+
						"separate account — run `spun logout%s` first, or pass --profile <new name>", name, profileFlag(name)))
			}
			c := &Client{root, ""}
			if continuing {
				if !sameServer(pending.URL, root) {
					return usage("the sign-up pending for profile %q was started on %s, not %s", name, pending.URL, root)
				}
				fields, err := c.completeSignup(name, map[string]any{"signup": pending.Signup, "code": normalizeCode(code)}, details)
				if err != nil {
					return err
				}
				return a.storeSignup(name, root, pending.Email, fields, noSetup)
			}

			interactive := a.tty || isTerminal(a.stdin)
			input := bufio.NewReader(a.stdin)
			if err := details.complete(input, interactive); err != nil {
				return err
			}

			terms, err := c.signUpTerms()
			if err != nil {
				return err
			}
			notice, _ := terms["notice"].(string)
			fmt.Fprintln(os.Stderr, notice)
			if interactive {
				fmt.Fprintln(os.Stderr, "Entering the code we mail you accepts these terms; the mail repeats them.")
				if !confirm(input, "Send the code to "+details.email+"? [y/N] ") {
					return usage("not signed up — nothing was sent")
				}
			} else if !acceptTerms {
				return usage("not signed up: show your human this sentence, verbatim, and pass --accept-terms "+
					"once they agree; the code they then read from their mail is their acceptance — %s", notice)
			}

			args := map[string]any{"email": details.email}
			if details.name != "" {
				args["name"] = details.name
			}
			if details.handle != "" {
				args["handle"] = details.handle
			}
			result, err := c.call("sign_up", args)
			if err != nil {
				return signupFailure(err, name, "")
			}
			fields, _ := result.(map[string]any)
			if token, _ := fields["bearer_token"].(string); token != "" {
				// A server from before the code switch: one call, the token comes back at once.
				return a.storeSignup(name, root, details.email, fields, noSetup)
			}
			handle, _ := fields["signup"].(string)
			if fields["status"] != "code_sent" || handle == "" {
				return fail(exitNetwork, "bad_response", "sign_up returned neither a code_sent status nor a bearer_token")
			}
			pending = pendingSignup{Signup: handle, URL: root, Email: details.email,
				Expires: time.Now().Add(time.Duration(numberOr(fields["signup_expires_in"], 900)) * time.Second).Unix()}
			if err := savePending(name, pending); err != nil {
				return err
			}

			if !interactive {
				return a.reportCodeSent(name, root, server, fields, pending)
			}
			fmt.Fprintf(os.Stderr, "We sent a code to %s — it is good for %d minutes.\n",
				details.email, numberOr(fields["code_expires_in"], 600)/60)
			done, err := a.completeAtTerminal(c, name, input, pending, details)
			if err != nil {
				return err
			}
			return a.storeSignup(name, root, details.email, done, noSetup)
		},
	}
	cmd.Flags().StringVar(&details.email, "email", "", "owner email; where the account is reached")
	cmd.Flags().StringVar(&details.name, "name", "", "display name for the account and its first site")
	cmd.Flags().StringVar(&details.handle, "handle", "", "the first site's subdomain label (derived from --name if blank)")
	cmd.Flags().StringVar(&code, "code", "", "the code from the sign-up mail: finishes the sign-up already started (with --name/--handle to correct them)")
	cmd.Flags().StringVar(&server, "url", "", "a dev server's root, e.g. http://spun.localhost:3002 (with --profile)")
	cmd.Flags().BoolVar(&acceptTerms, "accept-terms", false, "your human has read and accepted the terms (required without a terminal)")
	cmd.Flags().BoolVar(&noSetup, "no-setup", false, "do not install the spun skill for the agents found")
	return cmd
}

// pendingSignup is a sign-up whose code is out: the handle the server issued, and the server and
// profile it was issued for, so a continuation can reach nothing else.
type pendingSignup struct {
	Signup  string `json:"signup"`
	URL     string `json:"url"`
	Email   string `json:"email"`
	Expires int64  `json:"expires"`
}

const pendingFile = "signup-pending.json"

func readPending() (map[string]pendingSignup, error) {
	all := map[string]pendingSignup{}
	if err := readJSON(pendingFile, &all); err != nil {
		return nil, err
	}
	return all, nil
}

func savePending(profile string, p pendingSignup) error {
	all, err := readPending()
	if err != nil {
		return err
	}
	all[profile] = p
	return writeJSON(pendingFile, all)
}

func clearPending(profile string) {
	all, err := readPending()
	if err != nil {
		return
	}
	if _, held := all[profile]; held {
		delete(all, profile)
		_ = writeJSON(pendingFile, all)
	}
}

func loadPending(profile string) (pendingSignup, error) {
	all, err := readPending()
	if err != nil {
		return pendingSignup{}, err
	}
	p, ok := all[profile]
	if !ok {
		return p, usage("no sign-up is waiting for a code%s — run `spun signup` to start one", profileNote(profile))
	}
	if time.Now().Unix() > p.Expires {
		clearPending(profile)
		return p, usage("the sign-up for %s expired — run `spun signup` to start over", p.Email)
	}
	return p, nil
}

func profileNote(name string) string {
	if name == defaultProfile {
		return ""
	}
	return " for profile " + name
}

func numberOr(v any, fallback int) int {
	if n, ok := v.(float64); ok {
		return int(n)
	}
	return fallback
}

// normalizeCode turns "482 913" and "482-913" into "482913".
func normalizeCode(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, s)
}

// failCode is the error code of a Fail: a tool's ("invalid_code") or a JSON-RPC one ("-32002").
func failCode(err error) string {
	var f *Fail
	if !errors.As(err, &f) {
		return ""
	}
	body, _ := f.Body.(map[string]any)
	e, _ := body["error"].(map[string]any)
	if code, ok := e["code"]; ok {
		return fmt.Sprint(code)
	}
	return ""
}

func isRateLimit(err error) bool {
	code := failCode(err)
	return code == "-32002" || code == "http_error" && strings.Contains(asFail(err).message(), "HTTP 429")
}

// signupFailure finishes a refused sign_up call: a rate limit says to repeat the same call, and a
// refusal nothing can continue from clears the pending state. A wrong code or a refused name or
// handle keeps it — the row is still open.
func signupFailure(err error, profile, continueWith string) error {
	if isRateLimit(err) {
		msg := "rate limited — nothing was consumed; wait a little and run the same command again"
		if continueWith != "" {
			msg = "rate limited — nothing was consumed; wait a little and repeat `" + continueWith + "` (no new code)"
		}
		return fail(exitNetwork, "rate_limited", msg)
	}
	switch failCode(err) {
	case "terms_changed", "account_exists", "already_completed", "invalid_argument":
		clearPending(profile)
	}
	return err
}

// completeSignup is call 2, with the flags' corrections folded in.
func (c *Client) completeSignup(profile string, args map[string]any, d signupDetails) (map[string]any, error) {
	if d.name != "" {
		args["name"] = d.name
	}
	if d.handle != "" {
		args["handle"] = d.handle
	}
	result, err := c.call("sign_up", args)
	if err != nil {
		return nil, signupFailure(err, profile, "spun signup --code <code>"+profileFlag(profile))
	}
	fields, _ := result.(map[string]any)
	if token, _ := fields["bearer_token"].(string); token == "" {
		return nil, fail(exitNetwork, "bad_response", "sign_up returned no bearer_token")
	}
	return fields, nil
}

// completeAtTerminal asks for the code and finishes; a wrong code is asked again, and a refused
// name or handle is corrected at the prompt without a new code.
func (a *app) completeAtTerminal(c *Client, profile string, input *bufio.Reader, p pendingSignup, d signupDetails) (map[string]any, error) {
	proven := false
	for attempt := 0; attempt < maxCodeAttempts; attempt++ {
		args := map[string]any{"signup": p.Signup}
		if !proven {
			code, err := ask(input, "Code from the email: ")
			if err != nil {
				return nil, err
			}
			args["code"] = normalizeCode(code)
		}
		fields, err := c.completeSignup(profile, args, d)
		if err == nil {
			return fields, nil
		}
		switch failCode(err) {
		case "invalid_code":
			fmt.Fprintln(os.Stderr, asFail(err).message())
		case "validation_failed":
			fmt.Fprintln(os.Stderr, asFail(err).message())
			proven = true
			var askErr error
			if d.name, askErr = ask(input, "Name (Enter: from the email): "); askErr != nil {
				return nil, askErr
			}
			if d.handle, askErr = ask(input, "Site handle (Enter: from the name): "); askErr != nil {
				return nil, askErr
			}
		default:
			return nil, err
		}
	}
	return nil, usage("not signed up — the code was not accepted; the sign-up stays open for a few minutes: `spun signup --code <code>%s`", profileFlag(profile))
}

// reportCodeSent is the end of a sign-up run without a terminal: the machine-readable state on
// stdout, the way on on stderr, and a status of its own.
func (a *app) reportCodeSent(profile, root, urlFlag string, fields map[string]any, p pendingSignup) error {
	next := "spun signup --code <code>" + profileFlag(profile)
	if urlFlag != "" {
		next += " --url " + root
	}
	fmt.Fprintln(os.Stderr, "A code is on its way to "+p.Email+". Ask your human for it, then run: "+next+"  [--name <name>] [--handle <handle>]")
	a.exit = exitCodeSent
	return a.emit(map[string]any{
		"status": "code_sent", "email": p.Email, "profile": profile, "url": root,
		"code_expires_in": fields["code_expires_in"], "signup_expires_in": fields["signup_expires_in"],
		"next": next,
	})
}

// storeSignup keeps the token the server issued and reports; the email is verified by then.
func (a *app) storeSignup(name, root, email string, fields map[string]any, noSetup bool) error {
	token, _ := fields["bearer_token"].(string)
	where, err := storeProfile(name, root, token)
	if err != nil {
		return err
	}
	clearPending(name)

	// Built field by field: the payload carries the token, which is never printed.
	site, _ := fields["site"].(map[string]any)
	legal, _ := fields["legal"].(map[string]any)
	reply := map[string]any{
		"ok": true, "profile": name, "url": root, "token_stored_in": where,
		"email": email, "site": site["handle"], "site_url": site["url"],
		"terms_version": legal["terms_version"], "terms_content_hash": legal["terms_content_hash"],
	}
	lines := []string{"site     " + fmt.Sprint(site["url"]),
		fmt.Sprintf("terms    %v (%v)", legal["terms_version"], legal["terms_content_hash"])}
	return a.reportStored(reply, "Signed up.", lines, nil, noSetup)
}

func profileFlag(name string) string {
	if name == defaultProfile {
		return ""
	}
	return " --profile " + name
}

// complete asks at a terminal for what the flags left out; without one, the email is required.
func (d *signupDetails) complete(input *bufio.Reader, interactive bool) error {
	if !interactive {
		if d.email == "" {
			return usage("missing --email — `spun signup --email <address> [--name <name>] [--handle <handle>] --accept-terms`")
		}
		return nil
	}
	askedAny := d.email == ""
	for d.email == "" {
		email, err := ask(input, "Email: ")
		if err != nil {
			return err
		}
		d.email = email
	}
	if !askedAny {
		return nil
	}
	var err error
	if d.name == "" {
		if d.name, err = ask(input, "Name (Enter: from the email): "); err != nil {
			return err
		}
	}
	if d.handle == "" {
		if d.handle, err = ask(input, "Site handle (Enter: from the name): "); err != nil {
			return err
		}
	}
	return nil
}

func ask(input *bufio.Reader, prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	line, err := input.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		fmt.Fprintln(os.Stderr)
		return "", usage("not signed up — the input ended")
	}
	return strings.TrimSpace(line), nil
}

func confirm(input *bufio.Reader, prompt string) bool {
	answer, err := ask(input, prompt)
	answer = strings.ToLower(answer)
	return err == nil && (answer == "y" || answer == "yes")
}

// signUpTerms reads the terms from the tokenless tools/list, which lists sign_up plus whatever
// tools an account-wide OAuth grant can call; it finds sign_up by name.
func (c *Client) signUpTerms() (map[string]any, error) {
	result, err := c.rpc("tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	tools, _ := result["tools"].([]any)
	for _, entry := range tools {
		tool, _ := entry.(map[string]any)
		if tool["name"] != "sign_up" {
			continue
		}
		meta, _ := tool["_meta"].(map[string]any)
		terms, _ := meta[termsMetaKey].(map[string]any)
		if notice, _ := terms["notice"].(string); notice != "" {
			return terms, nil
		}
	}
	return nil, fail(exitNetwork, "bad_response", c.URL+" does not publish the sign-up terms — "+
		"nothing was sent; sign up at "+c.URL+"/signup instead")
}
