package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zalando/go-keyring"
)

const keyringService = "spun"

// Profile is a named server. The token lives apart from it — in the OS keyring, or in
// credentials.json when there is none — so config.json never holds a secret. Credential is the key
// the token is stored under, and Store names which of the two holds it, so a stale copy in the other
// can never answer instead.
type Profile struct {
	URL        string `json:"url"`
	Store      string `json:"store,omitempty"`
	Credential string `json:"credential,omitempty"`
}

type Config struct {
	Profiles map[string]Profile `json:"profiles"`
}

func configDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "spun"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fail(exitConfig, "config_missing", "no home directory: "+err.Error())
	}
	return filepath.Join(home, ".config", "spun"), nil
}

func readJSON(name string, into any) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err == nil {
		err = json.Unmarshal(data, into)
	}
	if err != nil {
		return fail(exitConfig, "config_invalid", fmt.Sprintf("%s: %v", filepath.Join(dir, name), err))
	}
	return nil
}

// writeJSON writes owner-only: credentials.json carries tokens, and config.json names the servers.
func writeJSON(name string, value any) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail(exitConfig, "config_unwritable", err.Error())
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fail(exitConfig, "config_unwritable", err.Error())
	}
	if err := writeFile(filepath.Join(dir, name), append(marshalJSON(value), '\n'), 0o600); err != nil {
		return fail(exitConfig, "config_unwritable", err.Error())
	}
	return nil
}

// writeFile is a var so a test can make a write fail.
var writeFile = writeAtomic

// writeAtomic replaces path with a file that never had looser permissions: a crash leaves the old
// file whole, and a symlink at path is replaced, never written through.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func loadConfig() (*Config, error) {
	config := &Config{}
	if err := readJSON("config.json", config); err != nil {
		return nil, err
	}
	if config.Profiles == nil {
		config.Profiles = map[string]Profile{}
	}
	return config, nil
}

func (c *Config) save() error { return writeJSON("config.json", c) }

func (c *Config) names() []string {
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func loadFileTokens() (map[string]string, error) {
	tokens := map[string]string{}
	return tokens, readJSON("credentials.json", &tokens)
}

// credentialKey is where a login stores its token: the server root and the profile, never the
// profile alone. Reassigning a profile to another server writes a new key, so a token can only ever
// be read back for the server it was stored for; the profile is in it because two accounts may share
// one server.
func credentialKey(root, profile string) string { return root + "#" + profile }

// storeToken prefers the keyring. Outside Windows it falls back to the file (a headless Linux box has
// no secret service); on Windows a 0600 file is not private, so a keyring failure is an error. It
// answers where the token went, so login can record and say so; the other store loses any copy.
func storeToken(key, token string) (string, error) {
	tokens, err := loadFileTokens()
	if err != nil {
		return "", err
	}
	keyringErr := keyring.Set(keyringService, key, token)
	if keyringErr == nil {
		if _, stale := tokens[key]; stale {
			delete(tokens, key)
			return "keyring", writeJSON("credentials.json", tokens)
		}
		return "keyring", nil
	}
	if !fileFallbackAllowed {
		return "", fail(exitConfig, "keyring_unavailable", fmt.Sprintf(
			"the OS credential store refused the token (%v) — spun never keeps it in a plain file on this system", keyringErr))
	}
	_ = keyring.Delete(keyringService, key)
	tokens[key] = token
	return "file", writeJSON("credentials.json", tokens)
}

// profileToken reads a profile's token, and only under the key of the profile's own server. A
// profile without a credential predates server-bound keys and must log in again.
func profileToken(name string, profile Profile) (string, error) {
	root, err := serverRoot(profile.URL)
	if err != nil {
		return "", fail(exitConfig, "config_invalid", err.Error())
	}
	if profile.Credential == "" {
		return "", nil
	}
	if profile.Credential != credentialKey(root, name) {
		return "", fail(exitConfig, "config_invalid", fmt.Sprintf(
			"profile %q names a credential stored for another server — run `%s` again", name, loginFor(name, root)))
	}
	return loadToken(profile.Credential, profile.Store)
}

func loadToken(key, store string) (string, error) {
	if store == "file" {
		tokens, err := loadFileTokens()
		return tokens[key], err
	}
	token, err := keyring.Get(keyringService, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fail(exitConfig, "keyring_unavailable", fmt.Sprintf(
			"the token is in the OS keyring, which answered: %v", err))
	}
	return token, nil
}

// deleteToken reports a keyring that may still hold the token, so logout never claims a removal it
// did not make.
func deleteToken(key, store string) error {
	if store != "file" {
		err := keyring.Delete(keyringService, key)
		if err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return fail(exitConfig, "keyring_unavailable", fmt.Sprintf(
				"the token may still be in the OS keyring, which answered: %v", err))
		}
		return nil
	}
	tokens, err := loadFileTokens()
	if _, held := tokens[key]; err != nil || !held {
		return err
	}
	delete(tokens, key)
	return writeJSON("credentials.json", tokens)
}

