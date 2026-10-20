package checks

import (
	"fmt"
	"regexp"
)

// PINNED TOOLS COME FROM THEIR OWN URL. A pinned release asset is fetched from
// the address tools.toml names, held to the sha256 beside it: the engine (or the
// executor pod) verifies the bytes at fetch time, so what the host serves is
// never trusted. The fleet's transparent cache answers the common hosts
// (github's release assets, static.rust-lang.org, nodejs.org, ...) and serves a
// cached entry stale on an upstream 5xx for 30 days, which is what the retired
// tool mirror (registry.notusmi.com/foundry/tool/mirror, `just mirror-seed`) was
// added to cover; a second copy to seed and keep in step bought nothing the cache
// did not. The blobs already in zot are left where they are.

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ToolSources answers the URLs that serve the asset pinned for url, in the order
// they must be tried, and the sha256 every one of them is verified against: the
// asset's own URL alone. A URL with no pin, or a pin that is not a sha256, gets
// an error and no source: the fetch fails closed rather than reach an address
// nothing vouches for.
func ToolSources(url string, sums map[string]string) (sources []string, sum string, err error) {
	sum, ok := sums[url]
	if !ok {
		return nil, "", fmt.Errorf("%s has no checksum in checks.ToolSHA256", url)
	}
	if !sha256Hex.MatchString(sum) {
		return nil, "", fmt.Errorf("%s is pinned to %q, which is not a sha256", url, sum)
	}
	return []string{url}, sum, nil
}
