package atoms

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
//
// AN IMPORT MERGE IS THE BASE OF THE SUBTREE IT BROUGHT. When dir is a
// subdirectory that the merge base does not have, and a first-parent merge
// between the merge base and HEAD joined an unrelated history (no common
// ancestor between its parents) and is what first put the directory in the
// tree, the answer is that merge: the imported lines are history the pull
// carries, not lines it wrote, and only what follows the import is its change
// (stellar-core: go:diff-coverage graded the whole imported go/ kit, 294
// lines). A directory the merge base already has, the repository root, and a
// history with no such merge keep the merge base.
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
	return importBase(ctx, in, dir, since), nil
}

// importBase answers the unrelated-histories merge that brought dir into the
// tree after since, or since itself. Every question it asks that git cannot
// answer leaves since in place: the merge base is the answer it refines, never
// a reason to fail.
func importBase(ctx context.Context, in Input, dir, since string) string {
	if filepath.Clean(dir) == filepath.Clean(in.Root) {
		return since // the repository root is no subtree
	}
	out, code := in.run(ctx, Cmd{Dir: dir, Name: "git", Args: []string{"rev-parse", "--show-prefix"}})
	prefix := strings.TrimSuffix(strings.TrimSpace(out), "/")
	if code != 0 || prefix == "" {
		return since
	}
	if _, code = in.run(ctx, Cmd{Dir: dir, Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", since + ":" + prefix}}); code != 1 {
		return since // the merge base has the directory (or git could not say)
	}
	out, code = in.run(ctx, Cmd{Dir: dir, Name: "git", Args: []string{"rev-list", "--merges", "--first-parent", "--parents", since + "..HEAD"}})
	if code != 0 {
		return since
	}
	found := since
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		merge, first, others := f[0], f[1], f[2:]
		if _, c := in.run(ctx, Cmd{Dir: dir, Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", first + ":" + prefix}}); c != 1 {
			continue // the first parent already had the directory
		}
		if _, c := in.run(ctx, Cmd{Dir: dir, Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", merge + ":" + prefix}}); c != 0 {
			continue // the merge did not bring it
		}
		for _, o := range others {
			if _, c := in.run(ctx, Cmd{Dir: dir, Name: "git", Args: []string{"merge-base", first, o}}); c == 1 {
				found = merge // exit 1 is no common ancestor; any other code is git failing
				break
			}
		}
	}
	return found
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
