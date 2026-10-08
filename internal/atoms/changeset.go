package atoms

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// git runs one git command in root and answers its output and exit code. The
// output is stdout, plus stderr when git exited non-zero — the module's
// output() does the same, so an error reads the same in either vector.
//
// A GIT THAT WOULD NOT START IS EXIT -1 with the reason as its output, not a
// second return value. Every caller already refuses a non-zero code, so "git is
// not installed" is refused by the same line as "git said no" and there is no
// error path beside it that no test can reach.
//
// safe.directory=* because the tree is owned by whoever cloned it and the
// process may not be: git refuses such a repository ("dubious ownership"), and
// the lane image sets the same thing for every chain (gitSystemConfig).
func git(ctx context.Context, root string, args ...string) (string, int) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "safe.directory=*", "-C", root}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), 0
	}
	if ee := new(exec.ExitError); errors.As(err, &ee) {
		return stdout.String() + stderr.String(), ee.ExitCode()
	}
	return fmt.Sprintf("git %s: %v", args[0], err), -1
}

// SnapshotError is the change set's answer for a source that is a throwaway
// snapshot of a linked worktree. It is typed so an atom that SAYS so (fleet:witness
// settles it pass, as the chain did) can tell it from a change set that would
// not compute.
type SnapshotError struct{ Kind string }

func (e SnapshotError) Error() string {
	return fmt.Sprintf("no change set to read: the source is a %s snapshot with no real commits", e.Kind)
}

// ChangeSet is the paths a pull added, modified or renamed, computed ONCE from
// git. It mirrors the module's fleet:witness (atoms_fleet.go) and changeBase
// (runtime.go), whose comments record real failures:
//
//   - With a base: the merge base of base and HEAD, then
//     `git diff --diff-filter=AMR <merge-base>..HEAD`. The base is main's tip at
//     dispatch, not the branch point, and a two-tree diff against it carries
//     every landing since as this pull's own change, reversed (urania #63:
//     ten deleted modules diffed as ADDED, and a mutation run graded a package
//     the pull never touched).
//   - With no base: HEAD^..HEAD, the tip against its parent.
//   - A root commit has no parent: every file it carries is the change.
//
// A base the history does not reach, and unrelated histories, are ERRORS — a
// change set with no origin is not a change set nothing touched. Paths come
// back NUL-separated from git, so a name with a space or a quote is intact.
func ChangeSet(ctx context.Context, root, base string) ([]string, error) {
	// A LINKED-WORKTREE SNAPSHOT HAS NO CHANGE SET. gitReady rebuilds a worktree
	// as a throwaway repository and marks it ca.snapshot; its one synthetic
	// commit is not the pull's commits (fleet:witness stands down the same way).
	// An error, not an empty set: "nothing changed" would pass a pull nobody read.
	if snap, code := git(ctx, root, "config", "--get", "ca.snapshot"); code == 0 && strings.TrimSpace(snap) != "" {
		return nil, SnapshotError{Kind: strings.TrimSpace(snap)}
	}
	var (
		out  string
		code int
	)
	if base != "" {
		since, err := mergeBase(ctx, root, base)
		if err != nil {
			return nil, err
		}
		out, code = git(ctx, root, "diff", "-z", "--name-only", "--diff-filter=AMR", since+"..HEAD")
	} else if _, parent := git(ctx, root, "rev-parse", "--verify", "--quiet", "HEAD^"); parent == 0 {
		out, code = git(ctx, root, "diff", "-z", "--name-only", "--diff-filter=AMR", "HEAD^..HEAD")
	} else {
		out, code = git(ctx, root, "show", "--pretty=", "-z", "--name-only", "--diff-filter=AMR", "HEAD")
	}
	if code != 0 {
		return nil, fmt.Errorf("git exited %d reading the change set: %s", code, strings.TrimSpace(out))
	}
	return checks.SplitNul(out), nil
}

