package rustcargotest

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// rule answers an exec whose argv line contains the needle.
type rule struct {
	needle string
	out    string
	code   int
}

// fake is an Exec that records every run (argv line, environment) and answers
// from its rules, first match wins; a --list names one test, anything else exits 0.
type fake struct {
	rules []rule
	runs  []string
	envs  map[string][]string
}

func (f *fake) exec(_ context.Context, c atoms.Cmd) (string, int) {
	line := strings.Join(append([]string{c.Name}, c.Args...), " ")
	f.runs = append(f.runs, line)
	if f.envs == nil {
		f.envs = map[string][]string{}
	}
	f.envs[line] = c.Env
	for _, r := range f.rules {
		if strings.Contains(line, r.needle) {
			return r.out, r.code
		}
	}
	if strings.HasSuffix(line, "-- --list") {
		return "checks::lane::works: test\n\n1 test\n", 0
	}
	return "", 0
}

func (f *fake) ran(needle string) bool {
	return slices.ContainsFunc(f.runs, func(r string) bool { return strings.Contains(r, needle) })
}

func input(t *testing.T, f *fake) atomkit.In {
	t.Helper()
	return atomkit.In{Root: t.TempDir(), Files: []string{"Cargo.toml"}, Exec: f.exec}
}

// scoping makes the tree a repo that scopes its crates and a pull of files.
func scoping(t *testing.T, f *fake, files, scriptOut string) atomkit.In {
	t.Helper()
	in := input(t, f)
	if err := os.MkdirAll(filepath.Join(in.Root, "tools", "scope"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.Root, checks.RustScopeScript), []byte("#!/usr/bin/env python3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := `{"workspace_members":["root","circ","conf"],"packages":[
	 {"name":"stellar-core","id":"root","manifest_path":"` + in.Root + `/Cargo.toml"},
	 {"name":"stellar-core-circuit","id":"circ","manifest_path":"` + in.Root + `/crates/circuit/Cargo.toml"},
	 {"name":"stellar-core-conformance","id":"conf","manifest_path":"` + in.Root + `/crates/conformance/Cargo.toml"}]}`
	f.rules = append(f.rules,
		rule{"git merge-base", "since0\n", 0},
		rule{"--name-only -z", files, 0},
		rule{"cargo metadata", meta, 0},
		rule{"python3 tools/scope/scope.py", scriptOut, 0})
	in.Base = "abc123"
	return in
}

func TestTheDeclarationValidatesAndNamesItsRow(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneRust || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "rust" || Def.Surface != "root:Cargo.toml" || Def.NotOnForks != "" ||
		Def.SubsumedBy != "" || len(Def.Needs) != 0 || !Def.WritesTree {
		t.Errorf("%+v", Def)
	}
}

// cargo test asks the toolchain what tests exist BEFORE it runs them, after the
// tree is dated, under mold, bounded, lean on debuginfo and capped at the link.
func TestTheSuiteIsListedBeforeItIsRun(t *testing.T) {
	f := &fake{}
	v := check(t.Context(), input(t, f))
	if v.State != 0 || v.Reason != "rust:cargo-test: PASS" {
		t.Fatalf("%+v", v)
	}
	list, run := "mold -run cargo test --workspace -- --list", "mold -run cargo test --workspace"
	if len(f.runs) != 3 || !strings.HasPrefix(f.runs[0], "find . -path ./.git -prune") || f.runs[1] != list || f.runs[2] != run {
		t.Fatalf("ran %v", f.runs)
	}
	want := "CARGO_BUILD_JOBS=6 CARGO_INCREMENTAL=0 CARGO_PROFILE_DEV_DEBUG=line-tables-only CARGO_PROFILE_TEST_DEBUG=line-tables-only CARGO_BUILD_RUSTFLAGS=-C link-arg=-Wl,--thread-count=4"
	for _, line := range []string{list, run} {
		if got := strings.Join(f.envs[line], " "); got != want {
			t.Errorf("%s ran under %q", line, got)
		}
	}
}

// The suite itself, in cargo's vocabulary.
func TestTheSuiteReadsCargosExit(t *testing.T) {
	for _, tc := range []struct {
		code  int
		out   string
		state int
		word  string
	}{
		{101, "test lane::works ... FAILED", 1, "FAILED"},
		{1, "", 1, "FINDINGS"},
		{137, "", 2, "CANNOT RUN"},
		{-1, "exec: mold: not found", 2, "the atom never ran"},
	} {
		f := &fake{}
		in := input(t, f)
		// the listing answers; the run is the exec under test
		in.Exec = (&runExit{fake: f, out: tc.out, code: tc.code}).exec
		v := check(t.Context(), in)
		if v.State != tc.state || !strings.Contains(v.Reason, tc.word) {
			t.Errorf("exit %d: %+v", tc.code, v)
		}
	}
}

// runExit answers the suite run (the exec that is neither a listing nor a stamp)
// with the exit under test.
type runExit struct {
	*fake
	out  string
	code int
}

func (g *runExit) exec(ctx context.Context, c atoms.Cmd) (string, int) {
	out, code := g.fake.exec(ctx, c)
	if strings.Join(append([]string{c.Name}, c.Args...), " ") == "mold -run cargo test --workspace" {
		return g.out, g.code
	}
	return out, code
}

// A listing that would not BUILD is a finding naming the first rustc diagnostic
// — not a could-not-run, and not the whole 400-line fallout.
func TestAListingThatWouldNotBuildIsAFindingNamingTheFirstDiagnostic(t *testing.T) {
	f := &fake{rules: []rule{{"-- --list", "error[E0425]: cannot find value `nope` in this scope\nerror: aborting due to 1 previous error\n", 101}}}
	v := check(t.Context(), input(t, f))
	if v.State != 1 || !strings.Contains(v.Reason, "the tests did not build") || !strings.Contains(v.Reason, "error[E0425]: cannot find value `nope`") || strings.Contains(v.Reason, "aborting due to") {
		t.Errorf("%+v", v)
	}
	if slices.Contains(f.runs, "mold -run cargo test --workspace") || len(f.runs) != 2 {
		t.Errorf("a listing that would not build must not run the suite: %v", f.runs)
	}

	// a build that prints nothing matching still files the finding: the exit decided it
	f = &fake{rules: []rule{{"-- --list", "the linker exploded\n", 101}}}
	if v = check(t.Context(), input(t, f)); v.State != 1 || !strings.Contains(v.Reason, "the tests did not build") {
		t.Errorf("%+v", v)
	}

	// a program that would not start never ran
	f = &fake{rules: []rule{{"-- --list", "exec: mold: not found", -1}}}
	if v = check(t.Context(), input(t, f)); v.State != 2 || !strings.Contains(v.Reason, "the atom never ran: exec: mold: not found") {
		t.Errorf("%+v", v)
	}
}

// libtest exits 0 and prints "running 0 tests" for a workspace with no #[test].
// That is the green this atom exists to refuse; a benchmark is not a test.
func TestAWorkspaceWithNoTestIsAFinding(t *testing.T) {
	for _, list := range []string{"\n0 tests, 0 benchmarks\n", "bench::throughput: benchmark\n\n0 tests, 1 benchmark\n"} {
		f := &fake{rules: []rule{{"-- --list", list, 0}}}
		v := check(t.Context(), input(t, f))
		if v.State != 1 || !strings.Contains(v.Reason, "no test in the workspace") || len(f.runs) != 2 {
			t.Errorf("%q: %+v ran %v", list, v, f.runs)
		}
	}
}

func TestAFailedStampStopsTheSuite(t *testing.T) {
	f := &fake{rules: []rule{{"touch", "find: boom", 1}}}
	v := check(t.Context(), input(t, f))
	if v.State != 2 || !strings.Contains(v.Reason, "rust:cargo-test: CANNOT RUN - preflight") || f.ran("cargo test") {
		t.Errorf("%+v ran %v", v, f.runs)
	}
}

// A repo that scopes per crate runs one list and one run per argument set, and no
// --workspace anywhere; a red in the second set is the verdict.
func TestTheSuiteRunsOnlyTheCratesAPullReaches(t *testing.T) {
	const second = "-p stellar-core-conformance --test circuit_conformance --test compose"
	f := &fake{}
	in := scoping(t, f, "crates/circuit/src/lib.rs\x00", "-p stellar-core-circuit\n"+second+"\n")
	if v := check(t.Context(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	for _, probe := range []string{
		"mold -run cargo test -p stellar-core-circuit -- --list",
		"mold -run cargo test " + second + " -- --list",
		"mold -run cargo test -p stellar-core-circuit",
		"mold -run cargo test " + second,
	} {
		if !slices.Contains(f.runs, probe) {
			t.Errorf("never ran %q: %v", probe, f.runs)
		}
	}
	if f.ran("--workspace") {
		t.Errorf("a scoped pull must not run the whole workspace: %v", f.runs)
	}

	// a red in the second set is the verdict, and the first set's green does not hide it
	f = &fake{rules: []rule{{"compose -- --list", "a::b: test\n", 0}, {"--test compose", "FAILED", 101}}}
	in = scoping(t, f, "crates/circuit/src/lib.rs\x00", "-p stellar-core-circuit\n"+second+"\n")
	if v := check(t.Context(), in); v.State != 1 {
		t.Errorf("%+v", v)
	}

	// a red in the first stops there
	f = &fake{rules: []rule{{"-- --list", "a::b: test\n", 0}, {"cargo test -p stellar-core-circuit", "FAILED", 101}}}
	in = scoping(t, f, "crates/circuit/src/lib.rs\x00", "-p stellar-core-circuit\n-p stellar-core-conformance\n")
	if v := check(t.Context(), in); v.State != 1 || slices.Contains(f.runs, "mold -run cargo test -p stellar-core-conformance") {
		t.Errorf("%+v ran %v", v, f.runs)
	}
}

// A pull that touched nothing a crate tests has nothing to run: absent, and cargo
// never runs.
func TestAPullThatTouchedNoCrateHasNothingToTest(t *testing.T) {
	f := &fake{}
	in := scoping(t, f, "README.md\x00", "-p never\n")
	v := check(t.Context(), in)
	if v.State != 0 || v.Result != "absent" || !strings.Contains(strings.Join(v.Logs, "\n"), "this pull changed nothing a crate tests") || f.ran("cargo test") || f.ran("touch") {
		t.Errorf("%+v ran %v", v, f.runs)
	}
}
