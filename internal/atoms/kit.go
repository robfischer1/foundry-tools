package atoms

import (
	"context"
	"os"
	"path/filepath"

	"dagger/foundry-tools/internal/checks"
)

// THE SURFACE THE ATOMS IN THEIR OWN FILES (atoms/<ns>/<name>) READ THE INPUT
// THROUGH. They are packages of their own, so what they share with the atoms
// still written here is exported once, in this file, and each is the function
// the unexported name already was: an atom is moved, never copied.

// Tree is the repository root as an atom reads it: a directory's entries, a
// file, a glob, every file.
type Tree = tree

// Tree answers the input's root.
func (in Input) Tree() Tree { return in.tree() }

// Entries lists dir's immediate children, a directory with its trailing slash.
func (t tree) Entries(dir string) ([]string, error) { return t.entries(dir) }

// Read answers a file's contents.
func (t tree) Read(rel string) (string, error) { return t.read(rel) }

// IsLink says whether rel is a symbolic link, by lstat: a link is never
// followed, so a dangling one is no unreadable file. A path that cannot be
// lstat'ed is not a link; reading it says why.
func (t tree) IsLink(rel string) bool {
	fi, err := os.Lstat(filepath.Join(t.root, rel))
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

// Glob answers the paths a pattern matches, one path segment at a time.
func (t tree) Glob(pattern string) []string { return t.glob(pattern) }

// Files answers every file under the root, less its own .git.
func (t tree) Files() ([]string, error) { return t.files() }

// DoorOf is the git door the input reads from: its own, or production's.
func (in Input) DoorOf() checks.Door { return in.door() }

// TempPath is a scratch path no other atom of this process uses.
func TempPath(kind, ext string) string { return tempPath(kind, ext) }

// MergeBase is the commit the change set is measured from.
func MergeBase(ctx context.Context, root, base string) (string, error) {
	return mergeBase(ctx, root, base)
}

// Indent sets a tool's output in from the line that introduced it.
func Indent(s, prefix string) []string { return indent(s, prefix) }

// Place writes a fetched file, making its directories.
func Place(dest string, body []byte) error {
	// The MkdirAll error is not branched on: a directory that could not be made
	// fails the write below, with the same cause.
	_ = os.MkdirAll(filepath.Dir(dest), 0o755)
	return os.WriteFile(dest, body, 0o644)
}

// Present answers whether the tree carries a path (or a pattern's match).
func Present(t Tree, path string) bool { return len(t.glob(path)) > 0 }

// RequirePaths is the run of `must be there or CANNOT RUN` an atom opens with;
// each entry keeps its own sentence, which says what cannot be claimed without
// that file. Nil means every path is there.
func RequirePaths(t Tree, a checks.AtomDef, required [][2]string) *checks.Verdict {
	for _, req := range required {
		if !Present(t, req[0]) {
			v := checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - "+req[1])
			return &v
		}
	}
	return nil
}

// HasVenv says whether the tools container's prebuilt python venv is there: the
// atoms binary's container has it, a toolchain image on the executor path does
// not.
func HasVenv() bool { return programPath("python3") != "python3" }
