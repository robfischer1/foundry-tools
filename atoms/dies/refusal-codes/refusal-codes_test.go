package diesrefusalcodes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// TestUT_485_TheDeclarationIsTheRowItReplaced: the Def carries the identity the
// catalogue row did, and validates.
func TestUT_485_TheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "python" || Def.Surface != "any:**" || strings.Join(Def.Needs, ",") != "tool:uv" {
		t.Errorf("%+v", Def)
	}
}

func ownerTree() map[string]string {
	return atomtest.DieTree(map[string]string{
		checks.RefusalChecker:  "# checker",
		checks.RefusalRegistry: "[codes.x]\n",
	})
}

// fleetOfFour is the door's answer for the checker, its two imports and the
// registry: what a spelling tree fetches.
func fleetOfFour() map[string]string {
	out := map[string]string{}
	for _, p := range checks.RefusalCheckerFiles() {
		out[checks.RefusalRepo+" "+p] = "# " + p
	}
	out[checks.RefusalRepo+" "+checks.RefusalRegistry] = "[codes.x]\n"
	return out
}

// answering is a script whose checker says out and exits code.
func answering(out string, code int) func(atoms.Cmd) (string, int) {
	return func(atoms.Cmd) (string, int) { return out, code }
}

func TestAsTheRegistryOwner(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		answer  func(atoms.Cmd) (string, int)
		state   checks.State
		result  string
		needles []string
	}{
		{"a checker that holds is a pass", ownerTree(), answering("all codes registered", 0), checks.StatePass, atomtest.Pass, []string{"all codes registered"}},
		{"a checker that finds a defect is a finding, unmapped", ownerTree(), answering("code X is undeclared", 1), checks.StateFindings, atomtest.Findings,
			[]string{"code X is undeclared"}},
		{"a checker that could not run is a 2", ownerTree(), answering("no door", 2), checks.StateCannotRun, atomtest.Cannot, []string{"no door"}},
		{"no checker beside the die", atomtest.DieTree(map[string]string{checks.RefusalRegistry: "x"}), nil, checks.StateCannotRun, atomtest.Cannot,
			[]string{checks.RefusalChecker + " is absent, so there is no checker to run"}},
		{"no registry beside the checker", atomtest.DieTree(map[string]string{checks.RefusalChecker: "x"}), nil, checks.StateCannotRun, atomtest.Cannot,
			[]string{checks.RefusalRegistry + " is absent, so there is no registry to check"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, _ := atomtest.UVTree(t, tc.files, tc.answer)
			atomtest.Expect(t, check(context.Background(), in), tc.state, tc.result, tc.needles...)
		})
	}
	t.Run("an image without uv never ran the checker", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, ownerTree(), answering("gone", 127))
		f.Free = []string{"uv"}
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the atom never ran: exit code: 127", "failed: uv --version")
	})
	t.Run("the checker runs under uv with tomli, from the root, bytecode off, and its output is both streams", func(t *testing.T) {
		in, f := atomtest.UVTree(t, ownerTree(), nil)
		check(context.Background(), in)
		atomtest.WantUV(t, f.Last(), in.Root, dieskit.WithTOML, checks.RefusalChecker)
	})
	t.Run("a registry that will not read", func(t *testing.T) {
		in := atomtest.TreeIn(t, atomtest.DieTree(map[string]string{checks.RefusalChecker: "x", checks.RefusalRegistry + "/x": "x"}))
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the registry would not read")
	})
	t.Run("a registry with no remote use never asks the door", func(t *testing.T) {
		in, _ := atomtest.UVTree(t, ownerTree(), answering("graded", 0))
		in.Door = atomtest.DeadDoor(t)
		atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass, "graded")
	})
	t.Run("a registry whose first remote use the door answers goes on to the checker", func(t *testing.T) {
		reg := "[[uses]]\nsource = { repo = \"rob/x\", path = \"a.py\" }\n"
		in, _ := atomtest.UVTree(t, atomtest.DieTree(map[string]string{checks.RefusalChecker: "x", checks.RefusalRegistry: reg}), answering("graded", 0))
		in.Door = atomtest.DoorOf(t, map[string]string{"rob/x a.py": "x"})
		atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass, "graded")
	})
	t.Run("a registry whose first remote use the door cannot answer", func(t *testing.T) {
		reg := "[[uses]]\nsource = { repo = \"rob/x\", path = \"a.py\" }\n"
		in, _ := atomtest.UVTree(t, atomtest.DieTree(map[string]string{checks.RefusalChecker: "x", checks.RefusalRegistry: reg}), nil)
		in.Door = atomtest.DeadDoor(t)
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the door's archive read is unreachable (rob/x:a.py)", "A use that could not be fetched")
	})
}

