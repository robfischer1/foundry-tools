package checks

import (
	"crypto/sha256"
	"encoding/hex"
)

// SourceCacheKey names the engine cache volume that holds one repository's
// warm git history for New's fetch (sourceAt). One volume per fetch URL: the
// history in it is one repository's, and two URLs never share objects.
//
// A DIGEST, NOT THE URL SPELLED OUT. A cache volume's key is an opaque string
// to the engine, and a URL carries ':' and '/' that read as structure to
// anything listing the engine's volumes; the digest is stable, fixed-width and
// says nothing a reader could mistake. The prefix says whose it is.
func SourceCacheKey(repo string) string {
	return "foundry-source-" + repoDigest(repo)
}

// repoDigest is the fixed-width, opaque spelling of a repository in a cache key.
func repoDigest(repo string) string {
	sum := sha256.Sum256([]byte(repo))
	return hex.EncodeToString(sum[:8])
}

// SourceFetchRef is the one ref the warm history keeps: the commit the last
// fetch asked for. ONE ref, not one per commit, and it is the reason the cache
// exists at all — git negotiates a fetch from the refs it holds, so a history
// with no refs (the engine's own git mirror fetches bare shas and keeps none)
// announces nothing it has and is sent the whole repository for every new
// commit. One ref is enough to announce the last commit and every ancestor of
// it; a ref per commit would grow without bound and lengthen the negotiation.
const SourceFetchRef = "refs/ca/fetched"
