// Package fleetcheckmergeconflict is the atom fleet:check-merge-conflict: no
// conflict markers were committed. One file holds the atom, and both runners
// execute it (lld.md FN-085): the atoms binary in the lead container and the
// Dagger adapter.
package fleetcheckmergeconflict

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It reads the tree and nothing else, so it runs in lead.
var Def = atomkit.Def{
	ID: "fleet:check-merge-conflict", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "No conflict markers were committed.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "fleet:check-merge-conflict"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// conflictRE is checks.ConflictMarkers, the chain's own grep -E pattern.
var conflictRE = regexp.MustCompile(checks.ConflictMarkers)

// check is `grep -In -E` IN PROCESS. -n: each hit is `path:line:text`, the line
// numbered from 1 and the text carrying everything grep would print, including
// a trailing carriage return, which grep does not strip. -I: a file containing
// a NUL byte is binary and is skipped whole, so a marker's bytes inside a
// compiled artifact are not a finding. The path is always there: a finding
// nobody can place is no finding.
//
// THE SCAN THAT DID NOT FINISH IS NOT A PASS. An unreadable file with no hit is
// a 2, and a hit outranks it.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	var hits, unread []string
	for _, f := range in.Files {
		if err := ctx.Err(); err != nil {
			return checks.Interrupted(row, err)
		}
		// A link is not read: a dangling one (base-images' chairman tree holds 61)
		// would fail the read, and a live one's target is scanned as itself.
		if in.Tree().IsLink(f) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(in.Root, f))
		if err != nil {
			unread = append(unread, err.Error())
			continue
		}
		hits = append(hits, markerHits(f, body)...)
	}
	switch {
	case len(hits) > 0:
		// TrimSpace as the chain's output() does: a last hit ending in "\r"
		// reads the same in both vectors.
		return checks.VerdictOf(row, int(checks.StateFindings), strings.TrimSpace(strings.Join(hits, "\n")))
	case len(unread) > 0:
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - the conflict-marker scan did not complete (%d file(s) unreadable) and found nothing in the rest. A scan that did not run is not a tree with no markers.\n%s",
			id, len(unread), checks.Unmeasured(unread)))
	}
	return checks.VerdictOf(row, int(checks.StatePass), id+": no conflict markers")
}

// markerHits is one file's `path:line:text` findings. A body holding a NUL byte
// is binary and yields none (grep -I).
func markerHits(path string, body []byte) []string {
	if bytes.IndexByte(body, 0) >= 0 {
		return nil
	}
	var hits []string
	for n, line := range strings.Split(string(body), "\n") {
		if conflictRE.MatchString(line) {
			hits = append(hits, fmt.Sprintf("%s:%d:%s", path, n+1, line))
		}
	}
	return hits
}
