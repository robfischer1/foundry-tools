package pinsync

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// versionToken is a dotted version inside a literal: 1.261.0, 0.9.145, 3.14.6.
// x86_64 and manylinux_2_28 have no dot and are not one.
var versionToken = regexp.MustCompile(`\d+(?:\.\d+)+`)

// follow answers literal with its version moved to version. The literal must
// carry exactly one distinct version token (it may repeat, as a tag and an
// asset name do); anything else is a shape this command was not written for,
// and it says so rather than guessing which token is the version.
func follow(literal, version string) (string, bool, error) {
	want := strings.TrimPrefix(version, "v")
	if !versionToken.MatchString(want) || versionToken.FindString(want) != want {
		return "", false, fmt.Errorf("version %q is not dotted digits", version)
	}
	distinct := map[string]bool{}
	for _, tok := range versionToken.FindAllString(literal, -1) {
		distinct[tok] = true
	}
	if len(distinct) != 1 {
		return "", false, fmt.Errorf("%q carries %d distinct version tokens, not one", literal, len(distinct))
	}
	var old string
	for tok := range distinct {
		old = tok
	}
	if old == want {
		return literal, false, nil
	}
	return replaceToken(literal, old, want), true, nil
}

// replaceToken replaces old only where it stands as a whole token, so 1.6.0
// moving to 1.7.0 cannot touch 11.6.0 or 1.6.01.
func replaceToken(s, old, repl string) string {
	return versionToken.ReplaceAllStringFunc(s, func(tok string) string {
		if tok == old {
			return repl
		}
		return tok
	})
}

// releaseURL is a GitHub release asset URL, split.
type releaseURL struct {
	owner, repo, tag, asset string
}

var releaseRe = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/releases/download/(.+)/([^/]+)$`)

func parseRelease(u string) (releaseURL, bool) {
	m := releaseRe.FindStringSubmatch(u)
	if m == nil {
		return releaseURL{}, false
	}
	return releaseURL{owner: m[1], repo: m[2], tag: m[3], asset: m[4]}, true
}

func (r releaseURL) String() string {
	return "https://github.com/" + r.owner + "/" + r.repo + "/releases/download/" + r.tag + "/" + r.asset
}

// retag moves a URL whose release tag is NOT its version (python-build-
// standalone: tag 20260623 carries cpython-3.14.6+20260623) to the newest
// release that publishes the same asset at the new version. The asset name
// carries the tag too, so the old tag is replaced in both places.
func (e *Env) retag(ctx context.Context, u string) (string, error) {
	r, ok := parseRelease(u)
	if !ok {
		return "", fmt.Errorf("%s is not a GitHub release asset, so its tag cannot be resolved", u)
	}
	for page := 1; page <= releasePages; page++ {
		releases, err := e.releases(ctx, r.owner, r.repo, page)
		if err != nil {
			return "", err
		}
		for _, rel := range releases {
			name := unescapeAsset(strings.ReplaceAll(r.asset, r.tag, rel.Tag))
			for _, a := range rel.Assets {
				if a.Name == name {
					next := r
					next.tag = rel.Tag
					next.asset = strings.ReplaceAll(r.asset, r.tag, rel.Tag)
					return next.String(), nil
				}
			}
		}
	}
	return "", fmt.Errorf("none of the %d newest releases of %s/%s publishes %s at its tag", releasePages*releasesPerPage, r.owner, r.repo, unescapeAsset(r.asset))
}

// unescapeAsset turns a URL's asset segment into the asset's name (%2B is +).
func unescapeAsset(s string) string { return strings.ReplaceAll(s, "%2B", "+") }
