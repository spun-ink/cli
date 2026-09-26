package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// maxUpload is the server's asset cap (25 MB, as Rails counts it); a larger file is refused
	// before it is read into memory.
	maxUpload = 25 << 20
	// maxReply is the server's own request ceiling; no honest reply comes near it.
	maxReply = 40 << 20
)

// Uploads run up to the 25 MB asset cap, so the ceiling is generous; it only stops a hung server.
// No request follows a redirect: the bearer, or a file, would go to a server nobody named.
var httpClient = &http.Client{
	Timeout:       5 * time.Minute,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

type Client struct {
	URL   string
	Token string
}

func (c *Client) send(method, target string, body []byte, contentType string) (*http.Response, []byte, error) {
	req, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fail(exitConfig, "config_invalid", err.Error())
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "spun-cli/"+version)
	// The upload URL is signed; the bearer token goes only to /mcp. Only signup's client has none.
	if method == http.MethodPost {
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		req.Header.Set("Accept", "application/json, text/event-stream")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = fmt.Errorf("%s %s: %w", urlErr.Op, withoutQuery(urlErr.URL), urlErr.Err)
		}
		return nil, nil, fail(exitNetwork, "network", err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, nil, fail(exitNetwork, "redirected", fmt.Sprintf(
			"HTTP %d to %s — spun never follows a redirect; if that is the server, use its root instead",
			resp.StatusCode, withoutQuery(resp.Header.Get("Location"))))
	}
	text, err := io.ReadAll(io.LimitReader(resp.Body, maxReply+1))
	if err != nil {
		return nil, nil, fail(exitNetwork, "network", err.Error())
	}
	if len(text) > maxReply {
		return nil, nil, fail(exitNetwork, "bad_response", "the reply is larger than 40 MB")
	}
	return resp, text, nil
}

// withoutQuery keeps a signed URL's signature out of error messages.
func withoutQuery(raw string) string {
	before, _, _ := strings.Cut(raw, "?")
	return before
}

func (c *Client) rpc(method string, params any) (map[string]any, error) {
	body := marshalJSON(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	resp, text, err := c.send(http.MethodPost, c.URL+"/mcp", body, "application/json")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fail(exitUnauthorized, "unauthorized",
			"the server rejected the token — a lost or rotated one is replaced at "+c.URL+"/recover")
	}

	var reply map[string]any
	if json.Unmarshal(text, &reply) != nil {
		if resp.StatusCode >= 300 {
			return nil, fail(exitNetwork, "http_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, firstLine(text)))
		}
		return nil, fail(exitNetwork, "bad_response", "not JSON-RPC: "+firstLine(text))
	}
	if rpcErr, ok := reply["error"]; ok {
		return nil, &Fail{rpcErrorExit(rpcErr), map[string]any{"ok": false, "error": rpcErr}}
	}
	if resp.StatusCode >= 300 {
		return nil, fail(exitNetwork, "http_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, firstLine(text)))
	}
	result, ok := reply["result"].(map[string]any)
	if !ok {
		return nil, fail(exitNetwork, "bad_response", "the JSON-RPC reply carries neither a result nor an error")
	}
	return result, nil
}

// rpcErrorExit: an unknown method or tool and invalid params are the caller's mistake, so a retry
// cannot help; every other JSON-RPC error is the protocol failing.
func rpcErrorExit(rpcErr any) int {
	fields, _ := rpcErr.(map[string]any)
	switch fields["code"] {
	case float64(-32601), float64(-32602):
		return exitUsage
	}
	return exitNetwork
}

func (c *Client) call(tool string, args map[string]any) (any, error) {
	result, err := c.rpc("tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return nil, err
	}
	ok := false
	content, _ := result["content"].([]any)
	var text string
	if len(content) > 0 {
		item, _ := content[0].(map[string]any)
		text, ok = item["text"].(string)
	}
	if !ok {
		return nil, fail(exitNetwork, "bad_response", "the tools/call result has no text content")
	}
	var payload any
	if json.Unmarshal([]byte(text), &payload) != nil {
		payload = text
	}
	if isError, _ := result["isError"].(bool); isError {
		fields, structured := payload.(map[string]any)
		if !structured {
			return nil, fail(exitToolError, "tool_error", text)
		}
		if _, shaped := fields["error"]; !shaped {
			refusal := fail(exitToolError, "tool_error", "the tool refused without an error field")
			refusal.Body.(map[string]any)["error"].(map[string]any)["detail"] = fields
			return nil, refusal
		}
		return nil, &Fail{exitToolError, payload}
	}
	return payload, nil
}

func (c *Client) tools(refresh bool) ([]any, error) {
	path := c.cachePath()
	if !refresh && path != "" {
		var cached []any
		if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &cached) == nil {
			return cached, nil
		}
	}
	result, err := c.rpc("tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	tools, ok := result["tools"].([]any)
	if !ok {
		return nil, fail(exitNetwork, "bad_response", "the tools/list result has no tools")
	}
	if path != "" && os.MkdirAll(filepath.Dir(path), 0o700) == nil {
		_ = writeAtomic(path, marshalJSON(tools), 0o600)
	}
	return tools, nil
}

// cachePath is per server and per token: an operator token sees only the tools its scope allows,
// so two tokens on one server can have different lists. The token itself is never written, and
// without a per-user cache directory there is no cache.
func (c *Client) cachePath() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		var err error
		if base, err = os.UserCacheDir(); err != nil {
			return ""
		}
	}
	sum := sha256.Sum256([]byte(c.URL + "\n" + c.Token))
	return filepath.Join(base, "spun", "tools-"+hex.EncodeToString(sum[:12])+".json")
}

