package witvalidate

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// run is the atom's check over an input, as the runner calls it.
func run(in atoms.Input) checks.Verdict { return check(context.Background(), in) }

func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	atomtest.DeclaresItsRow(t, Def, "prepush", "none")
	if Def.ID != id {
		t.Errorf("%+v", Def)
	}
}

func TestWitValidate(t *testing.T) {
	files := map[string]string{"wit/a.wit": "package a:b;\n", "justfile": "validate:\n\twasm-tools component wit wit/\n"}
	for _, tc := range []struct {
		name    string
		recipe  string
		code    int
		state   int
		result  string
		needles []string
	}{
		{"a recipe that passes", "ok", 0, 0, atomtest.Pass, nil},
		{"the recipe's exit 1 passes through as a finding", "error: unresolved type", 1, 1, atomtest.Findings, []string{"unresolved type"}},
		{"any other exit is a could-not-run", "boom", 101, 2, atomtest.Cannot, []string{"boom"}},
		{"a just that would not start never ran", "just: gone", -1, 2, atomtest.Cannot, []string{"the atom never ran: just: gone"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := atomtest.ToolTree(t, files, func(c atoms.Cmd) (string, int) {
				if c.Name == "just" && c.Args[0] == "validate" {
					return tc.recipe, tc.code
				}
				return "1.0", 0
			})
			atomtest.Expect(t, run(in), atomtest.StateOf(tc.state), tc.result, tc.needles...)
			// The probes, the private tree's repository, and then the recipe.
			if got := strings.Join(f.Ran(), " "); got != "wasm-tools just git git just" {
				t.Errorf("ran %s", got)
			}
			c := f.Calls[4]
			if atomtest.Flagged(c) != "validate" || c.Dir == in.Root || !c.Both {
				t.Errorf("the recipe ran as %q in %q (both streams %v): not the shared tree", atomtest.Flagged(c), c.Dir, c.Both)
			}
			if _, err := os.Stat(c.Dir); err == nil {
				t.Errorf("the private tree %s was left behind", c.Dir)
			}
			for _, g := range f.Calls[2:4] {
				if g.Dir != c.Dir || !slices.Contains(g.Env, "GIT_CONFIG_NOSYSTEM=1") {
					t.Errorf("git %v ran in %q with %v: the private tree's index, off the developer's configuration", g.Args, g.Dir, g.Env)
				}
			}
		})
	}
	for _, tool := range []string{"wasm-tools", "just"} {
		t.Run("a "+tool+" that fails its probe", func(t *testing.T) {
			in, f := atomtest.ToolTree(t, files, func(c atoms.Cmd) (string, int) {
				if c.Name == tool {
					return "gone", 127
				}
				return "1.0", 0
			})
			atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, tool+" --version exited 127", "WIT that was never resolved is not WIT that passed")
			for _, c := range f.Calls {
				if c.Name == "just" && c.Args[0] == "validate" {
					t.Errorf("the recipe ran with %s unprovisioned", tool)
				}
			}
		})
	}
	t.Run("a recipe that writes leaves the shared tree as it was", func(t *testing.T) {
		in, _ := atomtest.ToolTree(t, files, func(c atoms.Cmd) (string, int) {
			if c.Name == "just" && c.Args[0] == "validate" {
				atomtest.Put(t, c.Dir, "wit/generated.wit", "written by the recipe")
				atomtest.Put(t, c.Dir, ".cache/x", "a cache")
				if body, err := os.ReadFile(filepath.Join(c.Dir, "wit/a.wit")); err != nil || string(body) != "package a:b;\n" {
					t.Errorf("the recipe could not read the tree it was handed: %q, %v", body, err)
				}
			}
			return "ok", 0
		})
		before := atomtest.ListTree(t, in.Root)
		atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass)
		if after := atomtest.ListTree(t, in.Root); strings.Join(after, ",") != strings.Join(before, ",") {
			t.Errorf("the shared tree changed: before %v, after %v", before, after)
		}
		if body, _ := os.ReadFile(filepath.Join(in.Root, "justfile")); string(body) != files["justfile"] {
			t.Errorf("a tracked file changed: %q", body)
		}
	})
	t.Run("a private tree that cannot be made is a could-not-run and the recipe does not run", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, files, func(c atoms.Cmd) (string, int) {
			if c.Name == "git" {
				return "fatal: no", 128
			}
			return "1.0", 0
		})
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the private tree the recipe runs in could not be made", "git init -q . exited 128")
		for _, c := range f.Calls {
			if c.Name == "just" && c.Args[0] == "validate" {
				t.Error("the recipe ran without its tree")
			}
		}
	})
	for name, tc := range map[string]struct {
		files map[string]string
		why   string
	}{
		"no WIT":                      {map[string]string{"justfile": "validate:\n\ttrue\n"}, "tracks no wit/*.wit"},
		"WIT and no justfile":         {map[string]string{"wit/a.wit": "x"}, "tracks WIT but no root justfile"},
		"a justfile with no validate": {map[string]string{"wit/a.wit": "x", "justfile": "build:\n\ttrue\n"}, "defines no validate recipe"},
	} {
		t.Run(name+" is absent and no tool is touched", func(t *testing.T) {
			in, f := atomtest.ToolTree(t, tc.files, nil)
			atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Absent, tc.why)
			if len(f.Calls) != 0 {
				t.Errorf("an absent atom touched %v", f.Ran())
			}
		})
	}
	t.Run("a justfile that would not read", func(t *testing.T) {
		in, _ := atomtest.ToolTree(t, files, nil)
		if err := os.Remove(filepath.Join(in.Root, "justfile")); err != nil {
			t.Fatal(err)
		}
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the justfile would not read")
	})
	t.Run("a population that failed", func(t *testing.T) {
		in, _ := atomtest.ToolTree(t, files, nil)
		in.FilesErr = atomtest.ErrBoom
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the tree would not enumerate")
	})
}
