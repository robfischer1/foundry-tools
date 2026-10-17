package checks

import (
	"slices"
	"strings"
	"testing"
)

// THE FORK LANE, PINNED BY NAME. A fork of an upstream (record kind vendored)
// keeps what proves its code works and is safe, and stands down what holds
// upstream-owned code to the fleet's own house rules. A row that gains or
// loses NotOnForks changes what every fork is graded by, so the set is
// spelled out here rather than derived. The atoms that live in their own files
// are pinned in internal/catalogue, which sees them; this sees the table alone.
var forkInert = map[string]string{}

func TestTheForkLaneIsTheCatalogueRowsThatSaySo(t *testing.T) {
	got := map[string]string{}
	for _, a := range Atoms() {
		if a.NotOnForks == "" {
			continue
		}
		got[a.ID] = a.NotOnForks
		if !slices.Contains(ForkReasons, a.NotOnForks) {
			t.Errorf("%s: NotOnForks %q is not one of ForkReasons", a.ID, a.NotOnForks)
		}
	}
	for id, why := range forkInert {
		if got[id] != why {
			t.Errorf("%s: NotOnForks %q, want %q", id, got[id], why)
		}
	}
	for id := range got {
		if _, ok := forkInert[id]; !ok {
			t.Errorf("%s stands down on a fork and the pin does not say so", id)
		}
	}
}

// WHAT A FORK ALWAYS KEEPS: the build, the test suites, the audits, the
// release builds, SAST, and cargo fmt — which upstreams enforce as the same
// command (arcjet/gravity's ci.yml runs `cargo fmt --all --check`).
func TestAForkKeepsItsBuildTestsAuditsAndSAST(t *testing.T) {
	for _, id := range []string{
		"rust:release",
		"go:release",
		"python:release",
		"ts:release",
	} {
		if a := AtomByID(id); a.NotOnForks != "" {
			t.Errorf("%s stands down on a fork (%q); a fork keeps it", id, a.NotOnForks)
		}
	}
}

func TestPlanAtomsStandsDownWhatAForkDoesNotTake(t *testing.T) {
	sel := []AtomDef{
		{ID: "fleet:check-yaml", Lane: LaneAny},
		{ID: "fleet:stop-justifications", Lane: LaneAny, NotOnForks: ForkFleetStyle},
		{ID: "rust:cargo-test", Lane: LaneRust},
		{ID: "rust:cargo-clippy", Lane: LaneRust, NotOnForks: ForkFleetStyle},
		{ID: "go:staticcheck", Lane: LaneGo, NotOnForks: ForkFleetStyle},
	}
	tree := Tree{Entries: []string{"Cargo.toml"}, Fork: "gravity"}
	p := PlanAtoms(sel, tree)
	if planIDs(p.Run) != "fleet:check-yaml,rust:cargo-test" {
		t.Errorf("run %s", planIDs(p.Run))
	}
	if planIDs(p.Fork) != "fleet:stop-justifications,rust:cargo-clippy" {
		t.Errorf("fork %s", planIDs(p.Fork))
	}
	// THE LANE'S ABSENCE COMES FIRST: no Go here, so staticcheck says that,
	// whoever owns the code.
	if planIDs(p.Absent) != "go:staticcheck" {
		t.Errorf("absent %s", planIDs(p.Absent))
	}

	// The same tree, authored by the fleet: everything with a surface runs.
	tree.Fork = ""
	p = PlanAtoms(sel, tree)
	if planIDs(p.Run) != "fleet:check-yaml,fleet:stop-justifications,rust:cargo-test,rust:cargo-clippy" || len(p.Fork) != 0 {
		t.Errorf("a fleet repo: run %s, fork %s", planIDs(p.Run), planIDs(p.Fork))
	}
}

func TestForkVerdictIsAnAbsenceThatNamesTheRecordAndWhy(t *testing.T) {
	a := AtomDef{ID: "rust:cargo-clippy", Stage: StagePrecommit, Lane: LaneRust, NotOnForks: ForkFleetStyle}
	v := ForkVerdict(a, "gravity")
	if v.State != int(StatePass) || v.Result != "absent" || v.Logs == nil || len(v.Logs) != 0 {
		t.Errorf("%+v", v)
	}
	for _, want := range []string{"rust:cargo-clippy: ABSENT", "fleet/repos/gravity.json is kind vendored", ForkFleetStyle} {
		if !strings.Contains(v.Reason, want) {
			t.Errorf("reason %q lacks %q", v.Reason, want)
		}
	}
	if v.Atom != a.ID || v.Stage != a.Stage || v.Lane != string(a.Lane) {
		t.Errorf("%+v", v)
	}
}

func TestRecordIsFork(t *testing.T) {
	for slag, want := range map[string]bool{
		`{"name":"gravity","kind":"vendored","repo":"forks/gravity"}`: true,
		`{"name":"tongs","kind":"rust","repo":"rob/tongs"}`:           false,
		`{"name":"x"}`: false,
		`not json`:     false,
		``:             false,
	} {
		if got := RecordIsFork(slag); got != want {
			t.Errorf("RecordIsFork(%s) = %v, want %v", slag, got, want)
		}
	}
}
