package checks

import (
	"reflect"
	"strings"
	"testing"
)

func TestLanesOfReadsTheManifests(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string
		want    []Lane
	}{
		{"a go star", []string{"go.mod", "go.sum", "cmd", "internal"}, []Lane{LaneGo}},
		{"a python star", []string{"pyproject.toml", "uv.lock", "src", "tests"}, []Lane{LanePython}},
		{"a rust star", []string{"Cargo.toml", "Cargo.lock", "src"}, []Lane{LaneRust}},
		{"a bun star", []string{"package.json", "bun.lockb", "src"}, []Lane{LaneTS}},
		// themis and urania are both, and both lanes' atoms run on them.
		{"a mixed star", []string{"go.mod", "pyproject.toml"}, []Lane{LaneGo, LanePython}},
		{"an ops repo", []string{"Makefile", "ansible", "flux"}, nil},
		{"an empty tree", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := LanesOf(tc.entries)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("LanesOf(%v) = %v, want %v", tc.entries, got, tc.want)
			}
		})
	}
}

// ROOT-RELATIVE, deliberately. A vendored go.mod three directories down does
// not make a python star a Go repo, and a lane check that fired on one would
// report on a repo with nothing to check.
func TestALaneIsDeclaredAtTheRootOnly(t *testing.T) {
	entries := []string{"src", "tests", "pyproject.toml"}
	if DeclaresLane(entries, LaneGo) {
		t.Fatal("no go.mod at the root, so the Go lane is not declared")
	}
	if !DeclaresLane(entries, LanePython) {
		t.Fatal("pyproject.toml at the root declares the Python lane")
	}
}

// LaneAny is declared by every repository, including one with nothing in it —
// the cross-lane atoms have no manifest to look for.
func TestLaneAnyIsAlwaysDeclared(t *testing.T) {
	if !DeclaresLane(nil, LaneAny) {
		t.Fatal("the cross-lane atoms run everywhere, including an empty tree")
	}
}

func TestLanesOfIsDeterministic(t *testing.T) {
	a := LanesOf([]string{"package.json", "Cargo.toml", "pyproject.toml", "go.mod"})
	b := LanesOf([]string{"go.mod", "pyproject.toml", "Cargo.toml", "package.json"})
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("lane order must not depend on directory order: %v vs %v", a, b)
	}
	want := []Lane{LaneGo, LanePython, LaneRust, LaneTS}
	if !reflect.DeepEqual(a, want) {
		t.Fatalf("LanesOf = %v, want %v", a, want)
	}
}

func TestManifestForNamesTheDeclaringFile(t *testing.T) {
	for lane, want := range map[Lane]string{
		LaneGo:     "go.mod",
		LanePython: "pyproject.toml",
		LaneRust:   "Cargo.toml",
		LaneTS:     "package.json",
		LaneAny:    "",
	} {
		if got := ManifestFor(lane); got != want {
			t.Errorf("ManifestFor(%q) = %q, want %q", lane, got, want)
		}
	}
}

// The catalogue is the one definition. If a row is malformed, `dagger check -l`
// and the nereus rows disagree about what the fleet runs — the two-surface
// defect this repository exists to delete.
func TestEveryAtomIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	lanes := map[Lane]bool{LaneAny: true, LaneGo: true, LanePython: true, LaneRust: true, LaneTS: true}
	stages := map[string]bool{StagePrecommit: true, StagePrepush: true, StageMutation: true, StageOrbit: true, StageVisual: true}

	for _, a := range Atoms {
		if seen[a.ID] {
			t.Errorf("duplicate atom id %q", a.ID)
		}
		seen[a.ID] = true

		if !strings.Contains(a.ID, ":") {
			t.Errorf("atom %q is not namespaced", a.ID)
		}
		if a.Lane != LaneAny && !strings.HasPrefix(a.ID, string(a.Lane)+":") {
			t.Errorf("atom %q sits in lane %q; its id must carry that namespace", a.ID, a.Lane)
		}
		// A cross-lane atom is namespaced by fleet: — it has something to say
		// about every repository — or by the SURFACE it finds for itself, from
		// the closed set in checks.SurfaceNamespaces. (The third arm, sweep: on
		// the clock, went with StageSweep on 2026-09-23.)
		//
		// The third case was added 2026-09-10 with the compose: and dies:
		// ports, and it is a widening of exactly one clause. The rule used to
		// read "by WHEN it runs, because that is the only thing left to
		// namespace it by"; that premise held while fleet: meant both "runs
		// everywhere" and "cross-lane", and an atom whose surface is a tracked
		// compose spec is cross-lane without having anything to say about
		// every repository. See SurfaceNamespaces for why filing those under
		// fleet: would have been the defect rather than the discipline.
		//
		// WHAT IS NOT WIDENED: the set of surface namespaces is closed — an id
		// in an undeclared namespace still fails here.
		if a.Lane == LaneAny {
			switch {
			case strings.HasPrefix(a.ID, "fleet:"):
			case IsSurfaceNamespace(a.ID):
			default:
				t.Errorf("cross-lane %s atom %q must be namespaced fleet: or carry a declared surface namespace (%v)", a.Stage, a.ID, SurfaceNamespaces)
			}
		}
		if !lanes[a.Lane] {
			t.Errorf("atom %q declares unknown lane %q", a.ID, a.Lane)
		}
		if !stages[a.Stage] {
			t.Errorf("atom %q declares unknown stage %q", a.ID, a.Stage)
		}
		if a.Image == "" {
			t.Errorf("atom %q names no lane image", a.ID)
		}
		if strings.TrimSpace(a.Desc) == "" {
			t.Errorf("atom %q has no description — `dagger check -l` would list it unexplained", a.ID)
		}
	}
}

func TestAtomByIDPanicsOnAnUnknownID(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("an unknown atom id must not resolve silently")
		}
	}()
	AtomByID("go:nope")
}
