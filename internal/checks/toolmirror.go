package checks

import (
	"fmt"
	"regexp"
)

// THE TOOL MIRROR: pinned release assets served from the fleet's own registry
// before GitHub is asked. zot (flux foundry/zot-config.json) answers anonymous
// reads on every repository, and an OCI blob is addressed by the sha256 of its
// bytes, so the blob URL of a pinned asset is derived from the pin itself:
// there is no second table to keep in step with ToolSHA256, and the digest
// the registry serves under is the digest the engine verifies the fetch
// against. Upstream stays the fallback (github.com answered 500 in runs 870
// and 907 of stellar-core-rust), verified against the same sum.
//
// Seeding is `just mirror-seed <url> <sha256>` (justfile): an `oras push` of
// the asset to tag sha256-<sum>, a tag zot's retention keeps forever (the
// `^sha256-[0-9a-f]{64}…$` pattern), so no pinned asset is ever reaped.
const (
	// ToolMirrorRepo is the one repository every mirrored asset lives in.
	ToolMirrorRepo = "foundry/tool/mirror"
	// ToolMirrorBlobBase is the URL prefix of a blob in that repository; the
	// pinned sum completes it.
	ToolMirrorBlobBase = "https://registry.notusmi.com/v2/foundry/tool/mirror/blobs/sha256:"
)

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ToolMirrorURL is where the mirror serves the asset whose sha256 is sum.
func ToolMirrorURL(sum string) string { return ToolMirrorBlobBase + sum }

// ToolSources answers, in the order they must be tried, the URLs that serve the
// asset pinned for url, and the sha256 every one of them is verified against:
// the mirror first, the asset's own URL last. A URL with no pin, or a pin that
// is not a sha256, gets an error and no source: the fetch fails closed rather
// than reach an address nothing vouches for.
func ToolSources(url string, sums map[string]string) (sources []string, sum string, err error) {
	sum, ok := sums[url]
	if !ok {
		return nil, "", fmt.Errorf("%s has no checksum in checks.ToolSHA256", url)
	}
	if !sha256Hex.MatchString(sum) {
		return nil, "", fmt.Errorf("%s is pinned to %q, which is not a sha256", url, sum)
	}
	return []string{ToolMirrorURL(sum), url}, sum, nil
}