func (c *Client) upload(path, alt, title, site string) (any, error) {
	data, err := readUpload(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	args := map[string]any{"filename": filepath.Base(path), "sha256": hex.EncodeToString(sum[:])}
	if alt != "" {
		args["alt"] = alt
	}
	if title != "" {
		args["title"] = title
	}
	if site != "" {
		args["site"] = site
	}
	link, err := c.call("create_upload_link", args)
	if err != nil {
		return nil, err
	}
	fields, _ := link.(map[string]any)
	target, _ := fields["url"].(string)
	if target == "" {
		return nil, fail(exitNetwork, "bad_response", "no url in create_upload_link")
	}
	if !sameOrigin(target, c.URL) {
		return nil, fail(exitNetwork, "bad_response", fmt.Sprintf(
			"create_upload_link pointed at %s, not at %s — the file was not sent", withoutQuery(target), c.URL))
	}

	resp, text, err := c.send(http.MethodPut, target, data, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	var asset any
	if json.Unmarshal(text, &asset) != nil {
		asset = string(text)
	}
	_, structured := asset.(map[string]any)
	if resp.StatusCode >= 300 {
		if !structured {
			return nil, fail(exitToolError, "upload_refused", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, firstLine(text)))
		}
		return nil, &Fail{exitToolError, asset}
	}
	if !structured {
		return nil, fail(exitNetwork, "bad_response", "the upload reply is not a JSON object: "+firstLine(text))
	}
	return asset, nil
}

// readUpload reads a regular file up to the cap and no further: a device reports size 0, and a
// file can grow after it was checked.
func readUpload(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, usage("%s: %v", path, err)
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, usage("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxUpload+1))
	if err != nil {
		return nil, usage("%s: %v", path, err)
	}
	if len(data) > maxUpload {
		return nil, usage("%s is larger than 25 MB, the asset cap", path)
	}
	return data, nil
}

func sameOrigin(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	return errA == nil && errB == nil && origin(ua) == origin(ub)
}

func firstLine(b []byte) string {
	line, _, _ := strings.Cut(string(b), "\n")
	return line
}

func summary(description string) string {
	if first, _, ok := strings.Cut(description, ". "); ok {
		return first + "."
	}
	return description
}
