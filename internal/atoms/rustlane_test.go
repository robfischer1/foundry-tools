package atoms

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

var rustRow = checks.AtomDef{ID: "rust:x", Stage: checks.StagePrecommit, Lane: checks.LaneRust}

// Cargo answers 101, not 1, when a command failed: a finding, not a broken check.
func TestCargoVerdictReadsCargosExitInCargosVocabulary(t *testing.T) {
	for _, tc := range []struct {
		code  int
		state int
	}{{0, 0}, {1, 1}, {101, 1}, {137, 2}, {2, 2}, {-1, 2}} {
		v := CargoVerdict(rustRow, "out", tc.code)
		if v.State != tc.state {
			t.Errorf("exit %d is state %d, want %d: %+v", tc.code, v.State, tc.state, v)
		}
	}
	if v := CargoVerdict(rustRow, "exec: cargo: not found", -1); !strings.Contains(v.Reason, "the atom never ran: exec: cargo: not found") {
		t.Errorf("%+v", v)
	}
	if v := CargoVerdict(rustRow, "error: this expression creates a reference immediately dereferenced", 101); v.State != 1 || !strings.Contains(v.Reason, "immediately dereferenced") {
		t.Errorf("%+v", v)
	}
}

func TestCargoExecRunsInTheRootWithTheExtraEnvironment(t *testing.T) {
	f := &golaneFake{rules: []golaneRule{{"cargo clippy", "warning: x", 101}}}
	out, code := CargoExec(t.Context(), Input{Root: "/src", Exec: f.exec}, []string{"A=1"}, "cargo", "clippy", "--workspace")
	if out != "warning: x" || code != 101 || f.runs[0] != "/src: cargo clippy --workspace" || strings.Join(f.envs[0], ",") != "A=1" {
		t.Errorf("%q %d %v %v", out, code, f.runs, f.envs)
	}
}

// THE STAMP DATES THE TREE past every artifact, in the root, and a stamp that
// fails is the atom's could-not-run.
func TestRustStampDatesTheTreeAndAFailureIsACouldNotRun(t *testing.T) {
	f := &golaneFake{}
	if _, ok := RustStamp(t.Context(), Input{Root: "/src", Exec: f.exec}, rustRow); !ok {
		t.Fatal("a stamp that exited 0 failed")
	}
	if want := "/src: " + strings.Join(checks.Unstale(), " "); f.runs[0] != want {
		t.Errorf("ran %q, want %q", f.runs[0], want)
	}
	f = &golaneFake{rules: []golaneRule{{"touch", "find: read-only file system", 1}}}
	v, ok := RustStamp(t.Context(), Input{Root: "/src", Exec: f.exec}, rustRow)
	if ok || v.State != 2 || !strings.Contains(v.Reason, "rust:x: CANNOT RUN - preflight `find . -path ./.git -prune") || !strings.Contains(v.Reason, "read-only file system") {
		t.Errorf("%v %+v", ok, v)
	}
}

// THE PER-CRATE SCOPE (checks.RustScope): a repo that carries tools/scope/scope.py
// is graded on the crates a pull reaches, and on the whole workspace in every
// doubt.
const (
	rustscopeScript = "python3 tools/scope/scope.py"
	rustscopeMeta   = `{"workspace_members":["root","circ","conf"],"packages":[
	 {"name":"stellar-core","id":"root","manifest_path":"ROOT/Cargo.toml"},
	 {"name":"stellar-core-circuit","id":"circ","manifest_path":"ROOT/crates/circuit/Cargo.toml"},
	 {"name":"stellar-core-conformance","id":"conf","manifest_path":"ROOT/crates/conformance/Cargo.toml"}]}`
)

// rustscopeIn is a scoping repo (the script is in the tree) on which a pull of
// `files` is asked, and an Exec that answers git, cargo metadata and the script.
func rustscopeIn(t *testing.T, files, scriptOut string) (Input, *golaneFake) {
	t.Helper()
	in := treeIn(t, map[string]string{"tools/scope/scope.py": "#!/usr/bin/env python3\n", "Cargo.toml": ""})
	in.Base = "abc123"
	f := &golaneFake{rules: []golaneRule{
		{"git merge-base", "since0\n", 0},
		{"git diff --relative --no-renames --name-only -z", files, 0},
		{"cargo metadata", strings.ReplaceAll(rustscopeMeta, "ROOT", in.Root), 0},
		{rustscopeScript, scriptOut, 0},
	}}
	in.Exec = f.exec
	return in, f
}

