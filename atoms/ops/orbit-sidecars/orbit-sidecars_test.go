package opsorbitsidecars

import (
	"context"
	"reflect"
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

// ops:orbit-sidecars is an atom of its own as well as a finding of
// orbit:sidecars: pass, findings, absent and could-not-run, through the exec seam.
func TestOpsOrbitSidecarsIsRegistered(t *testing.T) {
	opsTree := map[string]string{"prime/orbits/a.orbit.toml": "x", "flux/k.yaml": "a: 1\n"}
	for _, tc := range []struct {
		name    string
		tree    map[string]string
		code    int
		out     string
		state   int
		result  string
		needles []string
	}{
		{"every sidecar parses", opsTree, 0, "parsed 1", 0, atomtest.Pass, []string{"parsed 1"}},
		{"a sidecar its star would refuse to boot on", opsTree, 1, "a.orbit.toml: bad acl", 1, atomtest.Findings, []string{"a.orbit.toml: bad acl"}},
		{"a reader that would not start never ran", opsTree, -1, "orbitparse: not found", 2, atomtest.Cannot, []string{"the atom never ran: orbitparse: not found"}},
		{"a reader that fails above 2 is still a 2", opsTree, 7, "boom", 2, atomtest.Cannot, []string{"boom"}},
		{"a tree that renders none is absent", map[string]string{"flux/k.yaml": "a: 1\n"}, 0, "", 0, atomtest.Absent, []string{"tracks no composed orbit sidecar"}},
		{"a tree that is not an ops tree is absent", map[string]string{"main.go": "x"}, 0, "", 0, atomtest.Absent, []string{"has no ops shape"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := atomtest.ToolTree(t, tc.tree, func(atoms.Cmd) (string, int) { return tc.out, tc.code })
			atomtest.Expect(t, run(in), atomtest.StateOf(tc.state), tc.result, tc.needles...)
			if tc.result == atomtest.Absent && len(f.Calls) != 0 {
				t.Errorf("an absent atom ran %v", f.Ran())
			}
		})
	}
}

// The tool is handed every tracked sidecar, sorted, under the root - and nothing
// that is not one.
func TestTheReaderIsHandedEverySidecarAndNothingElse(t *testing.T) {
	in, f := atomtest.ToolTree(t, map[string]string{
		"clusters/home/kustomization.yaml":  "resources: []\n",
		"prime/orbits/kustomization.yaml":   "# owned\n",
		"prime/orbits/urania.orbit.toml":    "[[produces]]\n",
		"prime/orbits/athena.orbit.toml":    "[[consumes]]\n",
		"prime/orbits/notes.orbit.toml.bak": "x",
	}, func(atoms.Cmd) (string, int) { return "", 0 })
	atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass)
	want := []string{in.Root + "/prime/orbits/athena.orbit.toml", in.Root + "/prime/orbits/urania.orbit.toml"}
	called := f.Call("orbitparse")
	if !reflect.DeepEqual(called.Args, want) || !called.Both || called.Dir != in.Root {
		t.Errorf("the reader was run as %+v, want orbitparse %v with both streams in the root", called, want)
	}
}

func TestAnOpsTreeThatTracksNoSidecarSaysSo(t *testing.T) {
	in := atomtest.TreeIn(t, map[string]string{"prime/orbits/a.orbit.toml": "x", "flux/k.yaml": "a: 1\n"})
	in.Committable = []string{"flux/k.yaml"}
	atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Absent, "tracks no composed orbit sidecar")
}

func TestAScanThatFailedIsATwo(t *testing.T) {
	in := atomtest.MissingRoot(t)
	in.FilesErr = atomtest.ErrBoom
	atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the tree would not enumerate")
}