func TestAsASpellingTree(t *testing.T) {
	core := map[string]string{"wit/aiws-result.wit": "x", "conformance/tapes/a.json": "{}"}
	for _, tc := range []struct {
		name  string
		files map[string]string
		tree  string
	}{
		{"stellar-core by the two paths it spells its codes on", core, "stellar-core"},
		{"hermes by the module line of its go.mod", map[string]string{"go.mod": "module git.notusmi.com/rob/hermes\n\ngo 1.25\n"}, "hermes"},
		{"daedalus by the module line of its go.mod", map[string]string{"go.mod": "module git.notusmi.com/rob/daedalus\n"}, "daedalus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := atomtest.UVTree(t, tc.files, answering("graded", 0))
			in.Door = atomtest.DoorOf(t, fleetOfFour())
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass, "graded")
			args := strings.Join(f.Last().Args, " ")
			for _, want := range []string{"--with " + dieskit.WithTOML, "--tree " + tc.tree + "=.", "--door " + in.Door.Base + "/archive", checks.RefusalChecker} {
				if !strings.Contains(args, want) {
					t.Errorf("the checker ran as %q, lacking %q", args, want)
				}
			}
			if left, _ := os.ReadDir(tmp); len(left) != 0 {
				t.Errorf("the fetched checker was left behind in the temp directory: %v", left)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		door    map[string]string
		dead    bool
		needles []string
	}{
		{"a door nothing answers at", nil, true, []string{"the door's archive read is unreachable", "A registry that could not be fetched"}},
		{"a door that does not hold the checker", map[string]string{}, false, []string{"the door answered HTTP 404 for " + checks.RefusalRepo + ":" + checks.RefusalChecker}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, _ := atomtest.UVTree(t, core, nil)
			in.Door = atomtest.DoorOf(t, tc.door)
			if tc.dead {
				in.Door = atomtest.DeadDoor(t)
			}
			atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, tc.needles...)
		})
	}
	t.Run("the registry fetched from the door is probed against the door", func(t *testing.T) {
		reg := "[[uses]]\nsource = { repo = \"rob/x\", path = \"a.py\" }\n"
		answers := fleetOfFour()
		answers[checks.RefusalRepo+" "+checks.RefusalRegistry] = reg
		in, _ := atomtest.UVTree(t, core, nil)
		in.Door = atomtest.DoorBreaking(t, answers, "rob/x a.py")
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the door's archive read is unreachable (rob/x:a.py)")
	})
	t.Run("a tree is stellar-core only by BOTH paths", func(t *testing.T) {
		for name, files := range map[string]map[string]string{
			"the WIT alone":   {"wit/aiws-result.wit": "x", "go.mod": "module git.notusmi.com/rob/hermes\n"},
			"the tapes alone": {"conformance/tapes/a.json": "{}", "go.mod": "module git.notusmi.com/rob/hermes\n"},
		} {
			in, f := atomtest.UVTree(t, files, nil)
			in.Door = atomtest.DoorOf(t, fleetOfFour())
			check(context.Background(), in)
			if args := strings.Join(f.Last().Args, " "); !strings.Contains(args, "--tree hermes=.") {
				t.Errorf("%s: graded as %q, want hermes", name, args)
			}
		}
	})
	t.Run("the fetched files cannot be placed", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMPDIR", blocker)
		in, _ := atomtest.UVTree(t, core, nil)
		in.Door = atomtest.DoorOf(t, fleetOfFour())
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the checker's inputs could not be placed")
	})
	t.Run("a go.mod that is nobody's is absent", func(t *testing.T) {
		atomtest.Expect(t, check(context.Background(), atomtest.TreeIn(t, map[string]string{"go.mod": "module example.com/other\n"})),
			checks.StatePass, atomtest.Absent, "neither owns the refusal registry")
	})
	t.Run("no go.mod and no tapes is absent", func(t *testing.T) {
		atomtest.Expect(t, check(context.Background(), atomtest.TreeIn(t, map[string]string{"a": "x"})), checks.StatePass, atomtest.Absent, "no code to register")
	})
	t.Run("a go.mod that will not read is a 2", func(t *testing.T) {
		atomtest.Expect(t, check(context.Background(), atomtest.TreeIn(t, map[string]string{"go.mod/x": "x"})), checks.StateCannotRun, atomtest.Cannot, "go.mod would not read")
	})
	t.Run("a shape that could not be read is not an answer", func(t *testing.T) {
		atomtest.Expect(t, check(context.Background(), atomtest.MissingRoot(t)), checks.StateCannotRun, atomtest.Cannot, "the repository root could not be read")
	})
}
