package atoms

import (
	"context"
	"slices"
)

// THE SURFACE THE ATOMS IN THEIR OWN PACKAGES READ (atoms/<ns>/<name>). They
// cannot reach this package's unexported helpers, and each of these is the same
// helper under an exported name, not a second copy of it.

// Tree is the repository root read the way the chains read their Directory: a
// directory's entries (a directory named with a trailing slash), a file's
// contents, a glob and the whole-tree walk.
type Tree struct{ t tree }

// Tree is the input's root as a Tree; Input.FS, when set, is the walk's file
// system.
func (in Input) Tree() Tree { return Tree{in.tree()} }

// Entries lists dir's immediate children.
func (t Tree) Entries(dir string) ([]string, error) { return t.t.entries(dir) }

// Read answers a file's contents.
func (t Tree) Read(rel string) (string, error) { return t.t.read(rel) }

// Glob answers the paths a pattern matches, sorted, a directory with its
// trailing slash.
func (t Tree) Glob(pattern string) []string { return t.t.glob(pattern) }

// Files is every file under the root, slash-separated and sorted.
func (t Tree) Files() ([]string, error) { return t.t.files() }

// Run runs a program through the input's seam: Input.Exec when a test set one,
// the real thing (RunProgram) otherwise.
func (in Input) Run(ctx context.Context, c Cmd) (string, int) { return in.run(ctx, c) }

// Git runs git in root, answering its output and exit code (-1 when it would not
// start).
func Git(ctx context.Context, root string, args ...string) (string, int) {
	return git(ctx, root, args...)
}

// MergeBase is the commit the change set is measured from.
func MergeBase(ctx context.Context, root, base string) (string, error) {
	return mergeBase(ctx, root, base)
}

// Place writes a file, making its directories.
func Place(dest string, body []byte) error { return place(dest, body) }

// TempPath is a scratch path no other atom of this process uses, nothing made
// yet.
func TempPath(kind, ext string) string { return tempPath(kind, ext) }

// PythonEnv is the environment a python checker runs in: bytecode off, so the
// tree under check is left as it was found.
func PythonEnv() []string { return slices.Clone(pythonEnv) }
