package atoms

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// tree is the repository root read the way the chains read their Directory:
// a directory's entries (a directory named with a trailing slash, as the engine
// names them), a file's contents, a glob. Several atoms are written against
// exactly those three questions, so their port keeps the questions and changes
// only who answers: the disk, not the Dagger API.
type tree struct{ root string }

func (in Input) tree() tree { return tree{root: in.Root} }

// entries lists dir's immediate children. The error is the directory's own:
// it is not there or would not list.
func (t tree) entries(dir string) ([]string, error) {
	des, err := os.ReadDir(filepath.Join(t.root, dir))
	if err != nil {
		return nil, err
	}
	out := make([]string, len(des))
	for i, de := range des {
		out[i] = de.Name()
		if de.IsDir() {
			out[i] += "/"
		}
	}
	return out, nil
}

// read answers a file's contents.
func (t tree) read(rel string) (string, error) {
	b, err := os.ReadFile(filepath.Join(t.root, rel))
	return string(b), err
}

// glob answers the paths a pattern matches, sorted, a directory with its
// trailing slash. ONE PATH SEGMENT AT A TIME (`*` does not cross a slash), which
// is every pattern an atom here asks; the whole-tree question is files. The
// pattern is a constant of the atom that asks, so the only error fs.Glob has
// (ErrBadPattern) is an authoring error the tests would meet, and a directory
// that would not list simply matches nothing.
func (t tree) glob(pattern string) []string {
	matches, _ := fs.Glob(os.DirFS(t.root), pattern)
	for i, m := range matches {
		if fi, err := os.Stat(filepath.Join(t.root, m)); err == nil && fi.IsDir() {
			matches[i] = m + "/"
		}
	}
	return matches
}

// files is every file under the root, slash-separated, relative to it and in
// lexical order: the chains' `Glob("**")`. The root's own .git is not walked (a
// checkout's object store is not a unit of anything and holds thousands of
// files); a .git deeper in, which a nested worktree carries, is a directory like
// any other.
func (t tree) files() ([]string, error) {
	var out []string
	err := fs.WalkDir(os.DirFS(t.root), ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && p == ".git":
			return fs.SkipDir
		case !d.IsDir():
			out = append(out, strings.TrimPrefix(p, "./"))
		}
		return nil
	})
	return out, err
}
