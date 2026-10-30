package rustmut

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/unitkey"
)

func TestTheRustEngineIsPinned(t *testing.T) {
	const want = "6deea0d40393beb058633386b8290980828c47cca285fb4b367aabc6223f8c7b"
	if got := rustMutationEngine(); got != want {
		t.Fatalf("rustMutationEngine() = %s, want %s — an input moved; every stored Rust grading now misses. If that is meant, pin the new value", got, want)
	}
}

// The caps are one share: jobs x cores per mutant is the lane's whole budget
// where the budget has room, and the flags are the same number in the linker's
// own spelling.
func TestRustMutationCPUShareIsAboutEightCores(t *testing.T) {
	if rustMutantCores*rustMutationJobs != 8 {
		t.Errorf("lane share = %d x %d cores, want 8", rustMutationJobs, rustMutantCores)
	}
}

// buildEnv reads the environment cargo mutants ran with, under a CPU budget and
// a toolchain.
func buildEnv(t *testing.T, budget, toolchain string, mold bool) string {
	t.Helper()
	p := newPull(t, "critical_modules: src/lib.rs\n")
	t.Setenv(checks.CPUsEnv, budget)
	if toolchain != "" {
		release, host, _ := strings.Cut(toolchain, "-")
		p.first(rule{"rustc -vV", "host: " + host + "\nrelease: " + release + "\n", 0})
	}
	moldAvailable = func() bool { return mold }
	if v := p.run(); v.State != 0 {
		t.Fatalf("%s", said(v))
	}
	c, _ := p.call("cargo mutants --colors")
	return strings.Join(c.Env, "\n")
}

// THE LANE FOLLOWS ITS CPU BUDGET, NOT THE NODE'S CORE COUNT (2026-10-19:
// mutation-bg-stellar-core-rust held 11.9 of a 12-core limit on a 24-core node).
// Where the budget covers jobs x cores the caps are what they always were; where
// it does not each mutant gets its share, never less than one.
func TestRustMutationCapsFollowTheCPUBudget(t *testing.T) {
	for name, tc := range map[string]struct {
		budget string
		want   []string
	}{
		"the 12-core cap is the old four": {"12", []string{"CARGO_BUILD_JOBS=4", "NEXTEST_TEST_THREADS=4", "--thread-count=4"}},
		"eight cores are the old four":    {"8", []string{"CARGO_BUILD_JOBS=4", "NEXTEST_TEST_THREADS=4", "--thread-count=4"}},
		"four cores are two a mutant":     {"4", []string{"CARGO_BUILD_JOBS=2", "NEXTEST_TEST_THREADS=2", "--thread-count=2"}},
		"one core is one a mutant":        {"1", []string{"CARGO_BUILD_JOBS=1", "NEXTEST_TEST_THREADS=1", "--thread-count=1"}},
	} {
		env := buildEnv(t, tc.budget, "", true)
		for _, need := range tc.want {
			if !strings.Contains(env, need) {
				t.Errorf("%s: the environment lacks %s:\n%s", name, need, env)
			}
		}
	}
}

// Without mold the executor's rust-lld sizes its own thread pool from the node
// (17 threads a link on a 24-core node), so the budget rides the link args, but
// only for a toolchain whose default linker is lld: another linker would fail
// on the flag.
func TestRustMutationCapsLLDWhereItIsTheDefaultLinker(t *testing.T) {
	env := buildEnv(t, "4", "1.97.0-x86_64-unknown-linux-gnu", false)
	if !strings.Contains(env, "CARGO_BUILD_RUSTFLAGS=-C link-arg=-Wl,--threads=2") {
		t.Errorf("rust-lld is the linker and the link is uncapped:\n%s", env)
	}
	if strings.Contains(env, "thread-count") {
		t.Errorf("mold's flag with no mold:\n%s", env)
	}
	for _, toolchain := range []string{"", "1.89.0-x86_64-unknown-linux-gnu", "1.97.0-aarch64-unknown-linux-gnu"} {
		if env := buildEnv(t, "4", toolchain, false); strings.Contains(env, "CARGO_BUILD_RUSTFLAGS") {
			t.Errorf("toolchain %q: a link flag for a linker not known to take it:\n%s", toolchain, env)
		}
	}
	if env := buildEnv(t, "4", "1.97.0-x86_64-unknown-linux-gnu", true); !strings.Contains(env, "--thread-count=2") || strings.Contains(env, "--threads=") {
		t.Errorf("mold is the linker and the flag is not its:\n%s", env)
	}
}

