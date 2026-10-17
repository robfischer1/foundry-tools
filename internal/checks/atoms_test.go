package checks

import (
	"slices"
	"strings"
	"testing"
)

// What this file holds is what is still a fact about the TABLE. Everything an
// atom's body used to be asserted with — `sh -n` over the script, a grep for a
// flag, a grep for a prelude — moved with the body: the judgements are pure
// functions in this package with their own table tests, and the two-surface
// guard the `sh -n` pass stood in for is registry_test.go, which reads the
// register() calls out of atoms_*.go.

// THE MOUNT IS GONE AND SO IS THE INVARIANT THAT GUARDED IT. This was
// TestTheRulesetAtomsAskForTheStocksMount: the linting atoms read the fleet's
// rulesets off a foundry-stocks mount, and one that pointed at RulesetsDir
// without declaring NeedsStocks answered CANNOT RUN in every repository.
//
// The rulesets are embedded in this module now (CA F18,
// internal/checks/rulesets), so there is no mount to forget and no declaration
// to omit. The failure this protected against cannot be written.
//
// What CAN still be written is the pairing — an atom that writes ruff.toml and
// then points mypy at it. That is asserted over the real chains in the root
// package (TestEachRulesetAtomReadsTheFileItWrote), not here, because it is a
// fact about the containers the atoms build rather than about the catalogue.

// (The go test atoms' record tree and the unit suite's subsumption are facts about
// the catalogue as the whole module sees it, rows of atoms in files included:
// internal/catalogue/golane_test.go holds them.)

// The mutation stage is four atoms, one per lane, and it is asked for BY NAME:
// the default vector (a pull's gate) must never carry one, and neither may the
// sweep. Each runs the canonical script at its one home, so each must mount
// foundry-stocks.
func TestTheMutationStageIsFourLanesAskedForByName(t *testing.T) {
	got := MutationAtoms()
	want := map[string]Lane{"go:mutation": LaneGo, "python:mutation": LanePython, "rust:mutation": LaneRust, "ts:mutation": LaneTS}
	if len(got) != len(want) {
		t.Fatalf("mutation stage has %d atoms, want %d", len(got), len(want))
	}
	for _, a := range got {
		lane, ok := want[a.ID]
		if !ok || a.Lane != lane {
			t.Errorf("%s: lane %q, want a mutation atom per lane", a.ID, a.Lane)
		}
	}
	for _, a := range PullPathAtoms() {
		if a.Stage == StageMutation {
			t.Errorf("%s: a mutation atom in the default vector — the gate would run it", a.ID)
		}
	}
}

// NO TESTS IS A FINDING, NOT AN ABSENCE. Rob, 2026-09-11: "absent tests red
// the PR and refuse the publish. We don't build anything without tests."
// Each language's test atom used to read green on a tree with nothing to run —
// `go test` prints "[no test files]" and exits 0, `cargo test` runs 0 tests and
// exits 0, pytest's exit 5 was mapped to ABSENT, and a repo's gate script may
// never reach `bun test`. Each counts before it runs now, and the count is a
// pure function with its own table: GoHasTestFiles, CargoListsTests,
// PytestState and TSTestPatterns.
//
// This asserts the FOUR COUNTERS ALL AGREE THAT NOTHING IS NOT CLEAN, in one
// place, because the rule is one rule and four files is four chances to soften
// it back to a pass.
func TestNoTestsIsAFindingInEveryLane(t *testing.T) {
	if GoHasTestFiles("00\n00\n00\n") {
		t.Error("go: a module where every package counts zero test files reads as having tests")
	}
	if !GoHasTestFiles("00\n10\n") {
		t.Error("go: a module with one test file reads as having none")
	}
	if CargoListsTests("\nrunning 0 tests\n\n0 tests, 0 benchmarks\n") {
		t.Error("rust: a workspace that listed no test reads as having tests")
	}
	if !CargoListsTests("src/lib.rs - a (line 3): test\n") {
		t.Error("rust: libtest's own listing reads as no tests")
	}
	if state, reason := PytestState(5); state != 1 || !strings.Contains(reason, "nothing is built without tests") {
		t.Errorf("python: pytest's exit 5 (nothing collected) is %d/%q, want a finding naming the rule", state, reason)
	}
	if len(TSTestPatterns) == 0 {
		t.Error("ts: no test pattern at all, so the presence check has nothing to enumerate with")
	}
}

// Every atom of the catalogue has a place in catalogueOrder, once: the order is
// the push stage's fail-fast sequence, and an atom outside it would sort last.
func TestEveryCatalogueIDHasAPlace(t *testing.T) {
	seen := map[string]bool{}
	for _, id := range catalogueOrder {
		if seen[id] {
			t.Errorf("%s is placed twice", id)
		}
		seen[id] = true
	}
	for _, a := range Atoms() {
		if !seen[a.ID] {
			t.Errorf("%s has no place in catalogueOrder", a.ID)
		}
	}
}

func TestOrderedKeepsThePlacesAndSortsTheRestByID(t *testing.T) {
	rows := []AtomDef{{ID: "z:last"}, {ID: "go:vet"}, {ID: "a:first"}, {ID: "fleet:check-yaml"}}
	got := ordered(rows)
	want := []string{"fleet:check-yaml", "go:vet", "a:first", "z:last"}
	for i, r := range got {
		if r.ID != want[i] {
			t.Fatalf("slot %d is %s, want %s", i, r.ID, want[i])
		}
	}
}

// UseDefs joins the rows of the atoms in their own files to the table, in the
// catalogue's order; an id the table still holds, or one given twice, panics at
// load rather than answering two atoms for one id.
func TestUseDefsJoinsTheRowsAndRefusesADuplicate(t *testing.T) {
	old := atoms
	t.Cleanup(func() { atoms = old })

	UseDefs([]AtomDef{{ID: "x:new"}, {ID: "go:vet"}})
	if got := AtomByID("go:vet"); got.ID != "go:vet" {
		t.Fatalf("go:vet is not resolved: %+v", got)
	}
	last := Atoms()[len(Atoms())-1]
	if last.ID != "x:new" {
		t.Errorf("an id with no place sorts last, got %s", last.ID)
	}

	for _, rows := range [][]AtomDef{{{ID: "go:mutation"}}, {{ID: "x:a"}, {ID: "x:a"}}} {
		func() {
			defer func() {
				if r := recover(); r == nil || !strings.Contains(r.(string), "catalogued twice") {
					t.Errorf("rows %v: recovered %v", rows, r)
				}
			}()
			UseDefs(rows)
		}()
	}
}

// THE TABLE HOLDS THE UNPORTED SET AND NOTHING ELSE (lld FN-090 step 3): the
// eight atoms whose chains stay on Dagger (DEF-B3-035, DEF-B3-044). Every
// other catalogue id comes from a file under atoms/.
func TestAtomTableHoldsExactlyTheUnportedAtoms(t *testing.T) {
	want := []string{
		"go:mutation",
		"python:mutation",
		"rust:mutation",
		"ts:mutation",
	}
	var got []string
	for _, a := range atomTable() {
		got = append(got, a.ID)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("atomTable() ids = %v, want %v", got, want)
	}
}
