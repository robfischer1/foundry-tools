package atoms

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// fakePy answers for python3: the provisioning probe, then the checker,
// recording each call.
type fakePy struct {
	probeCode   int
	checkerOut  string
	checkerCode int
	calls       []Cmd
}

func (f *fakePy) exec(_ context.Context, c Cmd) (string, int) {
	f.calls = append(f.calls, c)
	if c.Args[0] == "--version" {
		return "Python 3.14.0", f.probeCode
	}
	return f.checkerOut, f.checkerCode
}

func (f *fakePy) checker() Cmd { return f.calls[len(f.calls)-1] }

func ownerTree() map[string]string {
	return dieTree(map[string]string{
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

func TestDiesRefusalCodesAsTheRegistryOwner(t *testing.T) {
	const id = "dies:refusal-codes"
	for _, tc := range []struct {
		name    string
		files   map[string]string
		py      fakePy
		state   int
		result  string
		needles []string
	}{
		{"a checker that holds is a pass", ownerTree(), fakePy{checkerOut: "all codes registered"}, 0, pass, []string{"all codes registered"}},
		{"a checker that finds a defect is a finding, unmapped", ownerTree(), fakePy{checkerOut: "code X is undeclared", checkerCode: 1}, 1, findings,
			[]string{"code X is undeclared"}},
		{"a checker that could not run is a 2", ownerTree(), fakePy{checkerOut: "no door", checkerCode: 2}, 2, cannot, []string{"no door"}},
		{"an image without python never ran the checker", ownerTree(), fakePy{probeCode: 127}, 2, cannot, []string{"the atom never ran: python3 --version exited 127"}},
		{"no checker beside the die", dieTree(map[string]string{checks.RefusalRegistry: "x"}), fakePy{}, 2, cannot,
			[]string{checks.RefusalChecker + " is absent, so there is no checker to run"}},
		{"no registry beside the checker", dieTree(map[string]string{checks.RefusalChecker: "x"}), fakePy{}, 2, cannot,
			[]string{checks.RefusalRegistry + " is absent, so there is no registry to check"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := treeIn(t, tc.files)
			py := tc.py
			in.Exec = py.exec
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("the checker runs on the venv's python3, from the root, bytecode off, and its output is both streams", func(t *testing.T) {
		in := treeIn(t, ownerTree())
		py := &fakePy{}
		in.Exec = py.exec
		runAtom(t, id, in)
		c := py.checker()
		if want := checks.RefusalChecker; strings.Join(c.Args, " ") != want {
			t.Errorf("python3 ran as %q, want %q: nothing is resolved with --with", strings.Join(c.Args, " "), want)
		}
		if c.Name != "python3" || c.Dir != in.Root || !c.Both || !slices.Contains(c.Env, "PYTHONDONTWRITEBYTECODE=1") {
			t.Errorf("checker call %+v: want python3 in the root with both streams and no bytecode", c)
		}
	})
	t.Run("a registry that will not read", func(t *testing.T) {
		in := treeIn(t, dieTree(map[string]string{checks.RefusalChecker: "x", checks.RefusalRegistry + "/x": "x"}))
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the registry would not read")
	})
	t.Run("a registry with no remote use never asks the door", func(t *testing.T) {
		in := treeIn(t, ownerTree())
		in.Door = deadDoor(t)
		in.Exec = (&fakePy{checkerOut: "graded"}).exec
		expect(t, runAtom(t, id, in), stateOf(0), pass, "graded")
	})
	t.Run("a registry whose first remote use the door answers goes on to the checker", func(t *testing.T) {
		reg := "[[uses]]\nsource = { repo = \"rob/x\", path = \"a.py\" }\n"
		in := treeIn(t, dieTree(map[string]string{checks.RefusalChecker: "x", checks.RefusalRegistry: reg}))
		in.Door = doorOf(t, map[string]string{"rob/x a.py": "x"})
		py := &fakePy{checkerOut: "graded"}
		in.Exec = py.exec
		expect(t, runAtom(t, id, in), stateOf(0), pass, "graded")
	})
	t.Run("a registry whose first remote use the door cannot answer", func(t *testing.T) {
		reg := "[[uses]]\nsource = { repo = \"rob/x\", path = \"a.py\" }\n"
		in := treeIn(t, dieTree(map[string]string{checks.RefusalChecker: "x", checks.RefusalRegistry: reg}))
		in.Door = deadDoor(t)
		in.Exec = (&fakePy{}).exec
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the door's archive read is unreachable (rob/x:a.py)", "A use that could not be fetched")
	})
}

func TestDiesRefusalCodesAsASpellingTree(t *testing.T) {
	const id = "dies:refusal-codes"
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
			in := treeIn(t, tc.files)
			in.Door = doorOf(t, fleetOfFour())
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			py := &fakePy{checkerOut: "graded"}
			in.Exec = py.exec
			expect(t, runAtom(t, id, in), stateOf(0), pass, "graded")
			args := strings.Join(py.checker().Args, " ")
			for _, want := range []string{"--tree " + tc.tree + "=.", "--door " + in.Door.Base + "/archive", checks.RefusalChecker} {
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
		files   map[string]string
		door    map[string]string
		dead    bool
		needles []string
	}{
		{"a door nothing answers at", core, nil, true, []string{"the door's archive read is unreachable", "A registry that could not be fetched"}},
		{"a door that does not hold the checker", core, map[string]string{}, false, []string{"the door answered HTTP 404 for " + checks.RefusalRepo + ":" + checks.RefusalChecker}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := treeIn(t, tc.files)
			in.Door = doorOf(t, tc.door)
			if tc.dead {
				in.Door = deadDoor(t)
			}
			in.Exec = (&fakePy{}).exec
			expect(t, runAtom(t, id, in), stateOf(2), cannot, tc.needles...)
		})
	}
	t.Run("the registry fetched from the door is probed against the door", func(t *testing.T) {
		reg := "[[uses]]\nsource = { repo = \"rob/x\", path = \"a.py\" }\n"
		answers := fleetOfFour()
		answers[checks.RefusalRepo+" "+checks.RefusalRegistry] = reg
		in := treeIn(t, core)
		in.Door = doorBreaking(t, answers, "rob/x a.py")
		in.Exec = (&fakePy{}).exec
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the door's archive read is unreachable (rob/x:a.py)")
	})
	t.Run("a tree is stellar-core only by BOTH paths", func(t *testing.T) {
		for name, files := range map[string]map[string]string{
			"the WIT alone":   {"wit/aiws-result.wit": "x", "go.mod": "module git.notusmi.com/rob/hermes\n"},
			"the tapes alone": {"conformance/tapes/a.json": "{}", "go.mod": "module git.notusmi.com/rob/hermes\n"},
		} {
			in := treeIn(t, files)
			in.Door = doorOf(t, fleetOfFour())
			py := &fakePy{}
			in.Exec = py.exec
			runAtom(t, id, in)
			if args := strings.Join(py.checker().Args, " "); !strings.Contains(args, "--tree hermes=.") {
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
		in := treeIn(t, core)
		in.Door = doorOf(t, fleetOfFour())
		in.Exec = (&fakePy{}).exec
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the checker's inputs could not be placed")
	})
	t.Run("a go.mod that is nobody's is absent", func(t *testing.T) {
		in := treeIn(t, map[string]string{"go.mod": "module example.com/other\n"})
		expect(t, runAtom(t, id, in), stateOf(0), absent, "neither owns the refusal registry")
	})
	t.Run("no go.mod and no tapes is absent", func(t *testing.T) {
		expect(t, runAtom(t, id, treeIn(t, map[string]string{"a": "x"})), stateOf(0), absent, "no code to register")
	})
	t.Run("a go.mod that will not read is a 2", func(t *testing.T) {
		expect(t, runAtom(t, id, treeIn(t, map[string]string{"go.mod/x": "x"})), stateOf(2), cannot, "go.mod would not read")
	})
}
