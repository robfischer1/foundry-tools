package checks

import (
	"strings"
	"testing"
)

func planIDs(as []AtomDef) string {
	var out []string
	for _, a := range as {
		out = append(out, a.ID)
	}
	return strings.Join(out, ",")
}

func TestPlanAtomsAdmitsWhatTheTreeDeclares(t *testing.T) {
	sel := []AtomDef{
		{ID: "fleet:check-yaml", Lane: LaneAny},
		{ID: "go:vet", Lane: LaneGo},
		{ID: "python:mypy", Lane: LanePython},
		{ID: "ops:flux", Lane: LaneAny},
	}
	p := PlanAtoms(sel, Tree{Entries: []string{"pyproject.toml"}})
	if planIDs(p.Run) != "fleet:check-yaml,python:mypy,ops:flux" {
		t.Errorf("run %s", planIDs(p.Run))
	}
	if planIDs(p.Absent) != "go:vet" {
		t.Errorf("absent %s", planIDs(p.Absent))
	}
	if len(p.Lanes) != 1 || p.Lanes[0] != LanePython {
		t.Errorf("lanes %v: a tree with a pyproject.toml and no go.mod is the python lane alone", p.Lanes)
	}
}

// THE GO LANE IS NOT A ROOT ENTRY. A module anywhere in the tree declares it,
// and a root go.mod that the module walk did not find does not.
func TestPlanAtomsReadsTheGoLaneFromItsModuleCount(t *testing.T) {
	sel := []AtomDef{{ID: "go:vet", Lane: LaneGo}}
	if p := PlanAtoms(sel, Tree{GoModules: 2}); planIDs(p.Run) != "go:vet" || !hasLane(p.Lanes, LaneGo) {
		t.Errorf("two modules and no root entry is the go lane: %+v", p)
	}
	if p := PlanAtoms(sel, Tree{Entries: []string{"go.mod"}}); planIDs(p.Absent) != "go:vet" || hasLane(p.Lanes, LaneGo) {
		t.Errorf("a root go.mod the module walk did not count is not the go lane: %+v", p)
	}
	// The other lanes are untouched by the module count.
	if p := PlanAtoms([]AtomDef{{ID: "rust:cargo-fmt", Lane: LaneRust}}, Tree{Entries: []string{"Cargo.toml"}}); planIDs(p.Run) != "rust:cargo-fmt" {
		t.Errorf("the rust lane does not depend on go modules: %+v", p)
	}
}

func TestPlanAtomsOnAnEmptyTreeRunsOnlyWhatRunsEverywhere(t *testing.T) {
	sel := []AtomDef{{ID: "fleet:hadolint", Lane: LaneAny}, {ID: "ts:bun-gate", Lane: LaneTS}}
	p := PlanAtoms(sel, Tree{})
	if planIDs(p.Run) != "fleet:hadolint" || planIDs(p.Absent) != "ts:bun-gate" || len(p.Lanes) != 0 {
		t.Errorf("%+v", p)
	}
}

// THE PYTHON LANE IS DECLARED BY ITS FILES TOO, as of 2026-09-25. A .py
// anywhere the repository owns admits the lane's lint and test, exactly as a
// go.mod anywhere admits go's — the manifest stopped being the only way in.
func TestPlanAtomsReadsThePythonLaneFromItsFiles(t *testing.T) {
	sel := []AtomDef{{ID: "python:ruff-check", Lane: LanePython}}
	if p := PlanAtoms(sel, Tree{PythonFiles: 1}); planIDs(p.Run) != "python:ruff-check" || !hasLane(p.Lanes, LanePython) {
		t.Errorf("one .py and no manifest is the python lane: %+v", p)
	}
	if p := PlanAtoms(sel, Tree{}); planIDs(p.Absent) != "python:ruff-check" || hasLane(p.Lanes, LanePython) {
		t.Errorf("no .py and no manifest is no python lane: %+v", p)
	}
	// ADDITIVE, NEVER SUBTRACTIVE — the asymmetry with go. A star that has
	// taken its manifest but not yet written a .py still declares the lane,
	// so python:release does not vanish mid-scaffold.
	if p := PlanAtoms(sel, Tree{Entries: []string{"pyproject.toml"}}); !hasLane(p.Lanes, LanePython) {
		t.Errorf("a manifest with no .py yet is still the python lane: %+v", p)
	}
}

// A BUILD ATOM STANDS DOWN INSIDE A LANE THAT IS RUNNING. The two questions
// are not one: the files admit lint and test, the manifest admits the build.
func TestPlanAtomsHoldsBuildAtomsToTheRootManifest(t *testing.T) {
	sel := []AtomDef{
		{ID: "python:ruff-check", Lane: LanePython},
		{ID: "python:release", Lane: LanePython, NeedsManifest: true},
	}
	p := PlanAtoms(sel, Tree{PythonFiles: 3})
	if planIDs(p.Run) != "python:ruff-check" {
		t.Errorf("lint runs on files alone: %s", planIDs(p.Run))
	}
	if planIDs(p.Absent) != "python:release" {
		t.Errorf("the build waits for the manifest: %s", planIDs(p.Absent))
	}
	// With the manifest, both run — the flag is a requirement, not a ban.
	both := PlanAtoms(sel, Tree{Entries: []string{"pyproject.toml"}, PythonFiles: 3})
	if planIDs(both.Run) != "python:ruff-check,python:release" {
		t.Errorf("a manifest admits the build: %s", planIDs(both.Run))
	}
	// And a build atom whose lane is absent entirely is absent for that
	// reason, not for the manifest's — the verdicts say different things.
	if p := PlanAtoms(sel, Tree{}); planIDs(p.Absent) != "python:ruff-check,python:release" {
		t.Errorf("no lane at all: %s", planIDs(p.Absent))
	}
}

// Dagger spells a root entry with and without a leading "./" depending on the
// call. Both are the same fact about the repository.
func TestDeclaresManifestAcceptsEitherSpelling(t *testing.T) {
	for _, spelling := range []string{"pyproject.toml", "./pyproject.toml"} {
		if !DeclaresManifest([]string{spelling}, "pyproject.toml") {
			t.Errorf("%q is a root pyproject.toml", spelling)
		}
	}
	if DeclaresManifest([]string{"src/pyproject.toml"}, "pyproject.toml") {
		t.Error("a pyproject.toml one directory down is not the ROOT manifest")
	}
	if DeclaresManifest([]string{"pyproject.toml"}, "") {
		t.Error("a lane with no manifest is never declared by one")
	}
}

// THE LANES COME BACK SORTED whichever order they were assembled in. Go is
// removed and re-appended off the module count and python is appended off the
// file count, so the assembly order is an accident of the tree — and a stage
// that printed its lanes in that order would change its own output when a repo
// gained a .py.
func TestLanesOfTreeAnswersInASettledOrder(t *testing.T) {
	// Assembled go-last (appended off GoModules) and python-last (appended off
	// PythonFiles); both must come back go, python.
	tree := Tree{Entries: []string{"Cargo.toml"}, GoModules: 1, PythonFiles: 1}
	got := LanesOfTree(tree)
	var names []string
	for _, l := range got {
		names = append(names, string(l))
	}
	if strings.Join(names, ",") != "go,python,rust" {
		t.Errorf("got %v, want go,python,rust — sorted, not assembly order", names)
	}
}