// rust:mutation runs git, cargo metadata and cargo mutants as plain execs, with
// the shared target directory taken away, and no script: nothing mounts
// foundry-stocks and nothing runs bash.
func TestRustMutationMeasuresTheDiff(t *testing.T) {
	p := newPull(t, "critical_modules: src/lib.rs\n")
	t.Setenv("CARGO_TARGET_DIR", "/cache/cargo-target")
	v := p.run()
	if v.State != 0 {
		t.Fatalf("%s", said(v))
	}
	c, ok := p.call("cargo mutants --colors")
	if !ok {
		t.Fatalf("cargo mutants never ran: %v", p.runs)
	}
	link := filepath.Join(p.work, "rust", "bin", "execmem")
	if c.Name != link {
		t.Errorf("cargo mutants runs under the execmem link %s, not %s", link, c.Name)
	}
	if target, err := os.Readlink(link); err != nil || target == "" {
		t.Errorf("execmem is not linked: %v", err)
	}
	want := append(append([]string{"--", "mold", "-run", "cargo", "mutants", "--colors", "never", "--caught", "--unviable", "-j", "2",
		"--build-timeout", "900", "--minimum-test-timeout", "60", "--test-tool", "nextest", "-f", "src/lib.rs"}, rustExcludes()...),
		"--output", filepath.Join(p.work, "rust", "out"), "-D", filepath.Join(p.work, "rust", "pr.diff"))
	if !reflect.DeepEqual(c.Args, want) {
		t.Errorf("cargo mutants argv:\n%q\nwant\n%q", c.Args, want)
	}
	if c.Dir != p.in.Root || !c.Both {
		t.Errorf("cargo mutants runs in the tree and reads both streams: %+v", c)
	}
	env := strings.Join(c.Env, "\n")
	for _, need := range []string{
		"TMPDIR=" + filepath.Join(p.work, "rust", "tmp"),
		// mold links the mutants, nextest runs them, and the dev/test profiles carry
		// no debuginfo — the per-mutant cost, not the gate (bellows #31a47b3: 14s
		// build + 30s test per mutant before this).
		"CARGO_PROFILE_DEV_DEBUG=0", "CARGO_PROFILE_TEST_DEBUG=0",
		// every test process under RLIMIT_DATA, so a runaway mutant fails its test
		// instead of meeting the engine's per-exec OOM kill
		"CARGO_TARGET_X86_64_UNKNOWN_LINUX_GNU_RUNNER=prlimit --data=4294967296 --",
		// ABOUT 8 CORES FOR THE LANE (2 mutants x 4): cargo jobs, nextest threads
		// and mold's link threads, each capped; the unset defaults were nproc apiece
		// (dev01 load average 96, 2026-10-12).
		"CARGO_BUILD_JOBS=4", "CARGO_BUILD_RUSTFLAGS=-C link-arg=-Wl,--thread-count=4", "NEXTEST_TEST_THREADS=4",
	} {
		if !strings.Contains(env, need) {
			t.Errorf("cargo mutants' environment lacks %s:\n%s", need, env)
		}
	}
	// THE COPIES BUILD IN THEIR OWN TARGET DIRECTORIES. cargo's artifact hash is
	// workspace-relative, so cargo-mutants' parallel copies sharing the gate's
	// target ran each other's test binaries and the gate ran theirs
	// (foundry-tools#8869): no program of this atom inherits the executor's.
	for _, cmd := range p.cmds {
		if cmd.Name == "cargo" || strings.HasSuffix(cmd.Name, "execmem") {
			if !reflect.DeepEqual(cmd.Unset, []string{"CARGO_TARGET_DIR"}) {
				t.Errorf("%s %v may inherit the shared target: unset %v", cmd.Name, cmd.Args, cmd.Unset)
			}
		}
	}
	// The diff cargo mutants reads is the one git printed, whole, with its final
	// newline; and the diff is taken over the declared modules, and the file list
	// over the same pathspec, renames split so a moved file names the crate it left.
	if raw, _ := os.ReadFile(filepath.Join(p.work, "rust", "pr.diff")); string(raw) != rustDiff+"\n" {
		t.Errorf("pr.diff = %q", raw)
	}
	if d, ok := p.call("git diff --relative since0 HEAD"); !ok || strings.Join(d.Args, " ") != "diff --relative since0 HEAD -- src/lib.rs" {
		t.Errorf("the diff: %+v", d)
	}
	if d, ok := p.call("--name-only -z"); !ok || strings.Join(d.Args, " ") != "diff --relative --no-renames --name-only -z since0 HEAD -- src/lib.rs" {
		t.Errorf("the file list: %+v", d)
	}
	// provisioning: the fetch and the probe, in that order, before the mutants
	var order []string
	for _, r := range p.runs {
		for _, w := range []string{"cargo fetch --locked", "cargo mutants --version", "cargo metadata", "cargo mutants --colors"} {
			if strings.HasPrefix(r, w) || strings.Contains(r, " "+w) {
				order = append(order, w)
			}
		}
	}
	if strings.Join(order, ",") != "cargo fetch --locked,cargo mutants --version,cargo metadata,cargo mutants --colors" {
		t.Errorf("order %v", order)
	}
	// A one-crate workspace names no package.
	if strings.Contains(strings.Join(c.Args, " "), " -p ") {
		t.Errorf("a single-package crate passes no -p: %q", c.Args)
	}
	for _, r := range p.runs {
		if strings.Contains(r, "sh -c") || strings.Contains(r, "bash") || strings.Contains(r, "MUT_") {
			t.Errorf("the atom ran %q", r)
		}
	}
}

