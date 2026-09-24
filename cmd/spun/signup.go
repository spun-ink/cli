package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// termsMetaKey names the terms sign_up carries in its tools/list _meta: the sentence the human
// accepts is the server's, never a copy in this binary.
const termsMetaKey = "ink.spun/terms"

type signupDetails struct{ email, name, handle string }

func (a *app) signupCmd() *cobra.Command {
	var details signupDetails
	var server string
	var acceptTerms, noSetup bool
	cmd := &cobra.Command{
		Use:   "signup [--email <address>] [--name <name>] [--handle <handle>]",
		Short: "Create a new spun.ink account and store its token (asks for what is missing)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
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

			interactive := isTerminal(a.stdin)
			input := bufio.NewReader(a.stdin)
			if err := details.complete(input, interactive); err != nil {
				return err
			}

			c := &Client{root, ""}
			terms, err := c.signUpTerms()
			if err != nil {
				return err
			}
			notice, _ := terms["notice"].(string)
			fmt.Fprintln(os.Stderr, notice)
			if interactive {
				if !confirm(input, "Create the account and accept these terms? [y/N] ") {
					return usage("not signed up — nothing was sent")
				}
			} else if !acceptTerms {
				return usage("not signed up: show your human this sentence, verbatim, and pass --accept-terms "+
					"once they agree — %s", notice)
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
				return err
			}
			fields, _ := result.(map[string]any)
			token, _ := fields["bearer_token"].(string)
			if token == "" {
				return fail(exitNetwork, "bad_response", "sign_up returned no bearer_token")
			}
			where, err := storeProfile(name, root, token)
			if err != nil {
				return err
			}

			// Built field by field: the payload carries the token, which is never printed.
			site, _ := fields["site"].(map[string]any)
			legal, _ := fields["legal"].(map[string]any)
			reply := map[string]any{
				"ok": true, "profile": name, "url": root, "token_stored_in": where,
				"email": details.email, "site": site["handle"], "site_url": site["url"],
				"terms_version": legal["terms_version"], "terms_content_hash": legal["terms_content_hash"],
			}
			first := []string{"confirm the email we sent to " + details.email +
				" — pressing its button concludes the contract and puts your site online"}
			lines := []string{"site     " + fmt.Sprint(site["url"]),
				fmt.Sprintf("terms    %v (%v)", legal["terms_version"], legal["terms_content_hash"])}
			return a.reportStored(reply, "Signed up.", lines, first, noSetup)
		},
	}
	cmd.Flags().StringVar(&details.email, "email", "", "owner email; where the account is reached")
	cmd.Flags().StringVar(&details.name, "name", "", "display name for the account and its first site")
	cmd.Flags().StringVar(&details.handle, "handle", "", "the first site's subdomain label (derived from --name if blank)")
	cmd.Flags().StringVar(&server, "url", "", "a dev server's root, e.g. http://spun.localhost:3002 (with --profile)")
	cmd.Flags().BoolVar(&acceptTerms, "accept-terms", false, "your human has read and accepted the terms (required without a terminal)")
	cmd.Flags().BoolVar(&noSetup, "no-setup", false, "do not install the spun skill for the agents found")
	return cmd
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

// signUpTerms reads the terms from the tokenless tools/list, which lists sign_up alone.
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
