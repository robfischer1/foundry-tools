package rustmutation

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/unitkey"
)

const (
	// rustMutationJobs is cargo-mutants' -j. The job count stays two: the engine
	// is shared by every lane in flight, and two copies at a rustc each is what
	// fits beside them.
	rustMutationJobs = 2
	// THE LANE'S CPU SHARE, about 8 cores: rustMutationJobs mutants at
	// rustMutantCores each. Measured 2026-10-12 01:46Z on dev01 (24 cores): load
	// average 96, CPU PSI some ~0.70, seven 24-thread mold processes at once. Each
	// mutant's cargo ran at -j nproc, mold linked at a thread per core and nextest
	// ran a test per core, so one lane saturated the node while daedalus priced it
	// as a few units (flux #3051). Three knobs, all set on the one exec so the
	// baseline obeys them too:
	//   CARGO_BUILD_JOBS      cargo's build.jobs, rustc invocations per mutant
	//   NEXTEST_TEST_THREADS  nextest's --test-threads, tests per mutant
	//   --thread-count        mold 1.10.1 has no environment knob for it, so it
	//                         rides rustc's link args (CARGO_BUILD_RUSTFLAGS, the
	//                         lowest of cargo's rustflags sources: it never
	//                         overrides a star's RUSTFLAGS or target rustflags)
	rustMutantCores = 4
	// rustMoldThreadsFlag is the link arg that carries rustMutantCores to mold.
	rustMoldThreadsFlag = "-C link-arg=-Wl,--thread-count=4"
	// rustTestRunnerEnv keys cargo's runner by the host triple; every engine node
	// is x86_64 (measured 2026-10-02).
	rustTestRunnerEnv = "CARGO_TARGET_X86_64_UNKNOWN_LINUX_GNU_RUNNER"
	// rustTestRunner caps each test process at 4GiB of data. A mutant's test that
	// needs more than that is a runaway; the 16G per-exec memory.max stays as the
	// backstop behind it.
	rustTestRunner = "prlimit --data=4294967296 --"
)

// The disk and the link, as the atom touches them: variables so a test can say
// they fail.
var (
	writeFile  = os.WriteFile
	linkHelper = atoms.LinkHelper
)

// moldAvailable says the linker the lane drives every mutant's link through is
// installed. The Dagger lane image carries it; the executor's toolchain image is
// upstream's rust, which does not, and the verdict does not depend on it: only
// the link time does.
var moldAvailable = func() bool { _, err := exec.LookPath("mold"); return err == nil }

