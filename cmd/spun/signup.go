package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// maxCodeAttempts is the server's own: its fifth wrong code ends the sign-up. It also bounds the
// prompt loop at a terminal, corrections included.
const maxCodeAttempts = 5

// ownerOnly refuses a sign-up without a terminal: the code in the owner's mail proves their mailbox
// and accepts the Terms, so the owner types it at this prompt and never hands it to an agent.
const ownerOnly = "not signed up, nothing was sent — `spun signup` is the owner's: ask them to run it in their own " +
	"terminal, where it asks for the code from their mail. Never ask them for that code."

type signupDetails struct{ email, name, handle string }

func (a *app) signupCmd() *cobra.Command {
	var details signupDetails
	var server string
	var noSetup bool
	cmd := &cobra.Command{
		Use:   "signup [--email <address>] [--name <name>] [--handle <handle>]",
		Short: "Create a new spun.ink account at your own terminal and store its token (asks for what is missing)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !(a.tty || isTerminal(a.stdin)) {
				return fail(exitUsage, "owner_only", ownerOnly)
			}
			profile := a.profile
			if profile == "" {
				profile = defaultProfile
			}
			if cmd.Flags().Changed("accept-terms") || cmd.Flags().Changed("code") {
				return fail(exitUsage, "owner_only", "--accept-terms and --code are retired — run `spun signup`"+
					profileFlag(profile)+" without them: it continues an open sign-up and asks for the code")
			}
			pending, open := openSignup(profile)
			if open && server == "" {
				server = pending.URL
			}
			name, root, err := loginTarget(a.profile, server)
			if err != nil {
				return err
			}
			// Checked before anything is sent: a second sign-up is a second, separate account.
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
			input := bufio.NewReader(a.stdin)

			if open && sameServer(pending.URL, root) {
				code, err := ask(input, "A code went to "+pending.Email+" — enter it, or press Enter to start over: ")
				if err != nil {
					return err
				}
				if code != "" {
					done, err := a.completeAtTerminal(c, name, input, pending, details, code)
					if err != nil {
						return err
					}
					return a.storeSignup(name, root, pending.Email, done, noSetup)
				}
				clearPending(name)
			}

			if err := details.complete(input); err != nil {
				return err
			}
			terms, err := c.signUpTerms()
			if err != nil {
				return err
			}
			notice, _ := terms["notice"].(string)
			fmt.Fprintln(os.Stderr, notice)
			fmt.Fprintln(os.Stderr, "Entering the code we mail you accepts these terms; the mail repeats them.")
			if !confirm(input, "Send the code to "+details.email+"? [y/N] ") {
				return usage("not signed up — nothing was sent")
			}

			args := map[string]any{"email": details.email,
				"terms_version": terms["terms_version"], "terms_content_hash": terms["terms_content_hash"]}
			if details.name != "" {
				args["name"] = details.name
			}
			if details.handle != "" {
				args["handle"] = details.handle
			}
			fields, err := c.signupRequest(http.MethodPost, "/cli/signup", args)
			if err != nil {
				return signupFailure(err, name, "spun signup"+profileFlag(name)+" with the same flags")
			}
			handle, _ := fields["signup"].(string)
			if fields["status"] != "code_sent" || handle == "" {
				return fail(exitNetwork, "bad_response", "/cli/signup did not answer with a code_sent status")
			}
			pending = pendingSignup{Signup: handle, URL: root, Email: details.email,
				Expires: time.Now().Add(time.Duration(numberOr(fields["signup_expires_in"], 900)) * time.Second).Unix()}
			if err := savePending(name, pending); err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "We sent a code to %s — it is good for %d minutes.\n",
				details.email, numberOr(fields["code_expires_in"], 600)/60)
			done, err := a.completeAtTerminal(c, name, input, pending, details, "")
			if err != nil {
				return err
			}
			return a.storeSignup(name, root, details.email, done, noSetup)
		},
	}
	cmd.Flags().StringVar(&details.email, "email", "", "owner email; where the account is reached")
	cmd.Flags().StringVar(&details.name, "name", "", "display name for the account and its first site")
	cmd.Flags().StringVar(&details.handle, "handle", "", "the first site's subdomain label (derived from --name if blank)")
	cmd.Flags().StringVar(&server, "url", "", "a dev server's root, e.g. http://spun.localhost:3002 (with --profile)")
	cmd.Flags().BoolVar(&noSetup, "no-setup", false, "do not install the spun skill for the agents found")
	// The agent's half of the retired two-step sign-up, hidden for one release: an agent following an
	// older skill gets ownerOnly, not "unknown flag".
	cmd.Flags().Bool("accept-terms", false, "")
	cmd.Flags().String("code", "", "")
	_ = cmd.Flags().MarkHidden("accept-terms")
	_ = cmd.Flags().MarkHidden("code")
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

