package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// stubEveryChain answers every registered atom PASS without running it, and
// counts the asks — so a test of the planner sees which atoms reached a runner.
func stubEveryChain(t *testing.T) *chainCalls {
	t.Helper()
	calls := &chainCalls{n: map[string]int{}}
	for id, orig := range registry {
		t.Cleanup(func() { registry[id] = orig })
		registry[id] = func(_ context.Context, _ *run) checks.Verdict {
			calls.mu.Lock()
			calls.n[id]++
			calls.mu.Unlock()
			return checks.VerdictOf(checks.AtomByID(id), 0, chainMark+id)
		}
	}
	return calls
}

// forkTree is a rust workspace with a Go module beside it — gravity's shape.
var forkTree = map[string]string{
	"Cargo.toml":              "[workspace]\n",
	"cmd/gravity/src/main.rs": "fn main() {}\n",
	"validation/go.mod":       "module x\n",
	"validation/x.go":         "package x\n",
}

const forkDoor = "http://ourea.default.svc.cluster.local:8215/forks/gravity.git"

// A REPOSITORY WHOSE RECORD IS KIND VENDORED IS GRADED AS A FORK, through the
// whole vector: the atoms that hold code to the fleet's own house rules never
// reach a runner and answer ABSENT naming the record; build, tests and audits
// run. The same tree under a record of any other kind runs them all.
func TestAVendoredRecordGradesTheTreeAsAFork(t *testing.T) {
	casting(t, voterChains)
	for _, tc := range []struct {
		name, kind string
		fork       bool
	}{
		{"a fork", "vendored", true},
		{"a fleet repo", "rust", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, stage := range []string{checks.StagePrecommit, checks.StagePrepush, checks.StageMutation} {
				calls := stubEveryChain(t)
				engine.reset()
				engine.withTree(forkTree)
				engine.withTree(map[string]string{
					"/dies/fleet/repos/gravity.json": `{"name":"gravity","kind":"` + tc.kind + `","repo":"forks/gravity"}`,
				})
				m := &FoundryTools{Source: dag.Directory(), Repo: forkDoor, Sha: buildSha}
				vs, err := m.vector(soon(t), stage, "", "")
				if err != nil {
					t.Fatal(err)
				}
				for _, v := range vs {
					a := checks.AtomByID(v.Atom)
					inert := strings.Contains(v.Reason, "is kind vendored, a fork of an upstream")
					switch {
					case tc.fork && a.NotOnForks != "" && v.Result == "absent" && !inert && !strings.Contains(v.Reason, "covers it"):
						// Absent for its lane first (e.g. no python here): fine.
						if calls.of(v.Atom) != 0 {
							t.Errorf("%s/%s ran: %+v", stage, v.Atom, v)
						}
					case tc.fork && a.NotOnForks != "":
						if !inert || calls.of(v.Atom) != 0 || v.State != 0 {
							t.Errorf("%s/%s: want a fork ABSENT that never ran, got %+v (asked %d)", stage, v.Atom, v, calls.of(v.Atom))
						}
					case inert:
						t.Errorf("%s/%s stood down as a fork: %+v", stage, v.Atom, v)
					}
				}
				// The rust lane's suite and audit run either way; clippy only off a fork.
				for id, want := range map[string]int{"rust:cargo-test": 1, "rust:cargo-fmt": 1, "rust:cargo-audit": 1, "rust:cargo-clippy": map[bool]int{true: 0, false: 1}[tc.fork]} {
					if checks.AtomByID(id).Stage == stage && calls.of(id) != want {
						t.Errorf("%s/%s asked %d times, want %d", stage, id, calls.of(id), want)
					}
				}
			}
		})
	}
}