// measure runs cargo-mutants and settles what it left.
//
// THE PER-MUTANT COST, measured on bellows #31a47b3 (2026-09-18, 169 mutants):
// 14s build + 30s test each, two at a time — an hour's lane. Three things below
// are that cost, not the gate:
//
//	mold -run          every mutant relinks the test binaries; mold takes the link
//	                   through LD_PRELOAD for the whole process tree, so no
//	                   RUSTFLAGS are overridden and a star's own .cargo/config.toml
//	                   still applies.
//	debug = 0          the dev profile's line tables are most of what the linker
//	                   moves; a mutant's outcome does not need them. Set through
//	                   the environment, not the star's Cargo.toml.
//	--test-tool nextest one process per test, and a duration on every PASS/FAIL
//	                   line in the baseline log, which is what RustMutationVerdict
//	                   reads to name the ten tests the test half is made of.
//
// EVERY TEST PROCESS RUNS UNDER RLIMIT_DATA (2026-10-02). A mutant can turn a
// bounded loop into an unbounded allocation: paneless d988b9b grew one test
// binary to 16.4G and the engine's per-exec memory.max killed it. cargo-mutants
// has no memory flag, so the cap rides cargo's target runner, which nextest
// honours: the test binaries run under prlimit, and rustc, the linker and build
// scripts do not. DATA, NOT AS: RLIMIT_AS counts reservations, and 64 idle
// threads hold 4.3G of address space on 2.6M of RSS.
//
// EVERY GRADED MUTANT PRINTS A LINE (--caught --unviable). An exec the engine
// kills takes mutants.out with it, and the output its error carries is all that
// is left (checks.RustMutationKilled).
//
// UNDER execmem (foundry-tools #13052). A kill takes the exec and everything that
// could be read from it afterwards, so the cgroup's memory is printed WHILE the
// run goes and the last reading before a kill rides the output into
// checks.RustMutationKilled. The atom's own binary answers as execmem, through a
// link.
func (g *grading) measure(ctx context.Context, diff string, packages []string, keys []checks.UnitKey, owner func(string) (string, bool)) checks.Verdict {
	diffFile := filepath.Join(g.work, "pr.diff")
	out := filepath.Join(g.work, "out")
	tmp := filepath.Join(g.work, "tmp")
	for _, dir := range []string{out, tmp} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return g.settle(2, "CANNOT RUN - the atom never ran: "+err.Error())
		}
	}
	if err := writeFile(diffFile, []byte(diff+"\n"), 0o644); err != nil {
		return g.settle(2, "CANNOT RUN - the atom never ran: "+err.Error())
	}
	exe, err := linkHelper(filepath.Join(g.work, "bin"), "execmem")
	if err != nil {
		return g.settle(2, "CANNOT RUN - the atom never ran: "+err.Error())
	}
	args := []string{"--"}
	env := []string{
		"TMPDIR=" + tmp, "CARGO_PROFILE_DEV_DEBUG=0", "CARGO_PROFILE_TEST_DEBUG=0",
		rustTestRunnerEnv + "=" + rustTestRunner, "CARGO_BUILD_JOBS=" + strconv.Itoa(rustMutantCores),
		"NEXTEST_TEST_THREADS=" + strconv.Itoa(rustMutantCores),
	}
	if moldAvailable() {
		args = append(args, "mold", "-run")
		env = append(env, "CARGO_BUILD_RUSTFLAGS="+rustMoldThreadsFlag)
	}
	args = append(args, "cargo", "mutants", "--colors", "never", "--caught", "--unviable", "-j", strconv.Itoa(rustMutationJobs),
		"--build-timeout", rustBuildTimeout, "--minimum-test-timeout", rustMinTestTimeout, "--test-tool", "nextest")
	for _, m := range strings.Fields(g.mods) {
		args = append(args, "-f", m)
	}
	for _, p := range packages {
		args = append(args, "-p", p)
	}
	// THE FLEET'S SKIPPED CLASSES, as cargo-mutants names its mutants
	// (checks.RatifiedMutators): never generated, so never graded or printed.
	for _, re := range checks.SkippedMutators(checks.MutatorRust) {
		args = append(args, "--exclude-re", re)
	}
	args = append(args, "--output", out, "-D", diffFile)

	cmd := atoms.Cmd{Dir: g.tree, Name: exe, Args: args, Env: env, Both: true, Unset: unset}
	log, status := atoms.RunCmd(ctx, g.in, cmd)
	// A KILLED RUN RAN. The engine answers an exec that ended in the signal range
	// (137: SIGKILL) with an error, not a code, and the output it kept carries the
	// outcomes graded before the kill.
	if !atoms.ToolRan(status) {
		if status > 128 {
			state, reason, found := checks.RustMutationKilled(status, log)
			v := g.settle(state, reason)
			v.Findings = found
			return v
		}
		return g.neverRan(cmd, status, log)
	}
	// cargo mutants writes no list for an outcome it never reached; a list that
	// does not read is an empty one, and the exit decides the verdict.
	list := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(out, "mutants.out", name+".txt"))
		return string(b)
	}
	// The baseline's own log: the unmutated build and test run, where nextest
	// stamped every test with its wall time.
	baseline, _ := os.ReadFile(filepath.Join(out, "mutants.out", "log", "baseline.log"))
	missed, caught, unviable, timeout := list("missed"), list("caught"), list("unviable"), list("timeout")
	state, reason, found := checks.RustMutationVerdict(checks.RustMutationRun{
		Status: status, Log: log,
		Missed: missed, Caught: caught, Unviable: unviable, Timeout: timeout,
		Baseline: string(baseline),
	})
	v := g.settle(state, reason)
	v.Gradings = checks.BuildGradings(string(unitkey.Rust), rustMutationEngine(), keys, owner,
		checks.RustScored(missed, caught, unviable, timeout), state < 2)
	// THE FINDINGS COME FROM THE OUTCOME LISTS, as go:mutation's come from its
	// score: FindingsOf parses no mutation report, and a timed-out mutant's finding
	// rides a PASSING atom too — an exclusion nobody can read is a suppression
	// (findings.schema.json).
	v.Findings = found
	return v
}
