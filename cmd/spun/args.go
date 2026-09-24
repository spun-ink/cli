package main

import (
	"encoding/json"
	"os"
	"strings"
)

// parseArgs reads key=value as a string, key:=json as raw JSON, key=@path as a file's contents and
// key:=@path as a JSON file. Only the first `=` separates, so a value may contain `=` and `:=`.
func parseArgs(pairs []string, site string) (map[string]any, error) {
	args := map[string]any{}
	if site != "" {
		args["site"] = site
	}
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		name, isJSON := strings.CutSuffix(key, ":")
		if !ok || name == "" {
			return nil, usage("expected key=value, got %q", pair)
		}
		if path, isFile := strings.CutPrefix(value, "@"); isFile {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, usage("%s: %v", path, err)
			}
			value = string(data)
		}
		if !isJSON {
			args[name] = value
			continue
		}
		var parsed any
		if err := json.Unmarshal([]byte(value), &parsed); err != nil {
			return nil, usage("%s:= is not JSON: %v", name, err)
		}
		args[name] = parsed
	}
	return args, nil
}
