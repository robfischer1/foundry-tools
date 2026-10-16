package atoms

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// THE BASE A DIFF-SCOPED ATOM MEASURES FROM, for the atoms that run in the
// toolchain's container and ask git themselves (go:diff-coverage, the rust
// lane's scope). It is run.changeBase and run.missingBase of the chains, as
// execs: the same questions, worded as they were.

// doorTree reports whether the door fetched the tree under check. The adapter
// and the pod's executor hand the origin the tree was fetched from as
// ATOM_ORIGIN; a hook's local snapshot names none, and has no history to reach
// a base with.
var doorTree = func() bool { return os.Getenv("ATOM_ORIGIN") != "" }

// ChangeBase answers the commit a pull's change set is measured FROM: the merge
// base of the base sha and HEAD, asked in dir. "" with a nil error is a base the
// history does not reach, and the atom stands down as it does with no base at
// all (MissingBase).
//
// THE BASE IS MAIN'S TIP AT DISPATCH, NOT THE BRANCH POINT. `git diff base HEAD`
// compares two trees, so once main has moved past where the pull branched,
// every landing since reads as the pull's own change, reversed (urania #63).
//
// rev-parse --verify --quiet first, not merge-base alone: a missing object is
// exit 128 from merge-base, which the engine reports as its own error even
// under Expect ANY (foundry-tools#63); rev-parse answers 1. A merge-base that
// exits non-zero (unrelated histories) or answers nothing is an error the atom
// settles as could-not-run — a change set with no origin is not a change set
// nothing touched.
func ChangeBase(ctx context.Context, in Input, dir string) (string, error) {
	out, code := in.run(ctx, Cmd{Dir: dir, Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", in.Base + "^{commit}"}})
	if code < 0 {
		return "", fmt.Errorf("the atom never ran: %s", out)
	}
	if code != 0 {
		return "", nil
	}
	out, code = in.run(ctx, Cmd{Dir: dir, Name: "git", Args: []string{"merge-base", in.Base, "HEAD"}})
	if code < 0 {
		return "", fmt.Errorf("the atom never ran: %s", out)
	}
	since := strings.TrimSpace(out)
	if code != 0 || since == "" {
		return "", fmt.Errorf("git found no merge base between the base %s and HEAD (exit %d): %s", in.Base, code, out)
	}
	return since, nil
}

// MissingBase is how a diff-scoped atom settles when the door named a base and
// ChangeBase found it nowhere in the history: the state and the reason.
//
// ON A TREE THE DOOR HAD FETCHED, IT IS A COULD-NOT-RUN. The checkout carries
// the commit's whole history and the base was fetched into it by sha, so a base
// still missing is a broken fetch — and an atom that read it as "no base,
// nothing to scope to" would pass the pull having checked nothing. Only a local
// snapshot with no history to reach the base stands down, under the atom's own
// words.
func MissingBase(in Input, standDown string) (int, string) {
	if doorTree() {
		return 2, "CANNOT RUN - the base " + in.Base + " is not in this history, though the door named it and it was fetched — a change set with no origin is not one nothing touched"
	}
	return 0, standDown
}
