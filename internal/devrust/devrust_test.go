package devrust

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// fake is the Exec of a dev rust run: it records each program and answers the
// first rule whose prefix the command line starts with (exit 0, no output when
// none does).
type fake struct {
	rules []rule
	runs  []string
	envs  [][]string
}

type rule struct {
	prefix string
	out    string
	code   int
}

func (f *fake) exec(_ context.Context, c atoms.Cmd) (string, int) {
	line := strings.Join(append([]string{c.Name}, c.Args...), " ")
	f.runs = append(f.runs, line)
	f.envs = append(f.envs, c.Env)
	for _, r := range f.rules {
		if strings.HasPrefix(line, r.prefix) {
			return r.out, r.code
		}
	}
	return "", 0
}

// cargoRuns are the cargo programs the run started, the preflight first.
func (f *fake) cargoRuns() []string {
	var out []string
	for _, r := range f.runs {
		if strings.HasPrefix(r, "cargo ") && !strings.HasPrefix(r, "cargo metadata") || strings.HasPrefix(r, "mold ") {
			out = append(out, r)
		}
	}
	return out
}

const meta = `{"workspace_members":["facade","circ","conf"],"packages":[
 {"name":"stellar-core","id":"facade","manifest_path":"ROOT/rust/Cargo.toml"},
 {"name":"stellar-core-circuit","id":"circ","manifest_path":"ROOT/rust/crates/circuit/Cargo.toml"},
 {"name":"stellar-core-conformance","id":"conf","manifest_path":"ROOT/rust/crates/conformance/Cargo.toml"}]}`

// scopingIn is stellar-core's shape: a virtual workspace that carries the scope
// script, a dev commit whose change against its parent is diff, and a script
// that answers script.
func scopingIn(t *testing.T, diff, script string, args ...string) (atoms.Input, *fake) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tools", "scope"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, checks.RustScopeScript), []byte("#!/usr/bin/env python3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fake{rules: []rule{
		{"git merge-base HEAD~1 HEAD", "parent0\n", 0},
		{"git diff --relative --no-renames --name-only -z", diff, 0},
		{"cargo metadata", strings.ReplaceAll(meta, "ROOT", root), 0},
		{"python3 " + checks.RustScopeScript, script, 0},
	}}
	return atoms.Input{Root: root, Args: args, Exec: f.exec, Getenv: func(string) string { return "" }}, f
}

var test = Verb{ID: "dev:rust-test", Cargo: "test", Scope: "test", Link: true}

const twoSets = "-p stellar-core-circuit\n-p stellar-core-conformance --test circuit_conformance\n"

// RUSTDEV-1: a dev test whose change touches one crate runs that crate and what
// it reaches (the repo's scope script), not the whole workspace, measured from
// the dev commit's parent: the working tree against the HEAD it was asked from.
func TestRUSTDEV_1_AOneCrateChangeRunsTheCratesItReaches(t *testing.T) {
	in, f := scopingIn(t, "rust/crates/circuit/src/lib.rs\x00", twoSets)
	v := Check(t.Context(), in, test)
	if v.State != 0 {
		t.Fatalf("%+v", v)
	}
	want := []string{
		"cargo fetch --locked",
		"mold -run cargo test -p stellar-core-circuit",
		"mold -run cargo test -p stellar-core-conformance --test circuit_conformance",
	}
	if got := f.cargoRuns(); !slices.Equal(got, want) {
		t.Errorf("cargo ran %q, want %q", got, want)
	}
	if !slices.Contains(f.runs, "git diff --relative --no-renames --name-only -z parent0 HEAD") {
		t.Errorf("the change was not measured from the parent: %q", f.runs)
	}
	if !slices.ContainsFunc(v.Logs, func(l string) bool {
		return strings.Contains(l, "scoped to the change") && strings.Contains(l, "-p stellar-core-circuit")
	}) {
		t.Errorf("the logs do not show the plan: %q", v.Logs)
	}
}

