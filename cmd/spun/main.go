// Command spun is a thin MCP client over POST /mcp. Every command is one JSON-RPC call (or two, for
// an upload); the server stays the only place that knows what a tool does.
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	a := newApp()
	code := report(a.run(os.Args[1:]))
	a.afterCommand()
	os.Exit(code)
}

// report writes the command's value to stdout, or its Fail to stderr, and answers the exit code.
func report(value any, err error) int {
	if err != nil {
		f := asFail(err)
		fmt.Fprintln(os.Stderr, render(f.Body, isTerminal(os.Stderr)))
		return f.Code
	}
	if value == nil {
		return 0 // cobra printed help or the version itself
	}
	out := render(value, isTerminal(os.Stdout))
	if _, exact := value.(raw); !exact && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	// A full disk or a closed pipe must not pass for success: `pull > file && push` relies on it.
	if _, err := fmt.Print(out); err != nil {
		fmt.Fprintln(os.Stderr, render(fail(exitNetwork, "output", err.Error()).Body, isTerminal(os.Stderr)))
		return exitNetwork
	}
	return 0
}
