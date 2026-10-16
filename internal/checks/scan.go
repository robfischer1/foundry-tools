package checks

import (
	"fmt"
	"strings"
)

// The verdicts a scan over the tree's files shares. They live here, with the
// other helpers two or more atoms use, so an atom in its own package
// (atoms/<ns>/<name>) and the in-process runner's atoms word them alike.

// CannotEnumerate is the verdict when the TREE ITSELF would not answer: a
// could-not-run about the repository, in the wording the chains use.
func CannotEnumerate(a AtomDef, err error) Verdict {
	return VerdictOf(a, int(StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the tree would not enumerate: %v", a.ID, err))
}

// Interrupted is the verdict when the atom's deadline (or its caller) ended the
// run between two files. The runner has already settled the atom 2 by then;
// this is what stops the abandoned goroutine reading the rest of a tree
// nobody is waiting for.
func Interrupted(a AtomDef, err error) Verdict {
	return VerdictOf(a, int(StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - stopped before the scan finished: %v", a.ID, err))
}

// Unmeasured says how many files an atom could not read and why, first few
// only: a thousand identical permission errors are one fact.
func Unmeasured(errs []string) string {
	const shown = 5
	text := strings.Join(errs[:min(len(errs), shown)], "\n")
	if len(errs) > shown {
		text += fmt.Sprintf("\n... and %d more", len(errs)-shown)
	}
	return text
}