// The linker the lane drives every mutant through is the lane image's. Where the
// image has none (the executor's rust toolchain), the mutants link the ordinary
// way and the mold-only link flag is not set: it would fail a default link.
func TestRustMutationLinksWithMoldOnlyWhereItIsInstalled(t *testing.T) {
	p := newPull(t, "critical_modules: src/lib.rs\n")
	moldAvailable = func() bool { return false }
	if v := p.run(); v.State != 0 {
		t.Fatalf("%s", said(v))
	}
	c, _ := p.call("cargo mutants --colors")
	if strings.Contains(strings.Join(c.Args, " "), "mold") || strings.Contains(strings.Join(c.Env, "\n"), "CARGO_BUILD_RUSTFLAGS") {
		t.Errorf("mold is absent and the argv or the environment still names it: %q %q", c.Args, c.Env)
	}
	if got := strings.Join(c.Args[:5], " "); got != "-- cargo mutants --colors never" {
		t.Errorf("argv starts %q", got)
	}
}

// Survivors are findings with the table and the list; the lists are read off
// mutants.out in the directory cargo mutants was told to write to.
func TestRustMutationSettlesWhatItMeasured(t *testing.T) {
	p := newPull(t, "critical_modules: src/lib.rs\n")
	p.lists["missed"] = "src/lib.rs:1:1: replace f -> i32 with 1\n"
	p.code = 2
	v := p.run()
	if v.State != 1 {
		t.Fatalf("%s", said(v))
	}
	for _, w := range []string{"scoped to the declared critical modules: src/lib.rs", "1 viable mutant(s) survived the suite",
		"| 1 | 1 | 0 | 0 | 50% of 2 viable |", "src/lib.rs:1:1: replace f -> i32 with 1"} {
		if !strings.Contains(said(v), w) {
			t.Errorf("the verdict lacks %q:\n%s", w, said(v))
		}
	}
	if len(v.Findings) != 1 || v.Findings[0].Cause != "mutant-missed" || v.Findings[0].Subject != "src/lib.rs:1" {
		t.Errorf("the survivor is a finding on the atom: %+v", v.Findings)
	}

	// EXIT 3 IS A REPORT (anvil@0c5355f): a timed-out mutant beside a caught one is
	// a pass, and the timeout rides the passing atom as a finding.
	q := newPull(t, "")
	q.lists = map[string]string{
		"caught":  "src/lib.rs:1:1: replace f -> i32 with 0\nsrc/lib.rs:1:1: replace f -> i32 with -1\n",
		"timeout": "src/lib.rs:1:1: replace f -> i32 with 1\n",
	}
	q.code = 3
	q.printed = "TIMEOUT  src/lib.rs:1:1: replace f -> i32 with 1 in 0s build + 60s test\n3 mutants tested in 2m: 2 caught, 1 timeouts\n"
	v = q.run()
	if v.State != 0 || len(v.Findings) != 1 || v.Findings[0].Cause != checks.MutantTimeoutCause || v.Findings[0].Verdict != checks.VerdictExcluded ||
		!strings.Contains(v.Findings[0].Detail, "after 60s of test") {
		t.Errorf("the timeout is a finding on the passing atom: %+v %s", v.Findings, said(v))
	}

	// a baseline that did not build measured nothing
	r := newPull(t, "")
	r.lists = nil
	r.code = 4
	r.printed = "Found 3 mutants\nERROR cargo test failed in an unmutated tree\n"
	v = r.run()
	if v.State != 2 {
		t.Fatalf("%s", said(v))
	}
	for _, w := range []string{"no critical modules declared", "cargo mutants exited 4", "ERROR cargo test failed in an unmutated tree"} {
		if !strings.Contains(said(v), w) {
			t.Errorf("the verdict lacks %q:\n%s", w, said(v))
		}
	}
}

