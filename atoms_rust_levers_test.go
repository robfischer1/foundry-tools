package main

import (
	"strconv"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE GATE'S MEMORY LEVERS, ATOM BY ATOM. Cargo's defaults (-j nproc, debug=2,
// incremental on, GNU ld) under atomsInFlight compiling atoms at once drove
// the 14G anon spike and the 2026-10-15 dev01 OOM. The mutation atom has its
// own caps (TestRustMutationMeasuresTheDiffFromTheFetchedLayer); clippy and test
// set theirs on their own execs (atoms/rust/cargo-clippy, cargo-test); these are
// the chains' that remain.

const (
	leverJobs  = `name:"CARGO_BUILD_JOBS"`
	leverInc   = `name:"CARGO_INCREMENTAL"`
	leverDevDb = `name:"CARGO_PROFILE_DEV_DEBUG"`
	leverTstDb = `name:"CARGO_PROFILE_TEST_DEBUG"`
	leverFlags = `name:"CARGO_BUILD_RUSTFLAGS"`
)

// wantBounded is the jobs and incremental pair, set after the fetch (so the
// fetch layer is shared with every atom) and before the compile.
func wantBounded(t *testing.T, atom, chain, compile string) {
	t.Helper()
	wantCalls(t, chain,
		[]string{"withExec", `args:["cargo","fetch","--locked"]`},
		[]string{"withEnvVariable", leverJobs, `value:"` + strconv.Itoa(checks.RustGateJobs) + `"`},
		[]string{"withEnvVariable", leverInc, `value:"0"`},
	)
	fetch, jobs, inc := strings.Index(chain, `"cargo","fetch","--locked"`), strings.Index(chain, leverJobs), strings.Index(chain, leverInc)
	build := strings.LastIndex(chain, compile)
	if !(fetch < jobs && fetch < inc && jobs < build && inc < build) {
		t.Errorf("%s: the bound must follow the fetch and precede the compile (fetch %d, jobs %d, incremental %d, compile %d):\n%s", atom, fetch, jobs, inc, build, chain)
	}
}

func TestRustGateJobsIsBelowNprocAndNamed(t *testing.T) {
	if checks.RustGateJobs != 6 {
		t.Errorf("checks.RustGateJobs = %d, want 6", checks.RustGateJobs)
	}
	if checks.RustGateJobs <= 0 || checks.RustGateJobs > 8 {
		t.Errorf("checks.RustGateJobs = %d: a gate atom must stay a fraction of dev01's 24 cores", checks.RustGateJobs)
	}
	if want := "-C link-arg=-Wl,--thread-count=" + strconv.Itoa(checks.RustGateLinkThreads); checks.RustGateMoldThreadsFlag != want {
		t.Errorf("mold flag %q out of step with checks.RustGateLinkThreads, want %q", checks.RustGateMoldThreadsFlag, want)
	}
}

func TestRustReleaseIsBoundedButKeepsItsProfile(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": copiesTron, ".copier-answers.yml": "service_name: tron\n"})
	wantState(t, runAtom(t, "rust:release", ""), 0)
	c := engine.chain(`"cargo","build","--release"`, "exitCode")
	wantBounded(t, "rust:release", c, `"cargo","build","--release"`)
	// The release binary is the shipped artifact: its debuginfo and linker are
	// the star's own.
	for _, n := range []string{leverDevDb, leverTstDb, leverFlags, `"mold","-run"`} {
		if strings.Contains(c, n) {
			t.Errorf("the release build must leave %s alone:\n%s", n, c)
		}
	}
}

func TestRustWitGuestIsBounded(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": witTwoWorlds})
	engine.stdout(`"component","wit"`, "world w")
	wantState(t, runAtom(t, "rust:wit-guest", ""), 0)
	for _, probe := range []string{`"--target","` + checks.WitGuestTarget + `"`, `"cargo","test"`} {
		c := engine.chain(probe, "exitCode")
		if c == "" {
			t.Fatalf("no chain for %s:\n%v", probe, engine.chains())
		}
		wantBounded(t, "rust:wit-guest", c, probe)
		if strings.Contains(c, leverDevDb) || strings.Contains(c, leverTstDb) {
			t.Errorf("only clippy and test trim debuginfo:\n%s", c)
		}
	}
}
