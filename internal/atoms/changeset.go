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
// output() does the same, so an error reads the same in either vector. A git
// that would not START is the error; a git that ran and said no is a code.
//
// safe.directory=* because the tree is owned by whoever cloned it and the
// process may not be: git refuses such a repository ("dubious ownership"), and
// the lane image sets the same thing for every chain (gitSystemConfig).
func git(ctx context.Context, root string, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "safe.directory=*", "-C", root}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ee := new(exec.ExitError); errors.As(err, &ee) {
		return stdout.String() + stderr.String(), ee.ExitCode(), nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("git %s: %w", args[0], err)
	}
	return stdout.String(), 0, nil
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
	var (
		out  string
		code int
		err  error
	)
	if base != "" {
		since, serr := mergeBase(ctx, root, base)
		if serr != nil {
			return nil, serr
		}
		out, code, err = git(ctx, root, "diff", "-z", "--name-only", "--diff-filter=AMR", since+"..HEAD")
	} else {
		_, parent, perr := git(ctx, root, "rev-parse", "--verify", "--quiet", "HEAD^")
		if perr != nil {
			return nil, perr
		}
		if parent == 0 {
			out, code, err = git(ctx, root, "diff", "-z", "--name-only", "--diff-filter=AMR", "HEAD^..HEAD")
		} else {
			out, code, err = git(ctx, root, "show", "--pretty=", "-z", "--name-only", "--diff-filter=AMR", "HEAD")
		}
	}
	if err != nil {
		return nil, err
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
	_, code, err := git(ctx, root, "rev-parse", "--verify", "--quiet", base+"^{commit}")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("the base %s is not in this history", base)
	}
	out, code, err := git(ctx, root, "merge-base", base, "HEAD")
	if err != nil {
		return "", err
	}
	since := strings.TrimSpace(out)
	if code != 0 || since == "" {
		return "", fmt.Errorf("git found no merge base between the base %s and HEAD (exit %d): %s", base, code, since)
	}
	return since, nil
}

// Collect reads the tree ONCE and answers the Input every atom shares.
//
// THE POPULATION IS `git ls-files --cached --others --exclude-standard`, the
// committable files, where the chains ask the engine for a gitignore-filtered
// glob. They agree on a clean checkout; they differ for a file that is tracked
// AND matches a .gitignore (git lists it, the engine's filter drops it), which
// a fetched tree has none of by construction and a shadow run will show if one
// exists.
func Collect(ctx context.Context, root, base, origin string, now time.Time) Input {
	in := Input{Root: root, Origin: origin, Now: now}

	in.Tracked, in.TrackedErr = listFiles(ctx, root, "ls-files")
	files, err := listFiles(ctx, root, "ls-files", "--cached", "--others", "--exclude-standard")
	in.FilesErr = err
	in.Files = population(root, files)

	in.Changed, in.ChangedErr = ChangeSet(ctx, root, base)

	if origin == "" {
		out, code, err := git(ctx, root, "remote", "get-url", "origin")
		if err == nil && code == 0 {
			in.Origin = strings.TrimSpace(out)
		}
	}
	return in
}

// listFiles runs a `git ls-files` and decodes its quoted paths (SplitGitPaths:
// git C-quotes a path holding a control character or a non-ASCII byte, and the
// decode is to bytes, not runes).
func listFiles(ctx context.Context, root string, args ...string) ([]string, error) {
	out, code, err := git(ctx, root, args...)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("git %s exited %d: %s", strings.Join(args, " "), code, strings.TrimSpace(out))
	}
	return checks.SplitGitPaths(out)
}

// population is the gate's population over a git listing: sorted, deduplicated
// (an unmerged path is listed once per stage), the fleet exclude applied, and a
// directory entry — a submodule's gitlink — dropped, because it is not a file
// any atom can read.
func population(root string, listed []string) []string {
	sorted := slices.Clone(listed)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	var out []string
	for _, f := range checks.GatePopulation(sorted) {
		if fi, err := os.Lstat(filepath.Join(root, f)); err == nil && fi.IsDir() {
			continue
		}
		out = append(out, f)
	}
	return out
}
