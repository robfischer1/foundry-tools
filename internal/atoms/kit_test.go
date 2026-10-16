package atoms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// The exported surface is the unexported functions the atoms in their own files
// read the input through: each answers what its original does.
func TestTheTreeIsReadThroughTheExportedSurface(t *testing.T) {
	in := treeIn(t, map[string]string{"a/b.txt": "hello", "a/c.txt": "x", "d.txt": "y"})
	tr := in.Tree()
	if got, err := tr.Entries("a"); err != nil || strings.Join(got, ",") != "b.txt,c.txt" {
		t.Errorf("entries %v, %v", got, err)
	}
	if body, err := tr.Read("a/b.txt"); err != nil || body != "hello" {
		t.Errorf("read %q, %v", body, err)
	}
	if got := tr.Glob("a/*.txt"); strings.Join(got, ",") != "a/b.txt,a/c.txt" {
		t.Errorf("glob %v", got)
	}
	if got, err := tr.Files(); err != nil || strings.Join(got, ",") != "a/b.txt,a/c.txt,d.txt" {
		t.Errorf("files %v, %v", got, err)
	}
}

func TestPresentAndRequirePathsSayWhatTheTreeCarries(t *testing.T) {
	tr := treeIn(t, map[string]string{"tools/check.py": "x"}).Tree()
	if !Present(tr, "tools/check.py") || Present(tr, "tools/other.py") {
		t.Error("Present is wrong about the tree")
	}
	a := checks.AtomDef{ID: "x:y", Stage: checks.StagePrecommit}
	if v := RequirePaths(tr, a, [][2]string{{"tools/check.py", "unused"}}); v != nil {
		t.Errorf("a path that is there was refused: %+v", v)
	}
	v := RequirePaths(tr, a, [][2]string{{"tools/check.py", "unused"}, {"tools/other.py", "tools/other.py is absent."}})
	if v == nil || v.State != int(checks.StateCannotRun) || !strings.Contains(v.Reason, "x:y: CANNOT RUN - tools/other.py is absent.") {
		t.Errorf("%+v", v)
	}
}

func TestDoorOfIsTheInputsOwnOrProductions(t *testing.T) {
	if got := (Input{}).DoorOf(); got.Base != checks.NewDoor().Base {
		t.Errorf("%+v", got)
	}
	if got := (Input{Door: checks.Door{Base: "http://door.test"}}).DoorOf(); got.Base != "http://door.test" {
		t.Errorf("%+v", got)
	}
}

func TestTheSmallHelpersAreTheOnesTheyWrap(t *testing.T) {
	if a, b := TempPath("k", ".x"), TempPath("k", ".x"); a == b || !strings.HasSuffix(a, ".x") {
		t.Errorf("%s %s", a, b)
	}
	if got := Indent("a\nb\n", "  "); strings.Join(got, "|") != "  a|  b" {
		t.Errorf("%v", got)
	}
	if _, err := MergeBase(t.Context(), t.TempDir(), "nope"); err == nil {
		t.Error("a base in a directory that is no repository was found")
	}
}

func TestPlaceWritesAFileMakingItsDirectories(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "a", "b", "c.txt")
	if err := Place(dest, []byte("body")); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(dest); err != nil || string(got) != "body" {
		t.Errorf("%q, %v", got, err)
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Place(filepath.Join(blocker, "sub", "x"), nil); err == nil {
		t.Error("a file under a file was placed")
	}
}

func TestHasVenvFollowsWhereTheVenvIs(t *testing.T) {
	old := pythonBinDir
	t.Cleanup(func() { pythonBinDir = old })
	pythonBinDir = t.TempDir()
	if HasVenv() {
		t.Error("an empty directory is a venv")
	}
	if err := os.WriteFile(filepath.Join(pythonBinDir, "python3"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if !HasVenv() {
		t.Error("a venv with python3 in it is not one")
	}
}