func TestRustScopeNamesTheCratesAPullReaches(t *testing.T) {
	in, f := rustscopeIn(t, "crates/circuit/src/lib.rs\x00", "-p stellar-core-circuit\n-p stellar-core-conformance --test circuit_conformance --test compose\n")
	sets, ok := RustScope(t.Context(), in, "test")
	want := [][]string{{"-p", "stellar-core-circuit"}, {"-p", "stellar-core-conformance", "--test", "circuit_conformance", "--test", "compose"}}
	if !ok || len(sets) != 2 || !slices.Equal(sets[0], want[0]) || !slices.Equal(sets[1], want[1]) {
		t.Fatalf("%v %v", sets, ok)
	}
	// The change set carries BOTH sides of a rename: a file moved out of a crate
	// lists only the destination by default, and the source crate is never graded.
	// The script is asked about the crates the diff touched, for this verb.
	var ran []string
	for _, r := range f.runs {
		ran = append(ran, strings.TrimPrefix(r, in.Root+": "))
	}
	for _, w := range []string{
		"git diff --relative --no-renames --name-only -z since0 HEAD",
		"cargo metadata --no-deps --format-version 1",
		"python3 tools/scope/scope.py test stellar-core-circuit",
	} {
		if !slices.Contains(ran, w) {
			t.Errorf("never ran %q: %v", w, ran)
		}
	}
}

func TestRustScopeFallsBackToTheWholeWorkspaceInEveryDoubt(t *testing.T) {
	whole := func(name string, in Input) {
		t.Helper()
		if sets, ok := RustScope(t.Context(), in, "clippy"); ok || sets != nil {
			t.Errorf("%s: want the whole workspace, got %v %v", name, sets, ok)
		}
	}
	ok := "crates/circuit/src/lib.rs\x00"

	in, _ := rustscopeIn(t, ok+"Cargo.lock\x00", "-p stellar-core-circuit\n")
	whole("a file in the root package, beside a crate file", in)

	in, _ = rustscopeIn(t, ok, "-p stellar-core-circuit\n")
	in.Files = nil
	if err := removeScript(in); err != nil {
		t.Fatal(err)
	}
	whole("the repo did not opt in: no script in the tree", in)

	in, _ = rustscopeIn(t, ok, "")
	whole("the script answered nothing", in)

	in, f := rustscopeIn(t, ok, "boom\n")
	f.rules = append([]golaneRule{{rustscopeScript, "boom\n", 1}}, f.rules...)
	whole("the script failed", in)

	in, f = rustscopeIn(t, ok, "-p stellar-core-circuit\n")
	f.rules = append([]golaneRule{{"rev-parse", "", 1}}, f.rules...)
	whole("the base is not in this history", in)

	in, f = rustscopeIn(t, ok, "-p stellar-core-circuit\n")
	f.rules = append([]golaneRule{{"git merge-base", "fatal", 1}}, f.rules...)
	whole("no merge base", in)

	in, f = rustscopeIn(t, ok, "-p stellar-core-circuit\n")
	f.rules = append([]golaneRule{{"cargo metadata", "", 101}}, f.rules...)
	whole("cargo metadata failed", in)

	in, f = rustscopeIn(t, ok, "-p stellar-core-circuit\n")
	f.rules = append([]golaneRule{{"cargo metadata", "not json", 0}}, f.rules...)
	whole("cargo metadata did not parse", in)

	in, f = rustscopeIn(t, ok, "-p stellar-core-circuit\n")
	f.rules = append([]golaneRule{{"git diff", "fatal: bad", 128}}, f.rules...)
	whole("the diff would not list", in)

	// no base: a local snapshot has no history to measure from, and nothing is
	// asked of the scope script
	in, f = rustscopeIn(t, ok, "-p stellar-core-circuit\n")
	in.Base = ""
	whole("no base", in)
	if len(f.runs) != 0 {
		t.Errorf("with no base nothing is asked: %v", f.runs)
	}
}

// A pull that touched nothing a crate can feel has nothing to grade: scoped, with
// no sets.
func TestRustScopeAPullThatTouchedNoCrateHasNothingToGrade(t *testing.T) {
	in, f := rustscopeIn(t, "README.md\x00CLAUDE-INIT.md\x00", "-p never\n")
	sets, ok := RustScope(t.Context(), in, "test")
	if !ok || len(sets) != 0 {
		t.Errorf("%v %v", sets, ok)
	}
	for _, r := range f.runs {
		if strings.Contains(r, rustscopeScript) {
			t.Errorf("a prose-only pull asked the script: %s", r)
		}
	}
}

// removeScript takes the scope script out of the tree.
func removeScript(in Input) error {
	return os.Remove(filepath.Join(in.Root, "tools", "scope", "scope.py"))
}
