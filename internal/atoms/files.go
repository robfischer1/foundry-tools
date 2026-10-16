package atoms

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// The scan verdicts live in internal/checks (CannotEnumerate, Interrupted,
// Unmeasured), where the atoms in their own packages read them too.
var (
	cannotEnumerate = checks.CannotEnumerate
	interrupted     = checks.Interrupted
	unmeasured      = checks.Unmeasured
)

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
func checkAddedLargeFiles(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if in.FilesErr != nil {
		return cannotEnumerate(a, in.FilesErr)
	}
	clean := fmt.Sprintf("fleet:check-added-large-files: nothing over %d KB", checks.LargeFileLimitKB)

	var stat strings.Builder
	var unread []string
	for _, f := range in.Files {
		if err := ctx.Err(); err != nil {
			return interrupted(a, err)
		}
		fi, err := os.Lstat(filepath.Join(in.Root, f))
		if err != nil {
			unread = append(unread, err.Error())
			continue
		}
		fmt.Fprintf(&stat, "%d %s\n", fi.Size(), f)
	}
	if big := checks.FilesOver(stat.String(), checks.LargeFileLimitKB*1024); len(big) > 0 {
		return checks.VerdictOf(a, int(checks.StateFindings), fmt.Sprintf("files over %d KB:\n%s", checks.LargeFileLimitKB, strings.Join(big, "\n")))
	}
	if len(unread) > 0 {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - %d file(s) in the population could not be measured, and a size that was never read is not a size under the limit.\n%s",
			a.ID, len(unread), unmeasured(unread)))
	}
	return checks.VerdictOf(a, int(checks.StatePass), clean)
}
