package opsdup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// run is the atom's check over an input, as the runner calls it.
func run(in atoms.Input) checks.Verdict { return check(context.Background(), in) }

func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	atomtest.DeclaresItsRow(t, Def, "precommit", "python")
	if Def.ID != id {
		t.Errorf("%+v", Def)
	}
}

func TestTheChecker(t *testing.T) {
	atomtest.CheckerSuite(t, check, atomtest.Checker{ID: "ops:dup", Tool: "dup-check", Python: "python3", Args: []string{"--blocking"}, Executable: true})
}

// ops:dup is run as a program, so it has to be tracked executable.
func TestDupIsRunAsAProgramSoItHasToBeTrackedExecutable(t *testing.T) {
	in, f := atomtest.CheckerTree(t, "dup-check", 0o644, nil)
	atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Absent, "no tools/dup-check in this tree")
	if len(f.Calls) != 0 {
		t.Errorf("a script that is not executable was run: %v", f.Ran())
	}
}

func TestATrackedNameWithNoFileBehindItIsNotAChecker(t *testing.T) {
	in, _ := atomtest.ToolTree(t, map[string]string{"flux/x.yaml": "a: 1\n"}, nil)
	in.Committable = append(in.Committable, "tools/dup-check")
	atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Absent, "no tools/dup-check in this tree")
}

func TestATrackedSymlinkIsNotAnExecutableScript(t *testing.T) {
	in, _ := atomtest.ToolTree(t, map[string]string{"flux/x.yaml": "a: 1\n", "elsewhere": "#!/usr/bin/env python3\n"}, nil)
	if err := os.MkdirAll(filepath.Join(in.Root, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(in.Root, "elsewhere"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../elsewhere", filepath.Join(in.Root, "tools/dup-check")); err != nil {
		t.Fatal(err)
	}
	in.Committable = append(in.Committable, "tools/dup-check")
	atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Absent, "no tools/dup-check in this tree")
}
