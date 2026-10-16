package atoms

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The exported surface the atoms in their own packages read is the unexported
// helpers' answer, unchanged.
func TestThePortedAPIAnswersWhatTheHelpersDo(t *testing.T) {
	in := treeIn(t, map[string]string{"a.txt": "a", "d/b.txt": "b", ".git/HEAD": "ref"})
	tr := in.Tree()
	if entries, err := tr.Entries("."); err != nil || !slices.Equal(entries, []string{".git/", "a.txt", "d/"}) {
		t.Errorf("entries %v, %v", entries, err)
	}
	if body, err := tr.Read("a.txt"); err != nil || body != "a" {
		t.Errorf("read %q, %v", body, err)
	}
	if got := tr.Glob("d/*"); !slices.Equal(got, []string{"d/b.txt"}) {
		t.Errorf("glob %v", got)
	}
	if files, err := tr.Files(); err != nil || !slices.Equal(files, []string{"a.txt", "d/b.txt"}) {
		t.Errorf("files %v, %v", files, err)
	}

	ctx := context.Background()
	if out, code := (Input{Exec: func(context.Context, Cmd) (string, int) { return "seam", 9 }}).Run(ctx, Cmd{Name: "x"}); out != "seam" || code != 9 {
		t.Errorf("%q %d", out, code)
	}

	dir := newRepo(t)
	put(t, dir, "f", "1")
	base := commitAll(t, dir, "one")
	put(t, dir, "f", "2")
	commitAll(t, dir, "two")
	if out, code := Git(ctx, dir, "rev-parse", "HEAD"); code != 0 || len(strings.TrimSpace(out)) != 40 {
		t.Errorf("git %q %d", out, code)
	}
	if got, err := MergeBase(ctx, dir, base); err != nil || got != base {
		t.Errorf("merge base %q, %v", got, err)
	}

	dest := filepath.Join(t.TempDir(), "x", "y")
	if err := Place(dest, []byte("z")); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(dest); string(body) != "z" {
		t.Errorf("placed %q", body)
	}
	if a, b := TempPath("k", ".e"), TempPath("k", ".e"); a == b || !strings.HasSuffix(a, ".e") {
		t.Errorf("%q %q", a, b)
	}
	env := PythonEnv()
	if !slices.Contains(env, "PYTHONDONTWRITEBYTECODE=1") {
		t.Errorf("%v", env)
	}
	env[0] = "mutated"
	if PythonEnv()[0] == "mutated" {
		t.Error("PythonEnv hands out its own slice")
	}
}