// mergeBase is the commit the change set is measured FROM. rev-parse --verify
// --quiet first, not merge-base alone: a missing object is exit 128 from
// merge-base, which reads as a crash, and exit 1 from rev-parse, which reads as
// the answer it is (foundry-tools#63).
func mergeBase(ctx context.Context, root, base string) (string, error) {
	out, code := git(ctx, root, "rev-parse", "--verify", "--quiet", base+"^{commit}")
	if code < 0 {
		return "", fmt.Errorf("git would not run: %s", out)
	}
	if code != 0 {
		return "", fmt.Errorf("the base %s is not in this history", base)
	}
	out, code = git(ctx, root, "merge-base", base, "HEAD")
	if code != 0 {
		return "", fmt.Errorf("git found no merge base between the base %s and HEAD (exit %d): %s", base, code, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}

// Collect reads the tree ONCE and answers the Input every atom shares.
//
// THE POPULATION IS THE CHAINS' OWN, built from git: the committable files
// (`ls-files --cached --others --exclude-standard`) less two sets the chains
// never see.
//
//   - Tracked files a .gitignore matches (`--cached --ignored`). The engine's
//     Gitignore filter (run.population) drops them, and so does the `add -A`
//     that gitReadyOn runs over a worktree snapshot, so every chain atom skips a
//     file that was force-added and is ignored. A clone DOES contain such files;
//     the chains skip them anyway, and parity means this does too.
//   - Tracked files deleted from the working tree (`--deleted`). The chains glob
//     files that exist; Lstat or ReadFile on a deleted path would settle 2 where
//     the chain passes.
//
// Tracked is untouched: stop-justifications is defined over `git ls-files`.
func Collect(ctx context.Context, root, base, origin string, now time.Time) Input {
	in := Input{Root: root, Base: base, Origin: origin, Now: now}

	in.Tracked, in.TrackedErr = listFiles(ctx, root, "ls-files")
	// Three listings through ONE error path: the first failing is the tree not
	// enumerating, whichever of them it was.
	var sets [3][]string
	for i, q := range [][]string{
		{"ls-files", "--cached", "--others", "--exclude-standard"},
		{"ls-files", "--cached", "--ignored", "--exclude-standard"},
		{"ls-files", "--deleted"},
	} {
		var lerr error
		if sets[i], lerr = listFiles(ctx, root, q...); lerr != nil {
			in.FilesErr = lerr
			break
		}
	}
	files := slices.DeleteFunc(sets[0], func(f string) bool {
		return slices.Contains(sets[1], f) || slices.Contains(sets[2], f)
	})
	in.Committable = committable(root, files)
	in.Files = checks.GatePopulation(in.Committable)

	in.Changed, in.ChangedErr = ChangeSet(ctx, root, base)

	if origin == "" {
		out, code := git(ctx, root, "remote", "get-url", "origin")
		if code == 0 {
			in.Origin = strings.TrimSpace(out)
		}
	}
	return in
}

// listFiles runs a `git ls-files` and decodes its quoted paths (SplitGitPaths:
// git C-quotes a path holding a control character or a non-ASCII byte, and the
// decode is to bytes, not runes).
func listFiles(ctx context.Context, root string, args ...string) ([]string, error) {
	out, code := git(ctx, root, args...)
	if code != 0 {
		return nil, fmt.Errorf("git %s exited %d: %s", strings.Join(args, " "), code, strings.TrimSpace(out))
	}
	return checks.SplitGitPaths(out)
}

// committable is a git listing as the files the repository would commit:
// sorted, deduplicated (an unmerged path is listed once per stage), and a
// directory entry — a submodule's gitlink — dropped, because it is not a file
// any atom can read. The fleet exclude is NOT applied: the chains that read the
// raw tree (the compose atoms, ops:yaml) never applied it, and Files is this
// list with it applied.
func committable(root string, listed []string) []string {
	sorted := slices.Clone(listed)
	slices.Sort(sorted)
	var out []string
	for _, f := range slices.Compact(sorted) {
		if fi, err := os.Lstat(filepath.Join(root, f)); err == nil && fi.IsDir() {
			continue
		}
		out = append(out, f)
	}
	return out
}
