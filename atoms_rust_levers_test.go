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
// own caps (TestRustMutationMeasuresTheDiffFromTheFetchedLayer); these are the
// other four's.

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
		[]string{"withEnvVariable", leverJobs, `value:"` + strconv.Itoa(rustGateJobs) + `"`},
		[]string{"withEnvVariable", leverInc, `value:"0"`},
	)
	fetch, jobs, inc := strings.Index(chain, `"cargo","fetch","--locked"`), strings.Index(chain, leverJobs), strings.Index(chain, leverInc)
	build := strings.LastIndex(chain, compile)
	if !(fetch < jobs && fetch < inc && jobs < build && inc < build) {
		t.Errorf("%s: the bound must follow the fetch and precede the compile (fetch %d, jobs %d, incremental %d, compile %d):\n%s", atom, fetch, jobs, inc, build, chain)
	}
}

func TestRustGateJobsIsBelowNprocAndNamed(t *testing.T) {
	if rustGateJobs != 6 {
		t.Errorf("rustGateJobs = %d, want 6", rustGateJobs)
	}
	if rustGateJobs <= 0 || rustGateJobs > 8 {
		t.Errorf("rustGateJobs = %d: a gate atom must stay a fraction of dev01's 24 cores", rustGateJobs)
	}
	if want := "-C link-arg=-Wl,--thread-count=" + strconv.Itoa(rustGateLinkThreads); rustGateMoldThreadsFlag != want {
		t.Errorf("mold flag %q out of step with rustGateLinkThreads, want %q", rustGateMoldThreadsFlag, want)
	}
}

func TestRustCargoClippyIsBoundedAndLeanOnDebuginfo(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "rust:cargo-clippy", ""), 0)
	c := engine.chain(`"cargo","clippy"`, "exitCode")
	wantBounded(t, "rust:cargo-clippy", c, `"cargo","clippy"`)
	wantCalls(t, c,
		[]string{"withEnvVariable", leverDevDb, `value:"line-tables-only"`},
		[]string{"withEnvVariable", leverTstDb, `value:"line-tables-only"`},
	)
	// Clippy links nothing it needs mold for: no linker flag, no wrapper.
	if strings.Contains(c, leverFlags) || strings.Contains(c, `"mold","-run"`) {
		t.Errorf("clippy must not carry the link levers:\n%s", c)
	}
}

func TestRustCargoTestIsBoundedLeanAndLinkedWithMold(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(`"--list"`, "checks::lane::works: test\n\n1 test\n")
	wantState(t, runAtom(t, "rust:cargo-test", ""), 0)
	for _, probe := range []string{`"--","--list"]`, `args:["mold","-run","cargo","test","--workspace"]`} {
		c := engine.chain(probe, "exitCode")
		wantBounded(t, "rust:cargo-test", c, probe)
		wantCalls(t, c,
			[]string{"withEnvVariable", leverDevDb, `value:"line-tables-only"`},
			[]string{"withEnvVariable", leverTstDb, `value:"line-tables-only"`},
			// the mutation atom's cap on mold's link threads, same mechanism
			[]string{"withEnvVariable", leverFlags, `value:"-C link-arg=-Wl,--thread-count=4"`},
			[]string{"withExec", `expect:ANY`, `args:["mold","-run","cargo","test","--workspace"`},
		)
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