// A KILLED RUN RAN (foundry-tools #13052): an exec ended with SIGKILL still
// carries what cargo mutants printed, and the survivors in it are the atom's
// findings — never "the atom never ran". The tree's mutants.out is gone with the
// exec, so a list the test leaves in it must not be read.
func TestRustMutationSalvagesAKilledRun(t *testing.T) {
	const printed = "Found 9 mutants to test\n" +
		"ok       Unmutated baseline in 1s build + 1s test\n" +
		"caught   src/lib.rs:1:1: replace f -> i32 with 0 in 1s build + 1s test\n" +
		"MISSED   src/lib.rs:1:1: replace f -> i32 with 1 in 1s build + 1s test"
	for _, c := range []struct {
		name     string
		out      string
		state    int
		reason   []string
		findings int
	}{
		{"survivors printed before the kill", printed, 1,
			[]string{"cargo mutants was killed (SIGKILL 137) after 2 of 9 mutants, before its summary — 7 were never graded", "| 1 | 1 | 0 | 0 | 50% of 2 viable |"}, 2},
		{"execmem's last reading rides the output", printed + "\nexec-memory: peak_bytes=17179860992 memory_max=17179869184 oom_kill=1", 1,
			[]string{"after 2 of 9 mutants, out of memory (oom_kill=1 at peak_bytes=17179860992 of memory_max=17179869184)"}, 2},
		{"killed before any mutant", "Found 9 mutants to test", 2,
			[]string{"CANNOT RUN - cargo mutants was killed (SIGKILL 137) after 0 of 9 mutants", "Nothing was measured"}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newPull(t, "")
			p.lists = map[string]string{"missed": "src/stale.rs:1:1: not this run\n"}
			p.code, p.printed = 137, c.out
			v := p.run()
			if v.State != c.state {
				t.Fatalf("state %d:\n%s", v.State, said(v))
			}
			for _, w := range c.reason {
				if !strings.Contains(said(v), w) {
					t.Errorf("the verdict lacks %q:\n%s", w, said(v))
				}
			}
			if strings.Contains(v.Reason, "never ran") || strings.Contains(v.Reason, "stale.rs") {
				t.Errorf("reason:\n%s", v.Reason)
			}
			if len(v.Findings) != c.findings {
				t.Errorf("findings %+v", v.Findings)
			}
		})
	}
	// 128 is an exit the engine refuses to read, not a signal: no salvage, still
	// never-ran. And a start that failed is never-ran too.
	for code, want := range map[int]string{128: "CANNOT RUN - the atom never ran: exit code: 128", -1: "CANNOT RUN - the atom never ran: execmem: not found"} {
		p := newPull(t, "")
		p.code, p.printed = code, printed
		if code < 0 {
			p.printed = "execmem: not found"
		}
		if v := p.run(); v.State != 2 || !strings.Contains(said(v), want) {
			t.Errorf("exit %d: %s", code, said(v))
		}
	}
}

