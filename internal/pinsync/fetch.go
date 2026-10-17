package pinsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
)

// release is the part of GitHub's release JSON this command reads.
type release struct {
	Tag    string  `json:"tag_name"`
	Assets []asset `json:"assets"`
}

type asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"` // "sha256:<hex>", GitHub's own measurement
}

// open GETs u and answers the body of a 200; any other answer is an error
// that names it. api marks a GitHub API call, which carries the token.
func (e *Env) open(ctx context.Context, u string, api bool) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if api {
		req.Header.Set("Accept", "application/vnd.github+json")
		if e.Token != "" {
			req.Header.Set("Authorization", "Bearer "+e.Token)
		}
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return resp.Body, nil
}

// read answers at most limit bytes of u's body.
func (e *Env) read(ctx context.Context, u string, api bool, limit int64) ([]byte, error) {
	body, err := e.open(ctx, u, api)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(io.LimitReader(body, limit))
}

// getJSON decodes a GitHub API answer.
func (e *Env) getJSON(ctx context.Context, u string, into any) error {
	b, err := e.read(ctx, u, true, 64<<20)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, into); err != nil {
		return fmt.Errorf("decoding %s: %w", u, err)
	}
	return nil
}

// releasePages and releasesPerPage bound the search for a release: newest
// first, five to a page because a python-build-standalone release lists ~900
// assets (~2 MB of JSON each) and GitHub answers 504 for a page of thirty.
const releasePages, releasesPerPage = 6, 5

// releases answers one page of a repository's releases, newest first.
func (e *Env) releases(ctx context.Context, owner, repo string, page int) ([]release, error) {
	var out []release
	err := e.getJSON(ctx, fmt.Sprintf("%s/repos/%s/%s/releases?per_page=%d&page=%d", e.GitHubAPI, owner, repo, releasesPerPage, page), &out)
	return out, err
}

// sumOf answers the sha256 of the bytes the URL serves, then holds it to
// every checksum the publisher states for that asset: a sidecar `<url>.sha256`,
// a SHA256SUMS / checksums file in the same GitHub release, and GitHub's own
// digest of the asset. One that disagrees is an error and nothing is written;
// a publisher who states none leaves the measured sum standing, which is how
// the hand-written lines were made.
func (e *Env) sumOf(ctx context.Context, u string) (string, error) {
	body, err := e.open(ctx, u, false)
	if err != nil {
		return "", err
	}
	defer body.Close()
	h := sha256.New()
	if _, err := io.Copy(h, body); err != nil {
		return "", fmt.Errorf("reading %s: %w", u, err)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	for from, want := range e.statedSums(ctx, u) {
		if want != sum {
			return "", fmt.Errorf("%s measures %s but %s states %s", u, sum, from, want)
		}
		fmt.Fprintf(e.Log, "pinsync: %s agrees with %s\n", path.Base(u), from)
	}
	return sum, nil
}

var (
	hex64      = regexp.MustCompile(`\b[0-9a-f]{64}\b`)
	sumsAsset  = regexp.MustCompile(`(?i)(sha256|checksum|sums)`)
	notASumSet = regexp.MustCompile(`(?i)\.(sig|pem|asc|sigstore|bundle|cert|json|intoto\.jsonl)$`)
)

// statedSums answers each checksum the publisher states for the asset, by
// where it was read. A statement that cannot be read is logged and left out:
// the measured sum is still the asset's, and these only hold it to account.
func (e *Env) statedSums(ctx context.Context, u string) map[string]string {
	stated := map[string]string{}
	if b, err := e.read(ctx, u+".sha256", false, 4096); err == nil && hex64.Match(b) {
		stated[path.Base(u)+".sha256"] = hex64.FindString(string(b))
	}
	r, ok := parseRelease(u)
	if !ok {
		return stated
	}
	var rel release
	if err := e.getJSON(ctx, e.GitHubAPI+"/repos/"+r.owner+"/"+r.repo+"/releases/tags/"+r.tag, &rel); err != nil {
		fmt.Fprintf(e.Log, "pinsync: %s: could not read the release (%v); the checksums it publishes go unchecked\n", u, err)
		return stated
	}
	name := unescapeAsset(r.asset)
	for _, a := range rel.Assets {
		switch {
		case a.Name == name && strings.HasPrefix(a.Digest, "sha256:"):
			stated["GitHub's digest of "+name] = strings.TrimPrefix(a.Digest, "sha256:")
		case a.Name != name && sumsAsset.MatchString(a.Name) && !notASumSet.MatchString(a.Name):
			b, err := e.read(ctx, a.URL, false, 4<<20)
			if err != nil {
				fmt.Fprintf(e.Log, "pinsync: %s: %v\n", a.Name, err)
			} else if sum := sumsLine(b, name); sum != "" {
				stated[a.Name] = sum
			}
		}
	}
	return stated
}

// sumsLine answers the `<sum>  <name>` line for name in a checksums file, or
// "" when the file does not list it (a per-asset .sha256 for another asset).
func sumsLine(b []byte, name string) string {
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && len(fields[0]) == 64 && hex64.MatchString(fields[0]) && path.Base(strings.TrimPrefix(fields[len(fields)-1], "*")) == name {
			return fields[0]
		}
	}
	return ""
}
