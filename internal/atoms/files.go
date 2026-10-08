package atoms

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// cannotEnumerate is the verdict when the TREE ITSELF would not answer: a
// could-not-run about the repository, in the wording the chains use.
func cannotEnumerate(a checks.AtomDef, err error) checks.Verdict {
	return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the tree would not enumerate: %v", a.ID, err))
}

// interrupted is the verdict when the atom's deadline (or its caller) ended the
// run between two files. The runner has already settled the atom 2 by then;
// this is what stops the abandoned goroutine reading the rest of a tree
// nobody is waiting for.
func interrupted(a checks.AtomDef, err error) checks.Verdict {
	return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - stopped before the scan finished: %v", a.ID, err))
}

// unmeasured says how many files an atom could not read and why, first few
// only: a thousand identical permission errors are one fact.
func unmeasured(errs []string) string {
	const shown = 5
	text := strings.Join(errs[:min(len(errs), shown)], "\n")
	if len(errs) > shown {
		text += fmt.Sprintf("\n... and %d more", len(errs)-shown)
	}
	return text
}

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

// conflictRE is checks.ConflictMarkers, the chain's own grep -E pattern.
var conflictRE = regexp.MustCompile(checks.ConflictMarkers)

// checkMergeConflict: no conflict markers were committed.
//
// `grep -In -E` IN PROCESS. -n: each hit is `path:line:text`, the line numbered
// from 1 and the text carrying everything grep would print — including a
// trailing carriage return, which grep does not strip. -I: a file containing a
// NUL byte is binary and is skipped whole, so a marker's bytes inside a
// compiled artifact are not a finding. The chain's grep prefixes the path only
// when xargs hands it two files or more; a population of one printed bare
// `line:text`. Here the path is always there — a finding nobody can place is
// no finding.
//
// THE SCAN THAT DID NOT FINISH IS NOT A PASS. The shell this replaced had
// `|| true`; an unreadable file with no hit is a 2, and a hit outranks it.
func checkMergeConflict(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if in.FilesErr != nil {
		return cannotEnumerate(a, in.FilesErr)
	}
	var hits, unread []string
	for _, f := range in.Files {
		if err := ctx.Err(); err != nil {
			return interrupted(a, err)
		}
		body, err := os.ReadFile(filepath.Join(in.Root, f))
		if err != nil {
			unread = append(unread, err.Error())
			continue
		}
		if bytes.IndexByte(body, 0) >= 0 {
			continue
		}
		for n, line := range strings.Split(string(body), "\n") {
			if conflictRE.MatchString(line) {
				hits = append(hits, fmt.Sprintf("%s:%d:%s", f, n+1, line))
			}
		}
	}
	switch {
	case len(hits) > 0:
		// TrimSpace as the chain's output() does: a last hit ending in "\r"
		// reads the same in both vectors.
		return checks.VerdictOf(a, int(checks.StateFindings), strings.TrimSpace(strings.Join(hits, "\n")))
	case len(unread) > 0:
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - the conflict-marker scan did not complete (%d file(s) unreadable) and found nothing in the rest. A scan that did not run is not a tree with no markers.\n%s",
			a.ID, len(unread), unmeasured(unread)))
	}
	return checks.VerdictOf(a, int(checks.StatePass), "fleet:check-merge-conflict: no conflict markers")
}