// RUSTDEV-2: every doubt is the whole workspace, as today: no change at all, a
// workspace-level file (the root manifest, the lock), a repo without the scope
// script, a script that fails.
func TestRUSTDEV_2_EveryDoubtRunsTheWholeWorkspace(t *testing.T) {
	whole := []string{"cargo fetch --locked", "mold -run cargo test --workspace"}
	for name, mk := range map[string]func() (atoms.Input, *fake){
		"no change": func() (atoms.Input, *fake) { return scopingIn(t, "", twoSets) },
		"the workspace lock": func() (atoms.Input, *fake) {
			return scopingIn(t, "Cargo.lock\x00rust/crates/circuit/src/lib.rs\x00", twoSets)
		},
		"the root manifest":   func() (atoms.Input, *fake) { return scopingIn(t, "Cargo.toml\x00", twoSets) },
		"only prose":          func() (atoms.Input, *fake) { return scopingIn(t, "README.md\x00", twoSets) },
		"the script answered": func() (atoms.Input, *fake) { return scopingIn(t, "rust/crates/circuit/src/lib.rs\x00", "") },
		"no script": func() (atoms.Input, *fake) {
			in, f := scopingIn(t, "rust/crates/circuit/src/lib.rs\x00", twoSets)
			if err := os.Remove(filepath.Join(in.Root, checks.RustScopeScript)); err != nil {
				t.Fatal(err)
			}
			return in, f
		},
		"no parent": func() (atoms.Input, *fake) {
			in, f := scopingIn(t, "rust/crates/circuit/src/lib.rs\x00", twoSets)
			f.rules = append([]rule{{"git rev-parse", "", 1}}, f.rules...)
			return in, f
		},
	} {
		in, f := mk()
		v := Check(t.Context(), in, test)
		if got := f.cargoRuns(); v.State != 0 || !slices.Equal(got, whole) {
			t.Errorf("%s: cargo ran %q (state %d), want %q", name, got, v.State, whole)
		}
		if !slices.ContainsFunc(v.Logs, func(l string) bool { return strings.Contains(l, "the whole workspace") }) {
			t.Errorf("%s: the logs do not say the run is whole: %q", name, v.Logs)
		}
	}
}

// RUSTDEV-3: a caller who names the packages is honoured as asked, and the
// change is never read.
func TestRUSTDEV_3_ExplicitPackagesAreHonoured(t *testing.T) {
	for _, args := range [][]string{{"-p", "stellar-core-field"}, {"--workspace"}, {"--package=x"}, {"--manifest-path", "rust/Cargo.toml"}} {
		in, f := scopingIn(t, "rust/crates/circuit/src/lib.rs\x00", twoSets, args...)
		Check(t.Context(), in, test)
		want := []string{"cargo fetch --locked", "mold -run cargo test " + strings.Join(args, " ")}
		if got := f.cargoRuns(); !slices.Equal(got, want) {
			t.Errorf("%v: cargo ran %q, want %q", args, got, want)
		}
		if slices.ContainsFunc(f.runs, func(r string) bool { return strings.HasPrefix(r, "git diff") }) {
			t.Errorf("%v: the change was read though the caller chose: %q", args, f.runs)
		}
	}
}

// RUSTDEV-4: the caller's other arguments ride every scoped run, after the
// scope, so a filter or a `--` passthrough reaches each one.
func TestRUSTDEV_4_TheCallersArgumentsRideEachScopedRun(t *testing.T) {
	in, f := scopingIn(t, "rust/crates/circuit/src/lib.rs\x00", twoSets, "decide", "--", "--nocapture")
	Check(t.Context(), in, test)
	want := []string{
		"cargo fetch --locked",
		"mold -run cargo test -p stellar-core-circuit decide -- --nocapture",
		"mold -run cargo test -p stellar-core-conformance --test circuit_conformance decide -- --nocapture",
	}
	if got := f.cargoRuns(); !slices.Equal(got, want) {
		t.Errorf("cargo ran %q, want %q", got, want)
	}
}

