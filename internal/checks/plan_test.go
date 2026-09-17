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
	p := PlanAtoms(sel, []string{"pyproject.toml"}, 0)
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
	if p := PlanAtoms(sel, nil, 2); planIDs(p.Run) != "go:vet" || !hasLane(p.Lanes, LaneGo) {
		t.Errorf("two modules and no root entry is the go lane: %+v", p)
	}
	if p := PlanAtoms(sel, []string{"go.mod"}, 0); planIDs(p.Absent) != "go:vet" || hasLane(p.Lanes, LaneGo) {
		t.Errorf("a root go.mod the module walk did not count is not the go lane: %+v", p)
	}
	// The other lanes are untouched by the module count.
	if p := PlanAtoms([]AtomDef{{ID: "rust:cargo-fmt", Lane: LaneRust}}, []string{"Cargo.toml"}, 0); planIDs(p.Run) != "rust:cargo-fmt" {
		t.Errorf("the rust lane does not depend on go modules: %+v", p)
	}
}

func TestPlanAtomsOnAnEmptyTreeRunsOnlyWhatRunsEverywhere(t *testing.T) {
	sel := []AtomDef{{ID: "fleet:detect-secrets", Lane: LaneAny}, {ID: "ts:bun-gate", Lane: LaneTS}}
	p := PlanAtoms(sel, nil, 0)
	if planIDs(p.Run) != "fleet:detect-secrets" || planIDs(p.Absent) != "ts:bun-gate" || len(p.Lanes) != 0 {
		t.Errorf("%+v", p)
	}
}
