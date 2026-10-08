package atoms

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// pyTree is toolTree for the atoms that run python: `python3 --version` (the
// provisioning probe) is answered by the venv, and every other call by the
// script. A nil script answers 0 and nothing.
func pyTree(t *testing.T, files map[string]string, answer func(Cmd) (string, int)) (Input, *toolFake) {
	t.Helper()
	return toolTree(t, files, func(c Cmd) (string, int) {
		if c.Name == "python3" && len(c.Args) == 1 && c.Args[0] == "--version" {
			return "Python 3.14.0", 0
		}
		if answer == nil {
			return "", 0
		}
		return answer(c)
	})
}

// lastCall is the call the atom made last.
func (f *toolFake) lastCall(t *testing.T) Cmd {
	t.Helper()
	if len(f.calls) == 0 {
		t.Fatal("no program was exec'd")
	}
	return f.calls[len(f.calls)-1]
}

// wantPython holds a call to the shape every python atom runs in: python3, from
// the root, both streams, bytecode off.
func wantPython(t *testing.T, c Cmd, root string, args ...string) {
	t.Helper()
	if c.Name != "python3" || c.Dir != root || !c.Both || !slices.Contains(c.Env, "PYTHONDONTWRITEBYTECODE=1") {
		t.Errorf("call %+v: want python3 in %s with both streams and no bytecode", c, root)
	}
	if got := strings.Join(c.Args, " "); got != strings.Join(args, " ") {
		t.Errorf("python3 ran as %q, want %q: nothing is resolved with --with", got, strings.Join(args, " "))
	}
}

func TestFleetWitTopics(t *testing.T) {
	const id = "fleet:wit-topics"
	flux := map[string]string{"prime/orbits/a.yaml": "x", witTopicsChecker: "#!/usr/bin/env python3\n"}
	for _, tc := range []struct {
		name    string
		code    int
		out     string
		state   int
		result  string
		needles []string
	}{
		{"a checker that holds is a pass", 0, "wit-topics: redpanda/wit matches redpanda/schemas", 0, pass, []string{"redpanda/wit matches"}},
		{"stale is the checker's exit 1, unmapped", 1, "wit-topics: STALE - aiws-topics.wit", 1, findings, []string{"STALE - aiws-topics.wit"}},
		{"could not run is the checker's exit 2, unmapped", 2, "wit-topics: COULD NOT RUN - no schemas", 2, cannot, []string{"COULD NOT RUN - no schemas"}},
		{"a python3 that would not start never ran", -1, "python3: gone", 2, cannot, []string{"the atom never ran: python3: gone"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := pyTree(t, flux, func(Cmd) (string, int) { return tc.out, tc.code })
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
			wantPython(t, f.lastCall(t), in.Root, witTopicsChecker)
		})
	}
	t.Run("a container without the venv never ran the checker", func(t *testing.T) {
		in, f := toolTree(t, flux, func(Cmd) (string, int) { return "python3: not found", 127 })
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the atom never ran: python3 --version exited 127: python3: not found")
		if len(f.calls) != 1 {
			t.Errorf("the checker ran after a failed probe: %v", f.ran())
		}
	})
	t.Run("a tree with no prime/ is not flux's: absent, and python is not touched", func(t *testing.T) {
		in, f := pyTree(t, map[string]string{"a.txt": "x", witTopicsChecker: "x"}, nil)
		expect(t, runAtom(t, id, in), stateOf(0), absent, "carries no prime/", "not the fleet's flux tree")
		if len(f.calls) != 0 {
			t.Errorf("an absent atom touched %v", f.ran())
		}
	})
	t.Run("flux's tree without the checker is a could-not-run naming it, never an absence", func(t *testing.T) {
		in, f := pyTree(t, map[string]string{"prime/orbits/a.yaml": "x"}, nil)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, witTopicsChecker+" is absent, so there is no checker to run")
		if len(f.calls) != 0 {
			t.Errorf("python ran with no checker: %v", f.ran())
		}
	})
	t.Run("a root that cannot be read", func(t *testing.T) {
		expect(t, runAtom(t, id, missingRoot(t)), stateOf(2), cannot, "the tree would not enumerate")
	})
}

// EVERY PROGRAM A PYTHON ATOM EXECS IS IN Programs, the list the module's
// container test holds the layers to: python3 and python from the venv, the entry
// points it installs, git for the private tree. An atom that execs a program no
// layer carries is a could-not-run on every shadow, and looks like a finding
// about the binary.
func TestEveryPythonAtomExecsAListedProgram(t *testing.T) {
	provision(t, nil)
	r := &renderer{t: t, render: okRender()}
	star := map[string]string{"vendor/a.json": "{}", "main.go": "x"}
	checkers := dieTree(map[string]string{
		"tools/check_findings.py": "x", "schema/findings.schema.json": "{}", "tools/check_schemas.py": "x",
		"tools/check_wit_regenerated.py": "x", "tools/check_schema_rendered.py": "x",
		"schema/slag.schema.json": "{}", "schema/slag-v3.schema.json": "{}",
	})
	for _, tc := range []struct {
		id     string
		files  map[string]string
		door   bool
		answer func(Cmd) (string, int)
	}{
		{"fleet:wit-topics", map[string]string{"prime/a.yaml": "x", witTopicsChecker: "x"}, false, nil},
		{"dies:contracts", contractsTree(), false, fixtureAnswer(0, "")},
		{"dies:contract-copies", star, true, nil},
		{"dies:schema", checkers, false, nil},
		{"dies:findings", checkers, false, nil},
		{"dies:schemas", checkers, false, nil},
		{"dies:wit-regenerated", checkers, false, nil},
		{"dies:schema-rendered", checkers, false, nil},
		{"dies:refusal-codes", ownerTree(), false, nil},
		{"ops:dup", map[string]string{"flux/x.yaml": "a: 1\n", "tools/dup-check": "x"}, false, nil},
		{"ops:declaration", map[string]string{"flux/x.yaml": "a: 1\n", "tools/declaration-integrity": "x"}, false, nil},
		{"ops:metrics", map[string]string{"flux/x.yaml": "a: 1\n", "tools/metric-allowlist": "x"}, false, nil},
		{"ops:ansible", ansibleFiles(), false, nil},
		{"template:render-matrix", templateFiles(matrixTOML), false, r.answer},
	} {
		t.Run(tc.id, func(t *testing.T) {
			in, f := toolTree(t, tc.files, opsPyAnswer(tc.answer))
			if tc.id == "ops:dup" {
				if err := os.Chmod(filepath.Join(in.Root, "tools/dup-check"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.door {
				in.Door = copiesDoor(t)
			}
			if v := runAtom(t, tc.id, in); v.Result == absent {
				t.Fatalf("%s was absent on a tree built to run it: %s", tc.id, v.Reason)
			}
			if len(f.calls) == 0 {
				t.Fatal("no program was exec'd")
			}
			for _, c := range f.calls {
				if !slices.Contains(Programs, c.Name) {
					t.Errorf("%s exec'd %q, which Programs does not list", tc.id, c.Name)
				}
			}
		})
	}
}