// RUSTDEV-5: clippy and check scope by the script's clippy answer and keep
// their own defaults (devlane.RustArgv): --all-targets and the fleet's lints.
func TestRUSTDEV_5_ClippyAndCheckScopeAndKeepTheirDefaults(t *testing.T) {
	in, f := scopingIn(t, "rust/crates/circuit/src/lib.rs\x00", "-p stellar-core-circuit -p stellar-core-conformance\n")
	Check(t.Context(), in, Verb{ID: "dev:rust-clippy", Cargo: "clippy", Scope: "clippy"})
	want := "cargo clippy --all-targets -p stellar-core-circuit -p stellar-core-conformance -- -W clippy::all -D warnings"
	if got := f.cargoRuns(); len(got) != 2 || got[1] != want {
		t.Errorf("cargo ran %q, want %q", got, want)
	}
	if !slices.Contains(f.runs, "python3 "+checks.RustScopeScript+" clippy stellar-core-circuit") {
		t.Errorf("the script was not asked for clippy: %q", f.runs)
	}
	in, f = scopingIn(t, "", "")
	Check(t.Context(), in, Verb{ID: "dev:rust-check", Cargo: "check", Scope: "clippy"})
	if got := f.cargoRuns(); len(got) != 2 || got[1] != "cargo check --workspace" {
		t.Errorf("a bare check ran %q", got)
	}
}

// RUSTDEV-6: every cargo run is bounded as the gate's compiling atoms are: the
// jobs, no incremental cache, line tables only, and mold's threads for a verb
// that links.
func TestRUSTDEV_6_EveryCargoRunIsBounded(t *testing.T) {
	in, f := scopingIn(t, "rust/crates/circuit/src/lib.rs\x00", twoSets)
	Check(t.Context(), in, test)
	want := []string{"CARGO_BUILD_JOBS=6", "CARGO_INCREMENTAL=0", "CARGO_PROFILE_DEV_DEBUG=line-tables-only", "CARGO_PROFILE_TEST_DEBUG=line-tables-only", "CARGO_BUILD_RUSTFLAGS=" + checks.RustGateMoldThreadsFlag}
	for i, r := range f.runs {
		if !strings.HasPrefix(r, "cargo ") && !strings.HasPrefix(r, "mold ") || strings.HasPrefix(r, "cargo metadata") {
			continue
		}
		for _, w := range want {
			if !slices.Contains(f.envs[i], w) {
				t.Errorf("%q: env %q lacks %s", r, f.envs[i], w)
			}
		}
	}
	// a verb that does not link keeps the linker the star chose
	in, f = scopingIn(t, "", "")
	Check(t.Context(), in, Verb{ID: "dev:rust-check", Cargo: "check", Scope: "clippy"})
	if env := f.envs[len(f.envs)-1]; slices.Contains(env, "CARGO_BUILD_RUSTFLAGS="+checks.RustGateMoldThreadsFlag) {
		t.Errorf("check carries the mold flag: %q", env)
	}
}

// RUSTDEV-7: the toolchain's preflight still stands first (a stale lock is a
// could-not-run, as the command atoms had it), a red set is the verdict and
// stops the rest, and cargo's 101 is a finding.
func TestRUSTDEV_7_PreflightAndRedSets(t *testing.T) {
	in, f := scopingIn(t, "rust/crates/circuit/src/lib.rs\x00", twoSets)
	f.rules = append([]rule{{"cargo fetch --locked", "error: the lock file needs to be updated", 101}}, f.rules...)
	v := Check(t.Context(), in, test)
	if v.State != 2 || !strings.Contains(v.Reason, "preflight `cargo fetch --locked`") || len(f.cargoRuns()) != 1 {
		t.Errorf("a stale lock: %+v, ran %q", v, f.cargoRuns())
	}
	in, f = scopingIn(t, "rust/crates/circuit/src/lib.rs\x00", twoSets)
	f.rules = append([]rule{{"mold -run cargo test -p stellar-core-circuit", "test x ... FAILED", 101}}, f.rules...)
	v = Check(t.Context(), in, test)
	if v.State != 1 || len(f.cargoRuns()) != 2 || !strings.Contains(v.Reason, "FAILED") {
		t.Errorf("a red set: %+v, ran %q", v, f.cargoRuns())
	}
	in, f = scopingIn(t, "", "")
	f.rules = append([]rule{{"mold -run cargo test", "exec: mold: not found", -1}}, f.rules...)
	if v = Check(t.Context(), in, test); v.State != 2 {
		t.Errorf("a cargo that never started: %+v", v)
	}
}