// AN EMPTY DECLARATION IS NOT AN OPT-OUT: the whole diff's rust sources are the
// scope, no -f narrows the run, and every workspace member the pull touched is
// passed as -p.
func TestRustMutationScopesAnUndeclaredPullToTheWholeDiffAndItsMembers(t *testing.T) {
	p := newPull(t, "")
	root := p.in.Root
	p.first(rule{"--name-only -z", "src/lib.rs\x00tools/gen/src/lib.rs\x00", 0})
	p.first(rule{"cargo metadata", `{"packages":[` +
		`{"name":"x","id":"x","manifest_path":"` + root + `/Cargo.toml"},` +
		`{"name":"gen","id":"gen","manifest_path":"` + root + `/tools/gen/Cargo.toml"}],` +
		`"workspace_members":["x","gen"]}`, 0})
	if v := p.run(); v.State != 0 {
		t.Fatalf("%s", said(v))
	}
	d, _ := p.call("git diff --relative since0 HEAD")
	if strings.Join(d.Args, " ") != "diff --relative since0 HEAD -- *.rs :(glob,exclude)**/tests/**" {
		t.Errorf("an undeclared pull is diffed over every rust source outside any tests/ directory: %q", d.Args)
	}
	c, _ := p.call("cargo mutants --colors")
	if !strings.Contains(strings.Join(c.Args, " "), "--test-tool nextest -p gen -p x --exclude-re") || strings.Contains(strings.Join(c.Args, " "), " -f ") {
		t.Errorf("an empty declaration narrows nothing with -f and names the members: %q", c.Args)
	}

	// The value is the answers file's, one leading and one trailing quote stripped,
	// and each module is its own pathspec and its own -f.
	q := newPull(t, "other: 1\ncritical_modules: 'src/lib.rs src/gate.rs'\n")
	q.lists["missed"] = "src/lib.rs:1:1: replace f -> i32 with 1\n"
	q.code = 2
	v := q.run()
	if v.State != 1 || !strings.Contains(said(v), "scoped to the declared critical modules: src/lib.rs src/gate.rs") {
		t.Fatalf("%s", said(v))
	}
	c, _ = q.call("cargo mutants --colors")
	if !strings.Contains(strings.Join(c.Args, " "), "--test-tool nextest -f src/lib.rs -f src/gate.rs --exclude-re") {
		t.Errorf("each module is its own -f: %q", c.Args)
	}
}

