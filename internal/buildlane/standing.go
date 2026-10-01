package buildlane

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var commitSha = regexp.MustCompile(`^[0-9a-f]{40}$`)

// IsCommit answers whether an image's revision label names a commit: forty
// lowercase hex, the way the lane stamps org.opencontainers.image.revision.
func IsCommit(s string) bool { return commitSha.MatchString(s) }

// Standing decides a commit against the last build published from its repo,
// from the change set since that build's commit: every change inert stands
// down, because the image tagged `at` already carries the source; anything
// else builds, naming the first eight. at is the tag the build was read from
// (a star's newest stamp tag, a base's :stable).
func Standing(last, at, changed string) (needed bool, why string) {
	nonInert := NonInert(changed)
	if len(nonInert) == 0 {
		return false, fmt.Sprintf("every change since %.12s, the last published build (:%s), is inert — nothing built or published; :%s already carries this commit's source", last, at, at)
	}
	shown := nonInert[:min(len(nonInert), 8)]
	return true, fmt.Sprintf("%d source or deploy change(s) since %.12s, the last published build (:%s), building: %s", len(nonInert), last, at, strings.Join(shown, ", "))
}

// Eighteen digits at most, so the field always parses as an int64.
var stampTag = regexp.MustCompile(`^([0-9]{1,18})-[0-9a-f]{7}$`)

// NewestStamp answers the newest stamp tag (StampTag's `{unix-ts}-{sha7}`) in
// a tag listing, one tag per line: the highest timestamp, and on a tie the
// later name. ok is false when the listing holds none.
//
// THE NEWEST STAMP IS THE STAR'S LAST PUBLISHED TIP, by the order Flux's
// ImagePolicy reads: the timestamp is the commit's committer time, so the
// highest is the latest landing that published — the image the automation
// rolls the star to. A g-pin names a commit and carries no order.
func NewestStamp(listing string) (tag string, ok bool) {
	type stamp struct {
		ts  int64
		tag string
	}
	var stamps []stamp
	for _, line := range strings.Split(listing, "\n") {
		line = strings.TrimSpace(line)
		if m := stampTag.FindStringSubmatch(line); m != nil {
			ts, _ := strconv.ParseInt(m[1], 10, 64)
			stamps = append(stamps, stamp{ts, line})
		}
	}
	if len(stamps) == 0 {
		return "", false
	}
	newest := slices.MaxFunc(stamps, func(a, b stamp) int {
		return cmp.Or(cmp.Compare(a.ts, b.ts), strings.Compare(a.tag, b.tag))
	})
	return newest.tag, true
}
