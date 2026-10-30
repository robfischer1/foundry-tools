package atoms

import (
	"strings"
	"testing"
)

// importFixture is a main with a root file, and a feat branch off it.
func importFixture(t *testing.T) (dir, base string) {
	t.Helper()
	dir = newRepo(t)
	put(t, dir, "README.md", "r\n")
	base = commitAll(t, dir, "main")
	gitIn(t, dir, "checkout", "-q", "-b", "feat")
	return dir, base
}

// importHistory builds an unrelated history whose tree is already filed under
// prefix/ (as filter-repo leaves it) and merges it into the current branch.
func importHistory(t *testing.T, dir, prefix string) string {
	t.Helper()
	cur := gitIn(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
	gitIn(t, dir, "checkout", "-q", "--orphan", "imported")
	gitIn(t, dir, "rm", "-rfq", "--ignore-unmatch", ".")
	put(t, dir, prefix+"/a.go", "package a\n")
	put(t, dir, prefix+"/go.mod", "module a\n")
	commitAll(t, dir, "imported root")
	gitIn(t, dir, "checkout", "-q", cur)
	gitIn(t, dir, "merge", "-q", "--allow-unrelated-histories", "--no-ff", "-m", "import "+prefix, "imported")
	return gitIn(t, dir, "rev-parse", "HEAD")
}

func TestChangeBaseTakesTheImportMergeForItsSubtree(t *testing.T) {
	dir, base := importFixture(t)
	merge := importHistory(t, dir, "go")
	put(t, dir, "go/a.go", "package a\n\nvar X = 1\n")
	commitAll(t, dir, "edit after import")
	in := Input{Root: dir, Base: base}

	got, err := ChangeBase(t.Context(), in, dir+"/go")
	if err != nil || got != merge {
		t.Errorf("inside the imported subtree the base is the import merge %s, got %q %v", merge, got, err)
	}
	// the repository root is not a subtree: it keeps the merge base
	got, err = ChangeBase(t.Context(), in, dir)
	if err != nil || got != base {
		t.Errorf("at the root the base is the merge base %s, got %q %v", base, got, err)
	}
	// a directory the import did not bring keeps the merge base too
	put(t, dir, "other/x.go", "package x\n")
	commitAll(t, dir, "other")
	got, err = ChangeBase(t.Context(), in, dir+"/other")
	if err != nil || got != base {
		t.Errorf("a subtree the import did not bring keeps the merge base %s, got %q %v", base, got, err)
	}
}

func TestChangeBaseKeepsTheMergeBaseWhereNoImportMergeIs(t *testing.T) {
	dir, base := importFixture(t)
	put(t, dir, "go/a.go", "package a\n")
	commitAll(t, dir, "plain subtree")
	// an ordinary merge of a related branch is no import
	gitIn(t, dir, "checkout", "-q", "-b", "side", base)
	put(t, dir, "side.txt", "s\n")
	commitAll(t, dir, "side")
	gitIn(t, dir, "checkout", "-q", "feat")
	gitIn(t, dir, "merge", "-q", "--no-ff", "-m", "merge side", "side")

	got, err := ChangeBase(t.Context(), Input{Root: dir, Base: base}, dir+"/go")
	if err != nil || got != base {
		t.Errorf("a normal history keeps the merge base %s, got %q %v", base, got, err)
	}
}

func TestChangeBaseIgnoresAnImportIntoASubtreeMainAlreadyHas(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, "go/old.go", "package old\n")
	base := commitAll(t, dir, "main has go/")
	gitIn(t, dir, "checkout", "-q", "-b", "feat")
	importHistory(t, dir, "go")

	got, err := ChangeBase(t.Context(), Input{Root: dir, Base: base}, dir+"/go")
	if err != nil || got != base {
		t.Errorf("the subtree is not new, so its lines are the pull's: want %s, got %q %v", base, got, err)
	}
}

func TestChangeBaseStillErrorsOnATrulyUnrelatedBase(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, "a.txt", "1\n")
	tip := commitAll(t, dir, "c1")
	gitIn(t, dir, "checkout", "-q", "--orphan", "other")
	put(t, dir, "go/z.go", "package z\n")
	commitAll(t, dir, "orphan")

	_, err := ChangeBase(t.Context(), Input{Root: dir, Base: tip}, dir+"/go")
	if err == nil || !strings.Contains(err.Error(), "git found no merge base between the base") {
		t.Errorf("want the could-not-run error, got %v", err)
	}
}

// A subtree added by plain commits, with no merge at all, keeps the merge base
// (the rev-list answers nothing), and so does one whose history git cannot list.
func TestChangeBaseKeepsTheMergeBaseWithoutMergesOrAHistoryItCanList(t *testing.T) {
	dir, base := importFixture(t)
	put(t, dir, "go/a.go", "package a\n")
	commitAll(t, dir, "plain subtree, no merge")
	got, err := ChangeBase(t.Context(), Input{Root: dir, Base: base}, dir+"/go")
	if err != nil || got != base {
		t.Errorf("no merge in the range keeps the merge base %s, got %q %v", base, got, err)
	}

	f := &golaneFake{rules: []golaneRule{
		{"merge-base abc123 HEAD", "since0\n", 0},
		{"--show-prefix", "go/\n", 0},
		{"rev-parse --verify --quiet since0:go", "", 1},
		{"rev-list", "fatal: bad range", 128},
	}}
	got, err = ChangeBase(t.Context(), Input{Root: "/src", Base: "abc123", Exec: f.exec}, "/src/go")
	if err != nil || got != "since0" {
		t.Errorf("a rev-list git fails keeps the merge base, got %q %v", got, err)
	}
}
