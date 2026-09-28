package main

import (
	"encoding/json"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

// withSite adds the --site selector to arguments a command built itself.
func (a *app) withSite(args map[string]any) map[string]any {
	if a.site != "" {
		args["site"] = a.site
	}
	return args
}

func (a *app) toolsCmd() *cobra.Command {
	var refresh bool
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "List the server's tools (cached per server and token)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			tools, err := c.tools(refresh)
			if err != nil {
				return err
			}
			rows := make(toolTable, 0, len(tools))
			for _, t := range tools {
				tool, _ := t.(map[string]any)
				name, _ := tool["name"].(string)
				description, _ := tool["description"].(string)
				rows = append(rows, toolRow{name, summary(description)})
			}
			return a.emit(rows)
		},
	}
	cmd.Flags().BoolVar(&refresh, "refresh", false, "re-read tools/list instead of the cache")
	return cmd
}

// helpCmd replaces cobra's: `spun help <command>` is the CLI's own help, `spun help <tool>` is one
// tool's description and input schema from the server.
func (a *app) helpCmd(root *cobra.Command) *cobra.Command {
	var refresh bool
	cmd := &cobra.Command{
		Use:   "help [command | tool]",
		Short: "Help for a command, or one tool's description and input schema",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			a.quiet = true
			if len(args) == 0 {
				return root.Help()
			}
			if cmd, _, err := root.Find(args); err == nil && cmd != root {
				return cmd.Help()
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			tools, err := c.tools(refresh)
			if err != nil {
				return err
			}
			for _, t := range tools {
				if tool, _ := t.(map[string]any); tool["name"] == args[0] {
					return a.emit(tool)
				}
			}
			return usage("no tool named %q — run `spun tools` (or `spun tools --refresh` after a deploy)", args[0])
		},
	}
	cmd.Flags().BoolVar(&refresh, "refresh", false, "re-read tools/list instead of the cache")
	return cmd
}

func (a *app) callCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "call <tool> [key=value | key:=json | key=@path ...]",
		Short: "Call any tool",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			toolArgs, err := parseArgs(args[1:], a.site)
			if err != nil {
				return err
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			result, err := c.call(args[0], toolArgs)
			if err != nil {
				return err
			}
			return a.emit(result)
		},
	}
}

func (a *app) assetCmd() *cobra.Command {
	var alt, title string
	upload := &cobra.Command{
		Use:   "upload <path>",
		Short: "Upload a local file; its bytes never pass through the agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			asset, err := c.upload(args[0], alt, title, a.site)
			if err != nil {
				return err
			}
			return a.emit(asset)
		},
	}
	upload.Flags().StringVar(&alt, "alt", "", "alt text for the asset")
	upload.Flags().StringVar(&title, "title", "", "title for the asset, shown by list_assets")
	cmd := &cobra.Command{Use: "asset", Short: "Upload assets"}
	cmd.AddCommand(upload)
	return cmd
}

func (a *app) templateCmd() *cobra.Command {
	var pullSchema, pushSchema string
	pull := &cobra.Command{
		Use:   "pull <key>",
		Short: "Print a template's markup, byte for byte; --schema also writes its schema to a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			template, err := c.call("get_template", a.withSite(map[string]any{"key": args[0]}))
			if err != nil {
				return err
			}
			// A reply without markup must not pass for an empty template: the caller is
			// redirecting stdout over the file it will push back.
			fields, _ := template.(map[string]any)
			markup, ok := fields["markup"].(string)
			if !ok {
				return fail(exitNetwork, "bad_response", "get_template replied without markup")
			}
			if pullSchema != "" {
				schema, _ := json.MarshalIndent(fields["schema"], "", "  ")
				if err := os.WriteFile(pullSchema, append(schema, '\n'), 0o644); err != nil {
					return fail(exitNetwork, "write_failed", err.Error())
				}
			}
			return a.emit(raw(markup))
		},
	}
	push := &cobra.Command{
		Use:   "push <key> <file>",
		Short: "Replace a template's markup from a file; --schema also replaces its schema",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			markup, err := os.ReadFile(args[1])
			if err != nil {
				return usage("%s: %v", args[1], err)
			}
			toolArgs := map[string]any{"key": args[0], "markup": string(markup)}
			if pushSchema != "" {
				schema, err := readSchema(pushSchema)
				if err != nil {
					return err
				}
				toolArgs["schema"] = schema
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			template, err := c.call("update_template", a.withSite(toolArgs))
			if err != nil {
				return err
			}
			// The caller holds the markup in the file it just pushed. A server that still echoes it
			// (an older server) would cost its size again; one that does not makes this a no-op.
			if fields, ok := template.(map[string]any); ok {
				delete(fields, "markup")
			}
			return a.emit(template)
		},
	}
	pull.Flags().StringVar(&pullSchema, "schema", "", "also write the template's schema, as JSON, to this file")
	push.Flags().StringVar(&pushSchema, "schema", "", "also replace the template's schema from this JSON file (a full replace)")
	cmd := &cobra.Command{Use: "template", Short: "Move template markup and schema between files and the site"}
	cmd.AddCommand(pull, push)
	return cmd
}

// readSchema accepts what `template pull --schema` writes: a JSON array of fields, or null for a
// template without one. Anything else is refused before a call is made.
func readSchema(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, usage("%s: %v", path, err)
	}
	var schema any
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, usage("%s: not JSON: %v", path, err)
	}
	if _, ok := schema.([]any); !ok && schema != nil {
		return nil, usage("%s: a schema is a JSON array of fields", path)
	}
	return schema, nil
}

func (a *app) contentCmd() *cobra.Command {
	var idsOnly bool
	var kind, status string
	list := &cobra.Command{
		Use:   "list",
		Short: "Pages and posts from site_map; --ids-only prints one slug per line",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if !slices.Contains([]string{"", "page", "post"}, kind) {
				return usage("--kind %q: page or post", kind)
			}
			if !slices.Contains([]string{"", "draft", "published"}, status) {
				return usage("--status %q: draft or published", status)
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			siteMap, err := c.call("site_map", a.withSite(map[string]any{}))
			if err != nil {
				return err
			}
			rows := contentRows(siteMap, kind, status)
			if !idsOnly {
				return a.emit(rows)
			}
			var slugs strings.Builder
			for _, row := range rows {
				slug, _ := row.(map[string]any)["slug"].(string)
				slugs.WriteString(slug + "\n")
			}
			return a.emit(raw(slugs.String()))
		},
	}
	list.Flags().BoolVar(&idsOnly, "ids-only", false, "print one slug per line, for xargs")
	list.Flags().StringVar(&kind, "kind", "", "page or post")
	list.Flags().StringVar(&status, "status", "", "draft or published")
	cmd := &cobra.Command{Use: "content", Short: "Pages and posts"}
	cmd.AddCommand(list)
	return cmd
}

// contentRows flattens site_map's pages and posts into one list, each row naming its kind.
func contentRows(siteMap any, kind, status string) []any {
	fields, _ := siteMap.(map[string]any)
	rows := []any{}
	for _, group := range []struct{ key, kind string }{{"pages", "page"}, {"posts", "post"}} {
		if kind != "" && kind != group.kind {
			continue
		}
		entries, _ := fields[group.key].([]any)
		for _, e := range entries {
			entry, ok := e.(map[string]any)
			if !ok || status != "" && entry["status"] != status {
				continue
			}
			row := map[string]any{"kind": group.kind}
			for k, v := range entry {
				row[k] = v
			}
			rows = append(rows, row)
		}
	}
	return rows
}