// forgetUnboundToken removes, best effort, a token stored under the bare profile name by a login
// that predates server-bound keys.
func forgetUnboundToken(name string) {
	_ = keyring.Delete(keyringService, name)
	if tokens, err := loadFileTokens(); err == nil {
		if _, held := tokens[name]; held {
			delete(tokens, name)
			_ = writeJSON("credentials.json", tokens)
		}
	}
}

// serverRoot is the one check on a server URL, wherever it comes from: an http(s) origin plus an
// optional path, nothing a bearer token could leak through. Plain http is for a machine's own dev
// server only. A pasted `/mcp` is dropped, since every call appends it.
func serverRoot(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		if !strings.Contains(raw, "://") {
			return "", fmt.Errorf("%q is not an http(s) server root — did you mean https://%s?", raw, raw)
		}
		return "", fmt.Errorf("%q is not an http(s) server root", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("%q must be a bare server root, without credentials, query or fragment", raw)
	}
	if u.Scheme == "http" && !isLocalHost(u.Hostname()) {
		return "", fmt.Errorf("%q would send the token unencrypted — use https (plain http is only for localhost)", raw)
	}
	path := strings.TrimSuffix(strings.TrimRight(u.EscapedPath(), "/"), "/mcp")
	return origin(u) + strings.TrimRight(path, "/"), nil
}

// origin is scheme://host[:port] with the host lowercased and a default port dropped, so two
// spellings of one server compare equal.
func origin(u *url.URL) string {
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port == "" || (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		return u.Scheme + "://" + host
	}
	return u.Scheme + "://" + host + ":" + port
}

func isLocalHost(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func sameServer(a, b string) bool {
	rootA, errA := serverRoot(a)
	rootB, errB := serverRoot(b)
	return errA == nil && errB == nil && rootA == rootB
}

// The default server and the profile `spun login` stores it under. defaultServer is a var so tests
// can point it at a local server; the profile name is reserved for it.
const defaultProfile = "spun.ink"

var defaultServer = "https://spun.ink"

// resolve picks the server and token: --profile or SPUN_PROFILE, else SPUN_URL (which needs
// SPUN_TOKEN and never falls through to production), else spun.ink with SPUN_TOKEN or the stored
// spun.ink profile's token. A stored token only ever goes to its own profile's server — SPUN_URL
// cannot point it elsewhere.
func resolve(profileFlag string) (*Client, error) {
	name := profileFlag
	if name == "" {
		name = os.Getenv("SPUN_PROFILE")
	}
	server, token := os.Getenv("SPUN_URL"), os.Getenv("SPUN_TOKEN")

	var config *Config
	var err error
	if name != "" || server == "" {
		if config, err = loadConfig(); err != nil {
			return nil, err
		}
	}
	if name == "" && server == "" {
		if _, stored := config.Profiles[defaultProfile]; stored {
			name = defaultProfile
		} else if token != "" {
			server = defaultServer
		} else {
			return nil, fail(exitConfig, "config_missing", noServerNamed(config))
		}
	}

	if name != "" {
		profile, ok := config.Profiles[name]
		if !ok && name == defaultProfile {
			return nil, fail(exitConfig, "config_missing", noServerNamed(config))
		}
		if !ok {
			return nil, fail(exitConfig, "config_missing", fmt.Sprintf(
				"no profile named %q — create it with `spun login --profile %s --url <server>`", name, name))
		}
		if server != "" && !sameServer(server, profile.URL) {
			return nil, fail(exitConfig, "config_conflict", fmt.Sprintf(
				"SPUN_URL (%s) is not the server of profile %q (%s) — a stored token only goes to its own "+
					"server: unset SPUN_URL, or use SPUN_URL with SPUN_TOKEN and no profile", server, name, profile.URL))
		}
		server = profile.URL
		if token == "" {
			if token, err = profileToken(name, profile); err != nil {
				return nil, err
			}
		}
	}

	root, err := serverRoot(server)
	if err != nil {
		return nil, fail(exitConfig, "config_invalid", err.Error())
	}
	if token == "" {
		if name == "" {
			return nil, fail(exitUnauthorized, "unauthorized", "SPUN_URL needs SPUN_TOKEN beside it — or use a profile stored by `spun login`")
		}
		return nil, fail(exitUnauthorized, "unauthorized", fmt.Sprintf(
			"no token stored for profile %q — run `%s`", name, loginFor(name, root)))
	}
	return &Client{root, token}, nil
}

// noServerNamed fires only when nothing names a server and spun.ink has no stored profile; other
// stored profiles are listed, since naming them is not using one.
func noServerNamed(config *Config) string {
	if len(config.Profiles) == 0 {
		return "not logged in — run `spun signup` for a new account or `spun login` to paste your token, " +
			"or set SPUN_URL and SPUN_TOKEN"
	}
	return fmt.Sprintf("not logged in to %s — run `spun login`, or pass --profile <name> or set SPUN_PROFILE; "+
		"stored profiles: %s", defaultServer, strings.Join(config.names(), ", "))
}

// loginFor is the login command that stores a token for this profile and server.
func loginFor(name, root string) string {
	if name == defaultProfile && root == defaultServer {
		return "spun login"
	}
	return fmt.Sprintf("spun login --profile %s --url %s", name, root)
}
