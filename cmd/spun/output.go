package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

const (
	exitToolError    = 1
	exitUsage        = 2
	exitUnauthorized = 3
	exitNetwork      = 4
	exitConfig       = 5
)

// Fail is every error the CLI reports: an exit code and the structured body printed to stderr.
type Fail struct {
	Code int
	Body any
}

func (f *Fail) Error() string { return fmt.Sprint(f.Body) }

// message is the human sentence of a Fail the CLI raised itself.
func (f *Fail) message() string {
	if body, ok := f.Body.(map[string]any); ok {
		if e, ok := body["error"].(map[string]any); ok {
			if m, ok := e["message"].(string); ok {
				return m
			}
		}
	}
	return f.Error()
}

func fail(code int, kind, message string) *Fail {
	return &Fail{code, map[string]any{"ok": false, "error": map[string]any{"code": kind, "message": message}}}
}

func usage(format string, a ...any) *Fail {
	return fail(exitUsage, "usage", fmt.Sprintf(format, a...))
}

// asFail turns any error into a Fail. Everything the CLI raises itself is already one, so a plain
// error can only be cobra rejecting the command line.
func asFail(err error) *Fail {
	var f *Fail
	if errors.As(err, &f) {
		return f
	}
	return usage("%s — run `spun help`", err.Error())
}

// raw is output written byte for byte, without the trailing newline every other value gets:
// template pull feeds a file that is pushed back, and an added newline would change the markup.
type raw string

// toolTable is `spun tools`: aligned rows at a terminal, JSON when piped.
type toolTable []toolRow

type toolRow struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

func render(value any, tty bool) string {
	switch v := value.(type) {
	case raw:
		return string(v)
	case string:
		if tty {
			return v
		}
	case toolTable:
		if tty {
			lines := make([]string, len(v))
			for i, row := range v {
				lines[i] = fmt.Sprintf("%-26s %s", row.Name, row.Summary)
			}
			return strings.Join(lines, "\n")
		}
	}
	return encodeJSON(value, tty)
}

// encodeJSON is the one JSON writer: Go escapes < > & as < by default, which doubles markup's
// size and makes it unreadable to the agent.
func encodeJSON(value any, indent bool) string {
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if indent {
		encoder.SetIndent("", "  ")
	}
	_ = encoder.Encode(value)
	return strings.TrimSuffix(out.String(), "\n")
}

func marshalJSON(value any) []byte {
	return []byte(encodeJSON(value, false))
}