func TestRustMutationStandsDownOrCannotRun(t *testing.T) {
	const baseNeedle = "rev-parse --verify --quiet abc123^{commit}"
	for _, tc := range []struct {
		name   string
		base   string
		script func(p *pull)
		state  int
		// reason is checked off a could-not-run: a pass keeps no output
		// (checks.VerdictOf), so a stand-down is told apart by what it ran.
		reason  []string
		reached []string
		never   []string
	}{
		{"no base", "-", nil, 0, nil, nil, []string{baseNeedle, "cargo fetch"}},
		{"a base the history lacks", "abc123", func(p *pull) { p.first(rule{baseNeedle, "", 1}) }, 0, nil,
			[]string{baseNeedle}, []string{"merge-base", "git diff"}},
		{"no merge base", "abc123", func(p *pull) { p.first(rule{"merge-base", "", 1}) }, 2,
			[]string{"no merge base between the base abc123 and HEAD (exit 1)"}, []string{"merge-base"}, []string{"git diff"}},
		{"no rust changed", "abc123", func(p *pull) { p.first(rule{"git diff --relative since0 HEAD", "", 0}) }, 0, nil,
			[]string{"git diff --relative"}, []string{"--name-only", "cargo fetch"}},
		{"lines only removed", "abc123", func(p *pull) {
			p.first(rule{"git diff --relative since0 HEAD", "--- a/src/lib.rs\n+++ b/src/lib.rs\n@@ -1,2 +1 @@\n fn f() {}\n-fn g() {}", 0})
		}, 0, nil, []string{"git diff --relative"}, []string{"--name-only", "cargo fetch"}},
		{"git cannot diff", "abc123", func(p *pull) { p.first(rule{"git diff --relative since0 HEAD", "bad object", 1}) }, 2,
			[]string{"git could not diff the pull against its base since0: bad object"}, nil, []string{"--name-only"}},
		{"git cannot list the files", "abc123", func(p *pull) { p.first(rule{"--name-only -z", "bad object", 1}) }, 2,
			[]string{"git could not list the files the pull changed: bad object"}, nil, []string{"cargo metadata"}},
		{"cargo metadata fails", "abc123", func(p *pull) { p.first(rule{"cargo metadata", "error: failed to parse manifest", 101}) }, 2,
			[]string{"cargo metadata failed", "failed to parse manifest"}, nil, []string{"cargo mutants --colors"}},
		{"cargo metadata is not json", "abc123", func(p *pull) { p.first(rule{"cargo metadata", "warning: x", 0}) }, 2,
			[]string{"could not map the diff onto the workspace members"}, nil, []string{"cargo mutants --colors"}},
		{"the fetch fails", "abc123", func(p *pull) { p.first(rule{"cargo fetch", "failed to download `deranged v0.5.8`", 101}) }, 2,
			[]string{"CANNOT RUN - preflight `cargo fetch --locked` exited 101: failed to download `deranged v0.5.8`"}, nil, []string{"cargo metadata"}},
		{"no cargo-mutants in the image", "abc123", func(p *pull) { p.first(rule{"cargo mutants --version", "error: no such command: `mutants`", 101}) }, 2,
			[]string{"CANNOT RUN - preflight `cargo mutants --version` exited 101: error: no such command: `mutants`"}, nil, []string{"cargo metadata"}},
		{"the base check never ran", "abc123", func(p *pull) { p.first(rule{baseNeedle, "git: not found", -1}) }, 2,
			[]string{"the atom never ran: git: not found"}, nil, []string{"merge-base"}},
		{"the diff never ran", "abc123", func(p *pull) { p.first(rule{"git diff --relative since0 HEAD", "git: not found", -1}) }, 2,
			[]string{"the atom never ran: git: not found"}, nil, []string{"--name-only"}},
		{"the diff was killed", "abc123", func(p *pull) { p.first(rule{"git diff --relative since0 HEAD", "Killed", 137}) }, 2,
			[]string{"the atom never ran: exit code: 137"}, nil, []string{"--name-only"}},
		{"the file list never ran", "abc123", func(p *pull) { p.first(rule{"--name-only -z", "git: not found", -1}) }, 2,
			[]string{"the atom never ran: git: not found"}, nil, []string{"cargo metadata"}},
		{"cargo metadata never ran", "abc123", func(p *pull) { p.first(rule{"cargo metadata", "cargo: not found", -1}) }, 2,
			[]string{"the atom never ran: cargo: not found"}, nil, []string{"cargo mutants --colors"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPull(t, "")
			if tc.base == "-" {
				p.in.Base = ""
			}
			if tc.script != nil {
				tc.script(p)
			}
			v := p.run()
			if v.State != tc.state {
				t.Fatalf("state %d (%s), want %d:\n%s", v.State, v.Result, tc.state, v.Reason)
			}
			for _, w := range tc.reason {
				if !strings.Contains(said(v), w) {
					t.Errorf("the verdict lacks %q:\n%s", w, said(v))
				}
			}
			if tc.state != 1 && p.ran("cargo mutants --colors") {
				t.Errorf("settled before the mutate, and still went on to cargo mutants")
			}
			for _, n := range tc.reached {
				if !p.ran(n) {
					t.Errorf("never reached %s", n)
				}
			}
			for _, n := range tc.never {
				if p.ran(n) {
					t.Errorf("went on to %s: %v", n, p.runs)
				}
			}
		})
	}
}

// An execmem link or a work directory the atom cannot make is the atom's
// could-not-run, not a panic.
func TestRustMutationCannotRunWhereItCannotWrite(t *testing.T) {
	p := newPull(t, "")
	putFile(t, p.work, "rust", "a file where the work directory goes")
	v := p.run()
	if v.State != 2 || !strings.Contains(said(v), "CANNOT RUN") {
		t.Errorf("%s", said(v))
	}
	if p.ran("cargo mutants --colors") {
		t.Error("cargo mutants ran with nowhere to write")
	}
}

