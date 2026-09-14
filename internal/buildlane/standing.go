package buildlane

import (
	"fmt"
	"regexp"
	"strings"
)

var commitSha = regexp.MustCompile(`^[0-9a-f]{40}$`)

// IsCommit answers whether an image's revision label names a commit: forty
// lowercase hex, the way the lane stamps org.opencontainers.image.revision.
func IsCommit(s string) bool { return commitSha.MatchString(s) }

// Standing decides a commit against its last permitted build, from the change
// set since that build's commit: every change inert stands down, because
// :stable already carries the source; anything else builds, naming the first
// eight.
func Standing(permitted, changed string) (needed bool, why string) {
	nonInert := NonInert(changed)
	if len(nonInert) == 0 {
		return false, fmt.Sprintf("every change since %.12s, the last permitted build (:stable), is inert — nothing built, published or permitted; :stable already carries this commit's source", permitted)
	}
	shown := nonInert[:min(len(nonInert), 8)]
	return true, fmt.Sprintf("%d source or deploy change(s) since %.12s, the last permitted build (:stable), building: %s", len(nonInert), permitted, strings.Join(shown, ", "))
}
