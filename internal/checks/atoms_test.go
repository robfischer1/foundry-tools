package checks

import (
	"sort"
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

// THE GO TEST ATOM MOUNTS THE FLEET'S RECORD TREE, and the reason is not
// visible from the go lane — which is exactly why this test exists.
//
// hephaestus's internal/slag goldens grade every record committed in
// foundry-dies. A gate lane checks out one repository, so before the mount
// they resolved nothing and SKIPPED, and a skipped test is `ok` to `go test`
// and a success to the gate: three whole-fleet gates reported green having
// examined nothing (#2453, and #8118 for the same defect one level up).
//
// A future reader sees an unexplained field on a plain `go test -race` row and
// drops it as dead weight. Dropping it does not break a build, does not red a
// gate, and silently returns those goldens to reporting success without
// running — so the guard has to be a test rather than a comment.
func TestTheGoTestAtomsCarryTheFleetRecordTree(t *testing.T) {
	if !AtomByID("go:test-race").NeedsDies {
		t.Error("go:test-race no longer asks for foundry-dies. hephaestus's " +
			"internal/slag goldens are its reader: without the mount they " +
			"resolve nothing, SKIP, and report success — which is the whole " +
			"of #2453 and #8118. Restore NeedsDies, or move the goldens.")
	}
	// The mount is a fetch on every run of every atom that asks for it, so the
	// ask stays deliberate. Widening it is a decision, not a default.
	//
	// WHY THREE. go:mutation runs the repo's `go test` too (gremlins gathers
	// coverage with it), and hephaestus's armed goldens refuse in any
	// container that runs `go test` under CI=true without /dies — measured
	// 2026-09-11 on hephaestus #53, the mutation lane could-not-run on every
	// hephaestus pull. The rule is: an atom that runs `go test` needs the
	// tree the armed goldens read. go:test joined them when F13 split the
	// suite by cadence — the commit's unit run is the same `go test` over the
	// same packages, so the same goldens refuse without the mount.
	var asked []string
	for _, a := range Atoms {
		if a.NeedsDies {
			asked = append(asked, a.ID)
		}
	}
	sort.Strings(asked)
	if strings.Join(asked, ",") != "go:mutation,go:test,go:test-race" {
		t.Errorf("NeedsDies is declared by %v; go:test, go:test-race and go:mutation are expected — "+
			"the three atoms that run a repo's `go test`. Adding one is fine — say why "+
			"here, because each one is another clone of foundry-dies on every gate "+
			"run in the fleet.", asked)
	}
}

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

// A caller that asks for both pull-path stages at once — the door's gate lane,
// until F16 runs the stages separately — must not run the Go suite twice.
func TestTheUnitSuiteStandsDownWhenTheRaceSuiteRunsBeside(t *testing.T) {
	run, covered := Subsumed(AtomsForStage(""))
	ids := func(as []AtomDef) string {
		var out []string
		for _, a := range as {
			out = append(out, a.ID)
		}
		return strings.Join(out, ",")
	}
	if ids(covered) != "go:test" {
		t.Errorf("covered %q: go:test-race covers go:test in the union", ids(covered))
	}
	if strings.Contains(","+ids(run)+",", ",go:test,") || !strings.Contains(ids(run), "go:test-race") {
		t.Errorf("the union runs the race suite and not the unit one: %s", ids(run))
	}
	v := CoveredVerdict(covered[0])
	if v.Result != "absent" || v.State != 0 || !strings.Contains(v.Reason, "go:test-race") {
		t.Errorf("a covered atom is an omission that names its subsumer: %+v", v)
	}
	if _, ok := AnnouncedAbsence(v.Atom, v.Reason); !ok {
		t.Errorf("a covered atom's reason reads as an absence: %q", v.Reason)
	}
	// The commit stage alone still runs it: a subsumer that is not selected
	// subsumes nothing.
	commit, none := Subsumed(AtomsForStage(StagePrecommit))
	if len(none) != 0 || !strings.Contains(","+ids(commit)+",", ",go:test,") {
		t.Errorf("the commit stage runs its own unit suite: covered %v", ids(none))
	}
}