// The Rust lane keys the crate its diff touched and hands back its grading,
// trusted only when the run measured.
func TestRustMutationGradesTheCrateItTouched(t *testing.T) {
	p := newPull(t, "")
	p.lists["missed"] = "src/lib.rs:1:1: replace f -> i32 with 1\n"
	p.code = 2
	v := p.run()
	if v.State != 1 || len(v.Gradings) != 1 {
		t.Fatalf("%s %+v", said(v), v.Gradings)
	}
	g := v.Gradings[0]
	entries := checks.ParseLsTree(rustTree)
	if g.Unit != "." || g.Lang != "rust" || g.Engine != rustMutationEngine() || !g.Reusable ||
		g.Counts.Caught != 1 || g.Counts.Missed != 1 || len(g.Mutants) != 1 || g.Mutants[0].Op != "replace f -> i32 with 1" ||
		g.Hash != unitkey.Hash(unitkey.Rust, entries, []string{"."}) ||
		g.Ranges != unitkey.Ranges(unitkey.Rust, entries, ".", map[string][]unitkey.Range{"src/lib.rs": {{Start: 1, End: 1}}}) {
		t.Fatalf("grading = %+v", g)
	}

	q := newPull(t, "")
	q.lists, q.code, q.printed = nil, 4, "Found 3 mutants\nERROR\n"
	v = q.run()
	if len(v.Gradings) != 1 || v.Gradings[0].Reusable || v.Gradings[0].WhyNot != checks.WhyNotUntrusted {
		t.Fatalf("a run that could not measure is stored untrusted: %+v", v.Gradings)
	}
}

// A repository whose mutants config tests the whole workspace keys each crate
// over the whole workspace.
func TestAWideMutantsConfigWidensEveryClosure(t *testing.T) {
	p := newPull(t, "")
	root := p.in.Root
	putFile(t, root, ".cargo/mutants.toml", "test_workspace = true\n")
	p.first(rule{"--name-only -z", "a/src/lib.rs\x00", 0})
	p.first(rule{"cargo metadata", `{"packages":[{"name":"a","id":"a","manifest_path":"` + root + `/a/Cargo.toml","dependencies":[]},` +
		`{"name":"b","id":"b","manifest_path":"` + root + `/b/Cargo.toml","dependencies":[{"name":"a","path":"` + root + `/a"}]}],"workspace_members":["a","b"]}`, 0})
	p.first(rule{"ls-tree", "100644 blob w\tCargo.toml\x00100644 blob am\ta/Cargo.toml\x00100644 blob a\ta/src/lib.rs\x00100644 blob bm\tb/Cargo.toml\x00100644 blob b\tb/src/lib.rs\x00", 0})
	v := p.run()
	// The fixture's caught mutant sits in src/lib.rs, which no crate of this tree
	// owns: it lands in an unkeyable grading of its own, after a's.
	if len(v.Gradings) != 2 || v.Gradings[0].Unit != "a" || !reflect.DeepEqual(v.Gradings[0].Closure, []string{"a", "b"}) || v.Gradings[1].Reusable {
		t.Fatalf("gradings = %+v", v.Gradings)
	}
}

// Metadata that does not parse keys nothing — the atom has already refused such a
// run, and the keys say so on their own. A tree git could not read is unkeyable
// too, said in its own words.
func TestRustKeysOverUnreadableInputsAreUnkeyable(t *testing.T) {
	p := newPull(t, "")
	g := &grading{in: p.in, tree: p.in.Root}
	keys, _ := g.gradingKeys(t.Context(), rustDiff, "src/lib.rs", "not json", "")
	if len(keys) != 1 || !strings.Contains(keys[0].Err, "cargo metadata did not parse") {
		t.Fatalf("keys = %+v", keys)
	}
	// a tree git could not read keys no unit at all, and says why in its words
	for _, tc := range []struct {
		out  string
		code int
		why  string
	}{
		{"", 0, ""}, {"fatal", 128, "git ls-tree exited 128"}, {"git: not found", -1, "git ls-tree never ran: git: not found"},
	} {
		if got := readFailed("git ls-tree", tc.out, tc.code); got != tc.why {
			t.Errorf("readFailed(%q, %d) = %q, want %q", tc.out, tc.code, got, tc.why)
		}
	}
	q := newPull(t, "")
	q.first(rule{"ls-tree", "fatal", 128})
	gq := &grading{in: q.in, tree: q.in.Root}
	if keys, _ := gq.gradingKeys(t.Context(), rustDiff, "src/lib.rs", `{"packages":[]}`, ""); len(keys) != 0 {
		t.Errorf("a tree git could not read keyed %+v", keys)
	}
}