// EVERY DOUBT IS "NOT A FORK": no clone URL, no record for the repo, or
// records that cannot be read all leave the tree on the fleet's whole bar.
func TestForkRecordAnswersNotAForkOnEveryDoubt(t *testing.T) {
	vendored := map[string]string{"/dies/fleet/repos/gravity.json": `{"name":"gravity","kind":"vendored","repo":"forks/gravity"}`}
	ask := func(repo string) string {
		return newRun(dag.Directory(), repo, "").forkRecord(context.Background())
	}

	engine.reset()
	engine.withTree(vendored)
	if got := ask(forkDoor); got != "gravity" {
		t.Errorf("the vendored record: %q", got)
	}
	// ONE READ in the common case: the record named for the repo, no scan.
	if c := engine.chain(`"fleet/repos/*.json"`); c != "" {
		t.Errorf("the record named for the repo was found by a scan:\n%s", c)
	}
	if got := ask(""); got != "" {
		t.Errorf("no clone URL: %q", got)
	}
	if got := ask("http://door:8215/rob/hades.git"); got != "" {
		t.Errorf("no record names rob/hades: %q", got)
	}

	// A record named otherwise is found by the scan.
	engine.reset()
	engine.withTree(map[string]string{"/dies/fleet/repos/arcjet-gravity.json": `{"name":"arcjet-gravity","kind":"vendored","repo":"forks/gravity"}`})
	if got := ask(forkDoor); got != "arcjet-gravity" {
		t.Errorf("by scan: %q", got)
	}
	if engine.chain(`"fleet/repos/*.json"`) == "" {
		t.Errorf("a record named otherwise was found without the scan:\n%v", engine.chains())
	}

	// A record of the same name that names another repo is not this one's.
	engine.reset()
	engine.withTree(map[string]string{"/dies/fleet/repos/gravity.json": `{"name":"gravity","kind":"vendored","repo":"rob/gravity"}`})
	if got := ask(forkDoor); got != "" {
		t.Errorf("another repo's record: %q", got)
	}

	// The records cannot be listed: not a fork.
	engine.reset()
	engine.withTree(map[string]string{"/dies/fleet/repos/other.json": `{"name":"other","kind":"vendored","repo":"forks/other"}`})
	engine.fail(`glob(pattern:"fleet/repos/*.json")`, "the dies went away")
	if got := ask("http://door:8215/forks/elsewhere.git"); got != "" {
		t.Errorf("unlisted records: %q", got)
	}

	// The hook names the clone by --origin.
	engine.reset()
	engine.withTree(vendored)
	if got := newRun(dag.Directory(), "", "").fromOrigin("https://git.notusmi.com/forks/gravity.git").forkRecord(context.Background()); got != "gravity" {
		t.Errorf("by origin: %q", got)
	}
}

// A ROOT THAT CANNOT BE READ STILL KNOWS IT IS A FORK. The planner files every
// lane atom could-not-run and runs the run-anywhere atoms; on a fork the ones
// a fork does not take stand down instead of running.
func TestAnUnreadableForkRootStillStandsDownWhatAForkDoesNotTake(t *testing.T) {
	casting(t, voterChains)
	calls := stubEveryChain(t)
	engine.reset()
	engine.withTree(forkTree)
	engine.withTree(map[string]string{"/dies/fleet/repos/gravity.json": `{"name":"gravity","kind":"vendored","repo":"forks/gravity"}`})
	engine.fail("entries", "the tree went away")
	m := &FoundryTools{Source: dag.Directory(), Repo: forkDoor, Sha: buildSha}
	vs, err := m.vector(soon(t), checks.StagePrepush, "", "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]checks.Verdict{}
	for _, v := range vs {
		seen[v.Atom] = v
	}
	for _, id := range []string{"fleet:witness", "go:staticcheck"} {
		if v := seen[id]; v.State != 0 || !strings.Contains(v.Reason, "is kind vendored") || calls.of(id) != 0 {
			t.Errorf("%s: %+v (asked %d)", id, v, calls.of(id))
		}
	}
	if v := seen["go:build"]; v.State != 2 || !strings.Contains(v.Reason, "could not read the repository root") {
		t.Errorf("go:build: %+v", v)
	}
	if calls.of("fleet:node-kinds-declared") != 1 {
		t.Errorf("a run-anywhere atom a fork keeps did not run")
	}
}
