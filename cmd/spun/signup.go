package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

// retiredSignupFlags are what `spun signup` took before sign-up moved to the browser. They stay
// accepted so no script breaks on a usage error.
var retiredSignupFlags = []string{"email", "name", "handle", "code", "accept-terms", "no-setup"}

func (a *app) signupCmd() *cobra.Command {
	var server string
	cmd := &cobra.Command{
		Use:   "signup",
		Short: "Print the sign-up page of a spun server and open it in the browser",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, root, err := loginTarget(a.profile, server)
			if err != nil {
				return err
			}
			for _, flag := range retiredSignupFlags {
				if cmd.Flags().Changed(flag) {
					fmt.Fprintln(os.Stderr, "Sign-up now happens in the browser: the sign-up flags are ignored.")
					break
				}
			}
			page := root + "/signup"
			fmt.Fprintln(a.stdout, "Sign up in the browser: "+page)
			if a.stdoutIsTerminal() {
				if err := openBrowser(page); err != nil {
					fmt.Fprintln(os.Stderr, "Could not open the browser — open the address above yourself.")
				}
			}
			fmt.Fprintln(a.stdout, "Then store your token: "+loginFor(name, root))
			return nil
		},
	}
	for _, flag := range retiredSignupFlags {
		if flag == "accept-terms" || flag == "no-setup" {
			cmd.Flags().Bool(flag, false, "ignored")
		} else {
			cmd.Flags().String(flag, "", "ignored")
		}
		_ = cmd.Flags().MarkHidden(flag)
	}
	cmd.Flags().StringVar(&server, "url", "", "a dev server's root, e.g. http://spun.localhost:3002 (with --profile)")
	return cmd
}

func (a *app) stdoutIsTerminal() bool {
	f, ok := a.stdout.(*os.File)
	return a.tty || ok && isTerminal(f)
}

// openBrowser is a var so tests never launch one.
var openBrowser = func(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
