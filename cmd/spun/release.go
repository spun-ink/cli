package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
)

// releaseURL is where releases are published; a var so tests can serve their own.
var releaseURL = "https://github.com/spun-ink/cli"

// releaseClient talks to GitHub releases only, and never carries a token. Downloads redirect to
// GitHub's storage host; a redirect may not leave https.
var releaseClient = &http.Client{
	Timeout: 2 * time.Minute,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" && !isLocalHost(req.URL.Hostname()) {
			return fmt.Errorf("refusing a redirect to %s", req.URL.Redacted())
		}
		return nil
	},
}

func releaseRequest(ctx context.Context, method, target string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "spun-cli/"+version)
	client := releaseClient
	if method == http.MethodHead {
		plain := *releaseClient
		plain.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &plain
	}
	return client.Do(req)
}

// latestRelease is the version the releases/latest redirect names, as install.sh reads it: no
// api.github.com and its rate limit, and never a prerelease.
func latestRelease(ctx context.Context) (string, error) {
	resp, err := releaseRequest(ctx, http.MethodHead, releaseURL+"/releases/latest")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	location, err := resp.Location()
	if err != nil {
		return "", fmt.Errorf("no release found at %s/releases (%s)", releaseURL, resp.Status)
	}
	latest, ok := canonicalVersion(path.Base(location.Path))
	if !ok || path.Base(location.Path) != latest {
		return "", fmt.Errorf("no release found at %s/releases", releaseURL)
	}
	return latest, nil
}

// Download ceilings: a release archive is ~5 MB, and its binary ~10 MB.
const (
	maxArchive   = 64 << 20
	maxChecksums = 1 << 20
	maxBinary    = 128 << 20
)

// fetch reads a release asset into memory, refusing one larger than limit.
func fetch(ctx context.Context, target string, limit int64) ([]byte, error) {
	resp, err := releaseRequest(ctx, http.MethodGet, target)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", target, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d MB", target, limit>>20)
	}
	return data, nil
}

// checksumFor is name's SHA-256 in a checksums.txt, which must be `<64 hex>  <name>` on every line
// and list name exactly once.
func checksumFor(sums []byte, name string) (string, error) {
	var found string
	for i, line := range strings.Split(strings.TrimSuffix(string(sums), "\n"), "\n") {
		sum, file, ok := strings.Cut(strings.TrimSuffix(line, "\r"), "  ")
		if _, err := hex.DecodeString(sum); !ok || err != nil || len(sum) != 64 || file == "" || strings.ContainsAny(file, " \t") {
			return "", fmt.Errorf("checksums.txt line %d is malformed", i+1)
		}
		if file != name {
			continue
		}
		if found != "" {
			return "", fmt.Errorf("checksums.txt lists %s twice", name)
		}
		found = strings.ToLower(sum)
	}
	if found == "" {
		return "", fmt.Errorf("%s is not listed in checksums.txt", name)
	}
	return found, nil
}

// archiveEntry is one file in a release archive, whatever its format.
type archiveEntry struct {
	name    string
	regular bool // a plain file; anything else but a directory refuses the archive
	dir     bool
	size    int64
	open    func() (io.Reader, error)
}

// binaryFrom takes the one binary out of a release archive. Archives also carry LICENSE, README
// and SECURITY; an archive with a link, an absolute or escaping path, a duplicate, or not exactly
// one regular binary at its root is refused whole.
func binaryFrom(archive []byte, zipped bool, binary string) ([]byte, error) {
	entries, err := archiveEntries(archive, zipped, binary)
	if err != nil {
		return nil, fmt.Errorf("unreadable archive: %v", err)
	}
	seen := map[string]bool{}
	var found *archiveEntry
	var total int64
	for i, entry := range entries {
		name := path.Clean(entry.name)
		if entry.name == "" || strings.Contains(entry.name, `\`) || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return nil, fmt.Errorf("archive entry %q escapes the archive", entry.name)
		}
		if seen[name] {
			return nil, fmt.Errorf("archive lists %q twice", name)
		}
		seen[name] = true
		if entry.dir {
			continue
		}
		if !entry.regular {
			return nil, fmt.Errorf("archive entry %q is not a regular file", name)
		}
		if total += entry.size; entry.size < 0 || total > maxBinary {
			return nil, fmt.Errorf("archive unpacks to more than %d MB", maxBinary>>20)
		}
		if name == binary {
			found = &entries[i]
		}
	}
	if found == nil {
		return nil, fmt.Errorf("archive has no %s at its root", binary)
	}
	r, err := found.open()
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBinary+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != found.size {
		return nil, fmt.Errorf("%s is not the size the archive states", binary)
	}
	return data, nil
}

// archiveEntries lists an archive; a tarball, which cannot be reopened, keeps only binary's bytes,
// and stops reading once its entries claim more than the unpacked ceiling.
func archiveEntries(archive []byte, zipped bool, binary string) ([]archiveEntry, error) {
	var entries []archiveEntry
	if zipped {
		r, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range r.File {
			entries = append(entries, archiveEntry{
				name: f.Name, regular: f.Mode().IsRegular(), dir: f.Mode().IsDir(),
				size: int64(f.UncompressedSize64),
				open: func() (io.Reader, error) { return f.Open() },
			})
		}
		return entries, nil
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	var total int64
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return entries, nil
		}
		if err != nil {
			return nil, err
		}
		if total += max(header.Size, 0); total > maxBinary {
			return nil, fmt.Errorf("more than %d MB", maxBinary>>20)
		}
		var content []byte
		if header.Typeflag == tar.TypeReg && path.Clean(header.Name) == binary {
			if content, err = io.ReadAll(io.LimitReader(tr, header.Size)); err != nil {
				return nil, err
			}
		}
		entries = append(entries, archiveEntry{
			name: header.Name, regular: header.Typeflag == tar.TypeReg, dir: header.Typeflag == tar.TypeDir,
			size: header.Size,
			open: func() (io.Reader, error) { return bytes.NewReader(content), nil },
		})
	}
}
