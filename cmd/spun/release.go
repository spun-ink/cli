package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
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
