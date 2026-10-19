package catalogue

import (
	"sort"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// What the go atoms' rows say, as the module as a whole sees them (the rows of
// the atoms that live in files included). They were facts about the table
// while the table held the rows.

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
	if !checks.AtomByID("go:test-race").NeedsDies {
		t.Error("go:test-race no longer asks for foundry-dies. hephaestus's " +
			"internal/slag goldens are its reader: without the mount they " +
			"resolve nothing, SKIP, and report success — which is the whole " +
			"of #2453 and #8118. Restore NeedsDies, or move the goldens.")
	}
	// The mount is a fetch on every run of every atom that asks for it, so the
	// ask stays deliberate. Widening it is a decision, not a default.
	//
	// WHY FOUR. go:mutation runs the repo's `go test` too (gremlins gathers
	// coverage with it), and hephaestus's armed goldens refuse in any
	// container that runs `go test` under CI=true without /dies — measured
	// 2026-09-11 on hephaestus #53, the mutation lane could-not-run on every
	// hephaestus pull. The rule is: an atom that runs `go test` needs the
	// tree the armed goldens read. go:test joined them when F13 split the
	// suite by cadence — the commit's unit run is the same `go test` over the
	// same packages, so the same goldens refuse without the mount.
	// go:diff-coverage makes four: it runs that same go:test-race suite itself.
	//
	// AND FOUR MORE, FOR ANOTHER REASON: ops:orbit-composed, orbit:repo,
	// orbit:sidecars and orbit:surface read foundry-dies' contracts and roster
	// (Input.Dies), the one input of theirs that is not the tree; their chains
	// mounted it from the run, and their Defs now ask for it by need.
	//
	// AND FOUR MORE: go:release, rust:release, ts:release and python:release name
	// the star by its record in foundry-dies when the tree's answers file does not
	// (hephaestus has none; the frontend template's names repo_name, not the star).
	// Their chains read the records lazily from the run's own dies; a pod has no
	// run to read from, so the atom asks for the checkout.
	// AND TWO MORE (b6, DEF-B6-012): go:cast-release and rust:cast-release, the
	// cast lane's compile — only lane cast plans them, so no gate clones dies for
	// them; the pod lays dies only for a plan that needs it and the cast's
	// layer-cast reads the star's record there.
	// AND ONE MORE: dev:go-test is the Dev surface's `go test -race ./...` as a
	// manifest; Dagger's dev test mounts dies for the same goldens (dev.go planGo).
	var asked []string
	for _, a := range checks.Atoms() {
		if a.NeedsDies {
			asked = append(asked, a.ID)
		}
	}
	sort.Strings(asked)
	if strings.Join(asked, ",") != "dev:go-test,dev:go-test-live,go:cast-release,go:diff-coverage,go:mutation,go:release,go:test,go:test-race,ops:orbit-composed,orbit:repo,orbit:sidecars,orbit:surface,python:release,rust:cast-release,rust:release,ts:release" {
		t.Errorf("NeedsDies is declared by %v; go:test, go:test-race, go:diff-coverage and go:mutation (the four atoms that run a repo's `go test`) "+
			"and the four orbit atoms that read foundry-dies' contracts and the four release atoms that name a star by its record are expected. Adding one is fine — say why "+
			"here, because each one is another clone of foundry-dies on every gate "+
			"run in the fleet.", asked)
	}
}

// A caller that asks for both pull-path stages at once — the door's gate lane,
// until F16 runs the stages separately — must not run the Go suite twice.
func TestTheUnitSuiteStandsDownWhenTheRaceSuiteRunsBeside(t *testing.T) {
	run, covered := checks.Subsumed(checks.AtomsForStage(""))
	ids := func(as []checks.AtomDef) string {
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
	v := checks.CoveredVerdict(covered[0])
	if v.Result != "absent" || v.State != 0 || !strings.Contains(v.Reason, "go:test-race") {
		t.Errorf("a covered atom is an omission that names its subsumer: %+v", v)
	}
	if _, ok := checks.AnnouncedAbsence(v.Atom, v.Reason); !ok {
		t.Errorf("a covered atom's reason reads as an absence: %q", v.Reason)
	}
	// The commit stage alone still runs it: a subsumer that is not selected
	// subsumes nothing.
	commit, none := checks.Subsumed(checks.AtomsForStage(checks.StagePrecommit))
	if len(none) != 0 || !strings.Contains(","+ids(commit)+",", ",go:test,") {
		t.Errorf("the commit stage runs its own unit suite: covered %v", ids(none))
	}
}

// A FORK KEEPS ITS TESTS AND ITS AUDIT; the change-scoped bar is the fleet's own
// and a fork's changes are mostly its upstream's code.
func TestTheForkLaneOfTheGoSuiteAtoms(t *testing.T) {
	for _, id := range []string{"go:test", "go:test-race", "go:govulncheck"} {
		if got := checks.AtomByID(id).NotOnForks; got != "" {
			t.Errorf("%s stands down on a fork (%q); a fork keeps it", id, got)
		}
	}
	if got := checks.AtomByID("go:diff-coverage").NotOnForks; got != checks.ForkChangeBar {
		t.Errorf("go:diff-coverage: NotOnForks %q", got)
	}
}

// The rows the table carried, held as they were: the stage and subsumption the
// planner reads.
func TestTheGoSuiteAtomsKeepTheirRows(t *testing.T) {
	for _, tc := range []struct {
		id    string
		stage string
		subs  string
		dies  bool
	}{
		{"go:test", checks.StagePrecommit, "go:test-race", true},
		{"go:test-race", checks.StagePrepush, "", true},
		{"go:govulncheck", checks.StagePrepush, "", false},
		{"go:diff-coverage", checks.StagePrepush, "", true},
	} {
		a := checks.AtomByID(tc.id)
		if a.Stage != tc.stage || a.SubsumedBy != tc.subs || a.NeedsDies != tc.dies || a.Lane != checks.LaneGo || a.Image != checks.ImageGo || a.NeedsManifest {
			t.Errorf("%s: %+v", tc.id, a)
		}
	}
}
