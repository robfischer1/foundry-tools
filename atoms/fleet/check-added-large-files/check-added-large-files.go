// Package fleetcheckaddedlargefiles is the atom fleet:check-added-large-files: no file in the
// tree exceeds 2048 KB. One file holds the atom, and both runners execute it (lld.md FN-085).
package fleetcheckaddedlargefiles

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It runs in lead: the tree it reads is the pod's, and
// nothing else needs a toolchain.
var Def = atomkit.Def{
	ID: "fleet:check-added-large-files", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "No file in the tree exceeds 2 MB.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "fleet:check-added-large-files"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// checkAddedLargeFiles: no file in the tree exceeds 2048 KB.
//
// THE CHAIN ASKED `xargs stat -c '%s %n'` AND GAVE THE LINES TO checks.FilesOver;
// this does the same with os.Lstat and the same function, so the comparison
// (strictly greater than the limit — a file of exactly 2048 KB passes) and the
// way a path with spaces survives are the chain's own, not a second copy.
// Lstat, not Stat: `stat` without -L reports a symlink's own size.
//
// A PARTIAL MEASUREMENT IS NOT A CLEAN ONE. A size never read is not a size
// under the limit, so an unreadable file with no finding is a 2; a finding
// still outranks it, as in the chain — a file measured over the ceiling is over
// it however the rest of the pass went.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	clean := fmt.Sprintf("fleet:check-added-large-files: nothing over %d KB", checks.LargeFileLimitKB)

	var stat strings.Builder
	var unread []string
	for _, f := range in.Files {
		if err := ctx.Err(); err != nil {
			return checks.Interrupted(row, err)
		}
		fi, err := os.Lstat(filepath.Join(in.Root, f))
		if err != nil {
			unread = append(unread, err.Error())
			continue
		}
		fmt.Fprintf(&stat, "%d %s\n", fi.Size(), f)
	}
	if big := checks.FilesOver(stat.String(), checks.LargeFileLimitKB*1024); len(big) > 0 {
		return checks.VerdictOf(row, int(checks.StateFindings), fmt.Sprintf("files over %d KB:\n%s", checks.LargeFileLimitKB, strings.Join(big, "\n")))
	}
	if len(unread) > 0 {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - %d file(s) in the population could not be measured, and a size that was never read is not a size under the limit.\n%s",
			id, len(unread), checks.Unmeasured(unread)))
	}
	return checks.VerdictOf(row, int(checks.StatePass), clean)
}