// openSignup is the sign-up for profile still waiting for its code; an expired one is dropped.
func openSignup(profile string) (pendingSignup, bool) {
	all, err := readPending()
	if err != nil {
		return pendingSignup{}, false
	}
	p, ok := all[profile]
	if ok && time.Now().Unix() > p.Expires {
		clearPending(profile)
		return p, false
	}
	return p, ok
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

// failCode is the error code of a Fail: the route's ("invalid_code") or the client's ("http_error").
func failCode(err error) string {
	if e := errorField(err); e != nil {
		if code, ok := e["code"]; ok {
			return fmt.Sprint(code)
		}
	}
	return ""
}

// errorField is the `error` object of a Fail's body: what the server's refusal carried.
func errorField(err error) map[string]any {
	var f *Fail
	if !errors.As(err, &f) {
		return nil
	}
	body, _ := f.Body.(map[string]any)
	e, _ := body["error"].(map[string]any)
	return e
}

// isBusy: the server declined this attempt and consumed nothing — a rate limit (a refusal or an
// HTTP 429) or a brief outage. The same call works later, with no new code.
func isBusy(err error) bool {
	switch failCode(err) {
	case "rate_limited", "unavailable":
		return true
	case "http_error":
		return strings.Contains(asFail(err).message(), "HTTP 429")
	}
	return false
}

// worded keeps a refusal's body and its code, and replaces the message by one that names a CLI
// command: the server's sentences describe the route, not the CLI.
func worded(err error, message string) *Fail {
	f := asFail(err)
	body, _ := f.Body.(map[string]any)
	e := map[string]any{}
	for k, v := range errorField(err) {
		e[k] = v
	}
	e["message"] = message
	out := map[string]any{}
	for k, v := range body {
		out[k] = v
	}
	out["error"] = e
	return &Fail{f.Code, out}
}

// signupFailure finishes a refused sign-up call. Busy answers say to repeat the same command, with
// no new code. Refusals nothing can continue from clear the pending state; a wrong code or a refused
// name or handle keeps it, because the row is still open. retry is the command that repeats the call.
func signupFailure(err error, profile, retry string) error {
	if isBusy(err) {
		what, code := "rate limited", "rate_limited"
		if failCode(err) == "unavailable" {
			what, code = "briefly unavailable", "unavailable"
		}
		return fail(exitNetwork, code, what+" — nothing was consumed and no new code is needed; wait a little and repeat `"+retry+"`")
	}
	switch failCode(err) {
	case "terms_changed":
		clearPending(profile)
		notice, _ := errorField(err)["notice"].(string)
		return worded(err, "The Terms of Service changed after the code was mailed, so the sign-up has ended and nothing was "+
			"created. The new terms: "+notice+" Run `spun signup"+profileFlag(profile)+"` again; a new mail names the new version.")
	case "account_exists", "already_completed", "invalid_argument":
		clearPending(profile)
	case "validation_failed":
		reasons, _ := errorField(err)["errors"].([]any)
		if len(reasons) == 0 {
			break
		}
		return worded(err, fmt.Sprint(reasons[0])+" No code was sent: run `spun signup` again with a different --name or --handle.")
	}
	return err
}

// completeSignup is call 2, POST /cli/signup/complete, with the flags' corrections folded in.
func (c *Client) completeSignup(profile string, args map[string]any, d signupDetails) (map[string]any, error) {
	if d.name != "" {
		args["name"] = d.name
	}
	if d.handle != "" {
		args["handle"] = d.handle
	}
	fields, err := c.signupRequest(http.MethodPost, "/cli/signup/complete", args)
	if err != nil {
		return nil, signupFailure(err, profile, "spun signup"+profileFlag(profile))
	}
	if token, _ := fields["bearer_token"].(string); token == "" {
		return nil, fail(exitNetwork, "bad_response", "/cli/signup/complete returned no bearer_token")
	}
	return fields, nil
}

// completeAtTerminal finishes with code, or asks for it when code is empty; a wrong code is asked
// again, and a refused name or handle is corrected at the prompt without a new code.
func (a *app) completeAtTerminal(c *Client, profile string, input *bufio.Reader, p pendingSignup, d signupDetails, code string) (map[string]any, error) {
	proven, wrong := false, 0
	for attempt := 0; attempt < maxCodeAttempts; attempt++ {
		args := map[string]any{"signup": p.Signup}
		if !proven {
			if code == "" {
				var err error
				if code, err = ask(input, "Code from the email: "); err != nil {
					return nil, err
				}
			}
			args["code"] = normalizeCode(code)
			code = ""
		}
		fields, err := c.completeSignup(profile, args, d)
		if err == nil {
			return fields, nil
		}
		switch failCode(err) {
		case "invalid_code":
			if wrong++; wrong == maxCodeAttempts {
				clearPending(profile)
				return nil, usage("not signed up — five wrong codes end the sign-up: run `spun signup%s` again for a new code", profileFlag(profile))
			}
			fmt.Fprintln(os.Stderr, "That code did not work — check the newest mail from spun.ink.")
		case "validation_failed":
			reasons, _ := errorField(err)["errors"].([]any)
			fmt.Fprintln(os.Stderr, fmt.Sprint(reasons...))
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
	return nil, usage("not signed up — the code was not accepted; the sign-up stays open for a few minutes: run `spun signup%s` again to continue it", profileFlag(profile))
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

// complete asks for what the flags left out.
func (d *signupDetails) complete(input *bufio.Reader) error {
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

// signUpTerms reads the terms from GET /cli/signup: the sentence the human accepts is the server's,
// never a copy in this binary, and its version and hash stamp the sign-up that follows.
func (c *Client) signUpTerms() (map[string]any, error) {
	terms, err := c.signupRequest(http.MethodGet, "/cli/signup", nil)
	if err != nil {
		return nil, err
	}
	if notice, _ := terms["notice"].(string); notice == "" {
		return nil, fail(exitNetwork, "bad_response", c.URL+" does not publish the sign-up terms — "+
			"nothing was sent; sign up at "+c.URL+"/signup instead")
	}
	return terms, nil
}
