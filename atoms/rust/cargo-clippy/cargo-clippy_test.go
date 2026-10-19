package rustcargoclippy

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

// fake is an Exec that records every run (directory, argv line, environment) and
// answers from its rules, first match wins; a run no rule matches exits 0.
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
		Def.Toolchain != "rust" || Def.Surface != "root:Cargo.toml" || Def.NotOnForks != checks.ForkFleetStyle ||
		Def.SubsumedBy != "" || !slices.Equal(Def.Needs, []string{"tool:python-standalone", "tool:uv"}) || !Def.WritesTree {
		t.Errorf("%+v", Def)
	}
}

// The fleet's lint set is named on the command line, after `--`, so a crate's
// own [workspace.lints.clippy] table cannot narrow it. The tree is dated first,
// and cargo runs bounded and lean on debuginfo.
func TestClippyNamesTheFleetsLintSetAfterTheSeparator(t *testing.T) {
	f := &fake{}
	v := check(t.Context(), input(t, f))
	if v.State != 0 || v.Reason != "rust:cargo-clippy: PASS" {
		t.Fatalf("%+v", v)
	}
	want := "cargo clippy --workspace --all-targets -- -W clippy::all -D warnings"
	if len(f.runs) != 2 || !strings.HasPrefix(f.runs[0], "find . -path ./.git -prune") || f.runs[1] != want {
		t.Fatalf("ran %v", f.runs)
	}
	env := strings.Join(f.envs[want], " ")
	if env != "CARGO_BUILD_JOBS=6 CARGO_INCREMENTAL=0 CARGO_PROFILE_DEV_DEBUG=line-tables-only CARGO_PROFILE_TEST_DEBUG=line-tables-only" {
		t.Errorf("cargo ran under %q", env)
	}
	// Clippy links nothing it needs mold for: no linker flag, no wrapper.
	if strings.Contains(env, "RUSTFLAGS") || f.ran("mold") {
		t.Errorf("clippy must not carry the link levers: %s %v", env, f.runs)
	}
}

// A denied lint fails the BUILD, and cargo reports a failed build as 101. Reading
// that as could-not-run is the whole reason CargoExit exists.
func TestClippyReadsCargosExit(t *testing.T) {
	for _, tc := range []struct {
		code  int
		out   string
		state int
		word  string
	}{
		{101, "error: this expression creates a reference immediately dereferenced", 1, "immediately dereferenced"},
		{1, "", 1, "FINDINGS"},
		{137, "", 2, "CANNOT RUN"},
		{-1, "exec: cargo: not found", 2, "the atom never ran"},
	} {
		f := &fake{rules: []rule{{"cargo clippy", tc.out, tc.code}}}
		v := check(t.Context(), input(t, f))
		if v.State != tc.state || !strings.Contains(v.Reason, tc.word) {
			t.Errorf("exit %d: %+v", tc.code, v)
		}
	}
}

// A stamp that fails is the atom's could-not-run, and cargo is not run.
func TestAFailedStampStopsTheLint(t *testing.T) {
	f := &fake{rules: []rule{{"touch", "find: boom", 1}}}
	v := check(t.Context(), input(t, f))
	if v.State != 2 || !strings.Contains(v.Reason, "rust:cargo-clippy: CANNOT RUN - preflight") || f.ran("cargo clippy") {
		t.Errorf("%+v ran %v", v, f.runs)
	}
}

// A script that prints a second line for clippy gets it linted, not dropped.
func TestClippyLintsEverySetTheScriptNames(t *testing.T) {
	f := &fake{}
	in := scoping(t, f, "crates/circuit/src/lib.rs\x00", "-p stellar-core-circuit\n-p stellar-core-conformance\n")
	if v := check(t.Context(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	for _, w := range []string{
		"cargo clippy -p stellar-core-circuit --all-targets -- -W clippy::all -D warnings",
		"cargo clippy -p stellar-core-conformance --all-targets -- -W clippy::all -D warnings",
	} {
		if !f.ran(w) {
			t.Errorf("never ran %q: %v", w, f.runs)
		}
	}
	if f.ran("--workspace") {
		t.Errorf("a scoped pull must not lint the whole workspace: %v", f.runs)
	}

	// a red in the second set is the verdict, and the first set's green does not hide it
	f = &fake{rules: []rule{{"-p stellar-core-conformance --all-targets", "error: x", 101}}}
	in = scoping(t, f, "crates/circuit/src/lib.rs\x00", "-p stellar-core-circuit\n-p stellar-core-conformance\n")
	if v := check(t.Context(), in); v.State != 1 {
		t.Errorf("%+v", v)
	}
	// and a red in the first stops there
	f = &fake{rules: []rule{{"-p stellar-core-circuit --all-targets", "error: x", 101}}}
	in = scoping(t, f, "crates/circuit/src/lib.rs\x00", "-p stellar-core-circuit\n-p stellar-core-conformance\n")
	if v := check(t.Context(), in); v.State != 1 || f.ran("-p stellar-core-conformance --all-targets") {
		t.Errorf("%+v ran %v", v, f.runs)
	}
}

func TestClippyLintsOnlyTheCratesAPullReaches(t *testing.T) {
	f := &fake{}
	in := scoping(t, f, "crates/circuit/src/lib.rs\x00", "-p stellar-core-circuit -p stellar-core-conformance\n")
	if v := check(t.Context(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	if !f.ran("cargo clippy -p stellar-core-circuit -p stellar-core-conformance --all-targets -- -W clippy::all -D warnings") || f.ran("--workspace") {
		t.Errorf("ran %v", f.runs)
	}
}

// A pull that touched nothing a crate compiles has nothing to lint: absent, and
// cargo never runs.
func TestAPullThatTouchedNoCrateHasNothingToLint(t *testing.T) {
	f := &fake{}
	in := scoping(t, f, "README.md\x00", "-p never\n")
	v := check(t.Context(), in)
	if v.State != 0 || v.Result != "absent" || !strings.Contains(strings.Join(v.Logs, "\n"), "this pull changed nothing a crate compiles") || f.ran("cargo clippy") || f.ran("touch") {
		t.Errorf("%+v ran %v", v, f.runs)
	}
}
