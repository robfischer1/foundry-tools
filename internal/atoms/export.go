package atoms

import (
	"context"
	"os"

	"dagger/foundry-tools/internal/checks"
)

// WHAT THE ATOMS IN THEIR OWN PACKAGES (atoms/<ns>/<name>) READ OF THIS ONE.
// The tree they are handed, the program seam and the door are Input's, and an
// Input's unexported helpers are out of their reach; these are the same helpers,
// exported. They go when Input moves into atomkit.

// Tree is the repository root read the way the chains read their Directory: a
// directory's entries (a directory with a trailing slash), a file's contents, a
// glob and the whole walk.
type Tree struct{ t tree }

// Tree is the input's root as a Tree.
func (in Input) Tree() Tree { return Tree{t: in.tree()} }

// Entries lists dir's immediate children.
func (t Tree) Entries(dir string) ([]string, error) { return t.t.entries(dir) }

// Read answers a file's contents.
func (t Tree) Read(rel string) (string, error) { return t.t.read(rel) }

// Glob answers the paths a pattern matches, sorted.
func (t Tree) Glob(pattern string) []string { return t.t.glob(pattern) }

// Files is every file under the root, in lexical order.
func (t Tree) Files() ([]string, error) { return t.t.files() }

// AbsentRuleset answers for a tree with no rules/sast (checks.AbsentRuleset),
// reading the copier answers file that says which template stamped it.
func (t Tree) AbsentRuleset(a checks.AtomDef) checks.Verdict {
	answers, _ := t.t.read(checks.CopierAnswersFile)
	return checks.AbsentRuleset(a, answers)
}

// Run is the input's program seam: Exec, or the real RunProgram.
func (in Input) Run(ctx context.Context, c Cmd) (string, int) { return in.run(ctx, c) }

// GitDoor is the git door the input reads from.
func (in Input) GitDoor() checks.Door { return in.door() }

// TempPath is a fresh path under the atom's work directory: ATOM_WORK when the
// executor names one, the system's temporary directory otherwise.
func TempPath(kind, ext string) string { return tempPath(kind, ext) }

// NeverRan is a program that would not start: a 2, with the reason.
func NeverRan(a checks.AtomDef, why string) checks.Verdict { return neverRan(a, why) }

// WorkDir is where an atom writes what it needs for a run: ATOM_WORK when the
// executor names one, the system's temporary directory otherwise.
func WorkDir() string {
	if d := os.Getenv("ATOM_WORK"); d != "" {
		return d
	}
	return os.TempDir()
}
