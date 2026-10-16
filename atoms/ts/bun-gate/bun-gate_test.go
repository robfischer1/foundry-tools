package tsbungate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/rulesets"
)

type script struct {
	calls []atoms.Cmd
	exits []int
	out   []string
}

func (s *script) exec(_ context.Context, c atoms.Cmd) (string, int) {
	s.calls = append(s.calls, c)
	i := len(s.calls) - 1
	if i >= len(s.exits) {
		return "", 0
	}
	return s.out[i], s.exits[i]
}

// argvs are the programs run, as "bun install --frozen-lockfile" strings.
func (s *script) argvs() []string {
	var out []string
	for _, c := range s.calls {
		out = append(out, strings.Join(append([]string{c.Name}, c.Args...), " "))
	}
	return out
}

func tree(s *script, files ...string) atomkit.In {
	return atomkit.In{Root: "", Files: files, Exec: s.exec}
}

// withRoot gives the input a real root for the config to be written into.
func withRoot(t *testing.T, in atomkit.In) atomkit.In {
	t.Helper()
	in.Root = t.TempDir()
	return in
}

func TestTheDeclarationValidatesAndKeepsTheRowItReplaces(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneTS || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "bun" || Def.NotOnForks != "" || Def.NeedsManifest || Def.Surface != "root:package.json" || !Def.WritesTree ||
		Def.Desc != "bun run gate passes against a frozen lockfile under the fleet's eslint config, and the tree carries tests for it to run." ||
		Def.Timeout != 30*time.Minute {
		t.Errorf("%+v", Def)
	}
}

// THE FLEET'S CONFIG IS WRITTEN OVER THE REPOSITORY'S, at the root: eslint
// resolves its plugins relative to the config file.
func TestTheBunGateLaysTheFleetsEslintConfigInstallsFrozenAndRunsTheGate(t *testing.T) {
	s := &script{}
	in := withRoot(t, tree(s, "package.json", "src/index.test.ts"))
	if err := os.WriteFile(filepath.Join(in.Root, eslintConfig), []byte("the repo's own"), 0o644); err != nil {
		t.Fatal(err)
	}
	if v := check(context.Background(), in); v.State != 0 || v.Reason != "ts:bun-gate: PASS" {
		t.Fatalf("%+v", v)
	}
	if got, err := os.ReadFile(filepath.Join(in.Root, eslintConfig)); err != nil || string(got) != rulesets.ESLint {
		t.Errorf("the config laid in the tree is not the embedded ruleset: %v", err)
	}
	if want := []string{"bun install --frozen-lockfile", "bun run gate"}; !slices.Equal(s.argvs(), want) {
		t.Errorf("%v", s.argvs())
	}
	for _, c := range s.calls {
		if c.Dir != in.Root {
			t.Errorf("%s ran in %q", c.Name, c.Dir)
		}
	}
}

// A frozen lockfile that will not install is a fact about the pull's
// reproducibility, and it is asked before the test files are counted.
func TestAFrozenInstallThatFailsIsACouldNotRunBeforeAnythingIsGraded(t *testing.T) {
	s := &script{exits: []int{1}, out: []string{"error: lockfile had changes, but lockfile is frozen"}}
	in := withRoot(t, tree(s))
	v := check(context.Background(), in)
	if v.State != 2 || !strings.Contains(v.Reason, "ts:bun-gate: CANNOT RUN - frozen lockfile install failed") || !strings.Contains(v.Reason, "lockfile is frozen") {
		t.Errorf("%+v", v)
	}
	if len(s.calls) != 1 {
		t.Errorf("a tree that will not install must not run the gate: %v", s.argvs())
	}

	s = &script{exits: []int{137}, out: []string{"Killed"}}
	if v := check(context.Background(), withRoot(t, tree(s))); v.State != 2 || !strings.Contains(v.Reason, "the atom never ran") {
		t.Errorf("%+v", v)
	}
}

// NO TESTS IS A FINDING, and the presence check is the atom's own.
func TestATreeWithNoTestFileIsAFindingAndNeverRunsTheGate(t *testing.T) {
	s := &script{}
	in := withRoot(t, tree(s, "package.json", "src/index.ts", "testing/why.md"))
	v := check(context.Background(), in)
	if v.State != 1 || !strings.Contains(v.Reason, "no test file in the tree") || !strings.Contains(v.Reason, "nothing is built without tests") {
		t.Errorf("%+v", v)
	}
	if len(s.calls) != 1 {
		t.Errorf("only the install ran: %v", s.argvs())
	}

	// one test file anywhere is enough, found at the root as surely as deep
	for _, f := range []string{"a.test.ts", "src/deep/b.spec.tsx", "src/my_test_helpers.js", "c_spec_x.jsx"} {
		s = &script{}
		if v := check(context.Background(), withRoot(t, tree(s, "package.json", f))); v.State != 0 {
			t.Errorf("%s: %+v", f, v)
		}
	}
}

func TestAPopulationThatWillNotEnumerateIsACouldNotRun(t *testing.T) {
	s := &script{}
	in := withRoot(t, tree(s, "package.json"))
	in.FilesErr = errors.New("the filter would not evaluate")
	v := check(context.Background(), in)
	if v.State != 2 || !strings.Contains(v.Reason, "could not be enumerated") || !strings.Contains(v.Reason, "unknown rather than empty") {
		t.Errorf("%+v", v)
	}
}

// The script's exit is its failing tool's: tsc's 2 is a finding.
func TestTheGateScriptsExitIsReadAsItsFailingToolSpeaksIt(t *testing.T) {
	for _, tc := range []struct {
		code  int
		out   string
		state int
		has   string
	}{
		{1, "src/index.ts:1:7 - error TS2322", 1, "TS2322"},
		{2, "error TS2769", 1, "TS2769"},
		{127, "prettier: command not found", 2, "prettier: command not found"},
		{137, "Killed", 2, "the atom never ran"},
		{-1, "bun: executable file not found", 2, "the atom never ran"},
	} {
		s := &script{exits: []int{0, tc.code}, out: []string{"", tc.out}}
		v := check(context.Background(), withRoot(t, tree(s, "a.test.ts")))
		if v.State != tc.state || !strings.Contains(v.Reason, tc.has) {
			t.Errorf("exit %d: %+v", tc.code, v)
		}
	}
}

func TestAConfigThatCannotBeLaidIsACouldNotRun(t *testing.T) {
	s := &script{}
	in := tree(s, "a.test.ts")
	in.Root = filepath.Join(t.TempDir(), "absent")
	v := check(context.Background(), in)
	if v.State != 2 || !strings.Contains(v.Reason, "ts:bun-gate: CANNOT RUN - the fleet's eslint config could not be laid") || len(s.calls) != 0 {
		t.Errorf("%+v %v", v, s.calls)
	}
}