// A tree whose files are locked is copied before it is built in (the executor's
// /src); the history stays with the original.
func TestALockedTreeIsBuiltInACopy(t *testing.T) {
	p := newPull(t, "")
	if err := os.Chmod(filepath.Join(p.in.Root, "Cargo.toml"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(p.in.Root, "src/lib.rs"), 0o444); err != nil {
		t.Fatal(err)
	}
	if v := p.run(); v.State != 0 {
		t.Fatalf("%s", said(v))
	}
	c, _ := p.call("cargo mutants --colors")
	if c.Dir != filepath.Join(p.work, "rust", "src") {
		t.Errorf("a locked tree is built in its copy, not at %s", c.Dir)
	}
	if d, _ := p.call("git diff --relative since0 HEAD"); d.Dir != p.in.Root {
		t.Errorf("the history is the original's: git ran in %s", d.Dir)
	}
	if info, err := os.Stat(filepath.Join(c.Dir, "src/lib.rs")); err != nil || info.Mode().Perm()&0o200 == 0 {
		t.Errorf("the copy is not writable: %v %v", info, err)
	}
}

var realMold = moldAvailable

// The lane image's linker is found on the PATH, and a PATH without it has none.
func TestMoldIsFoundOnThePath(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if realMold() {
		t.Error("mold found on an empty PATH")
	}
	putFile(t, bin, "mold", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(bin, "mold"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !realMold() {
		t.Error("mold not found")
	}
}

// What the atom writes or links to run cargo mutants, and cannot, is its
// could-not-run — and cargo mutants does not run.
func TestRustMutationCannotRunWhereItCannotLinkWriteOrCopy(t *testing.T) {
	for name, set := range map[string]func(t *testing.T){
		"the diff cannot be written": func(t *testing.T) {
			writeFile = func(string, []byte, os.FileMode) error { return os.ErrPermission }
			t.Cleanup(func() { writeFile = os.WriteFile })
		},
		"execmem cannot be linked": func(t *testing.T) {
			linkHelper = func(string, string) (string, error) { return "", os.ErrPermission }
			t.Cleanup(func() { linkHelper = atoms.LinkHelper })
		},
		"the tree cannot be copied": func(t *testing.T) {
			workingTree = func(string, string) (string, bool, error) { return "", false, os.ErrPermission }
			t.Cleanup(func() { workingTree = atoms.WorkingTree })
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := newPull(t, "")
			set(t)
			v := p.run()
			if v.State != 2 || !strings.Contains(said(v), "CANNOT RUN") || !strings.Contains(said(v), "permission denied") || p.ran("cargo mutants --colors") {
				t.Errorf("%s", said(v))
			}
		})
	}
}

// A rustc that will not say what it is costs the cap and nothing else: the
// mutants still run, with no link flag.
func TestRustMutationRunsWithoutTheCapWhenRustcWillNotSay(t *testing.T) {
	p := newPull(t, "critical_modules: src/lib.rs\n")
	moldAvailable = func() bool { return false }
	p.first(rule{"rustc -vV", "rustc: broken", 1})
	if v := p.run(); v.State != 0 {
		t.Fatalf("%s", said(v))
	}
	c, _ := p.call("cargo mutants --colors")
	if strings.Contains(strings.Join(c.Env, "\n"), "CARGO_BUILD_RUSTFLAGS") {
		t.Errorf("a link flag with no toolchain: %q", c.Env)
	}
}
