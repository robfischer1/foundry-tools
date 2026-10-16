package atomtest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Checker is one of the ops atoms that run the tree's own checker
// (atomlib.RunChecker): the file under tools/ it runs, the python that runs it
// and the arguments after it.
type Checker struct {
	ID, Tool, Python string
	Args             []string
	Executable       bool
}

// PyAnswer answers every `--version` probe and hands the rest to the script.
func PyAnswer(answer func(atoms.Cmd) (string, int)) func(atoms.Cmd) (string, int) {
	return func(c atoms.Cmd) (string, int) {
		if len(c.Args) == 1 && c.Args[0] == "--version" {
			return "Python 3.14.0", 0
		}
		if answer == nil {
			return "", 0
		}
		return answer(c)
	}
}

// CheckerTree is an ops tree (it carries flux/) with one of the tree's own
// checkers, tracked executable or not.
func CheckerTree(t *testing.T, tool string, mode os.FileMode, answer func(atoms.Cmd) (string, int)) (atoms.Input, *ToolFake) {
	t.Helper()
	in, f := ToolTree(t, map[string]string{"flux/x.yaml": "a: 1\n", "tools/" + tool: "#!/usr/bin/env python3\n"}, PyAnswer(answer))
	if err := os.Chmod(filepath.Join(in.Root, "tools", tool), mode); err != nil {
		t.Fatal(err)
	}
	return in, f
}

// CheckerSuite holds a checker atom to the shape all six share: the surface, the
// checker's presence, the probe, the run and the settle. run is the atom's check.
func CheckerSuite(t *testing.T, run func(context.Context, atoms.Input) checks.Verdict, c Checker) {
	t.Helper()
	phase := strings.TrimPrefix(c.ID, "ops:")
	for _, k := range []struct {
		name    string
		code    int
		out     string
		state   int
		result  string
		needles []string
	}{
		{"a checker that holds is a pass", 0, "checked", 0, Pass, []string{"checked"}},
		{"a checker that finds something is a finding", 1, "a duplicate", 1, Findings, []string{"a duplicate", phase + " failed (rc=1) — findings"}},
		{"an uncaught exception (missing pyyaml) is a 2, not a finding", 1, "Traceback (most recent call last):\n  File \"x\", line 1, in <module>\nModuleNotFoundError: No module named 'yaml'\n", 2, Cannot, []string{"ModuleNotFoundError: No module named 'yaml'", "crashed on an uncaught python exception"}},
		{"a fault of the substrate is a 2, not a finding", 1, "connection refused", 2, Cannot, []string{"failed on a fault of the substrate"}},
		{"a python that would not start never ran", -1, "python: gone", 2, Cannot, []string{"the atom never ran: python: gone"}},
	} {
		t.Run(k.name, func(t *testing.T) {
			in, f := CheckerTree(t, c.Tool, 0o755, func(atoms.Cmd) (string, int) { return k.out, k.code })
			Expect(t, run(context.Background(), in), StateOf(k.state), k.result, k.needles...)
			last := f.LastCall(t)
			want := append([]string{"tools/" + c.Tool}, c.Args...)
			if last.Name != c.Python || strings.Join(last.Args, " ") != strings.Join(want, " ") || last.Dir != in.Root || !last.Both {
				t.Errorf("the checker ran as %+v, want %s %v in the root with both streams", last, c.Python, want)
			}
			if probe := f.Calls[0]; probe.Name != c.Python || Flagged(probe) != "--version" {
				t.Errorf("the first call was %+v: the version probe comes before the work", probe)
			}
			if !strings.Contains(strings.Join(last.Env, " "), "PYTHONDONTWRITEBYTECODE=1") {
				t.Errorf("the checker may write bytecode into the tree: %v", last.Env)
			}
		})
	}
	t.Run("a python that fails its probe is unprovisioned", func(t *testing.T) {
		in, f := ToolTree(t, map[string]string{"flux/x.yaml": "a: 1\n", "tools/" + c.Tool: "#!/usr/bin/env python3\n"}, func(atoms.Cmd) (string, int) { return "gone", 127 })
		if err := os.Chmod(filepath.Join(in.Root, "tools", c.Tool), 0o755); err != nil {
			t.Fatal(err)
		}
		Expect(t, run(context.Background(), in), StateOf(2), Cannot, "the phase's tool could not be provisioned", c.Python+" --version exited 127")
		if len(f.Calls) != 1 {
			t.Errorf("the checker ran on an unprovisioned container: %v", f.Ran())
		}
	})
	t.Run("no checker is absent and python is not touched", func(t *testing.T) {
		in, f := ToolTree(t, map[string]string{"flux/x.yaml": "a: 1\n"}, nil)
		Expect(t, run(context.Background(), in), StateOf(0), Absent, "no tools/"+c.Tool+" in this tree")
		if len(f.Calls) != 0 {
			t.Errorf("an absent atom touched %v", f.Ran())
		}
	})
	t.Run("a tree that is not an ops tree is absent", func(t *testing.T) {
		in, f := ToolTree(t, map[string]string{"main.go": "x"}, nil)
		Expect(t, run(context.Background(), in), StateOf(0), Absent, "has no ops shape")
		if len(f.Calls) != 0 {
			t.Errorf("an absent atom touched %v", f.Ran())
		}
	})
	t.Run("a population that failed", func(t *testing.T) {
		in, _ := CheckerTree(t, c.Tool, 0o755, nil)
		in.FilesErr = ErrBoom
		Expect(t, run(context.Background(), in), StateOf(2), Cannot, "the tree would not enumerate")
	})
}
