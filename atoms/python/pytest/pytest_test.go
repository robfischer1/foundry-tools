package pythonpytest

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

// tree answers the input over a temp root holding the entries given, with the
// files as its population.
func tree(t *testing.T, s *script, entries []string, files ...string) atomkit.In {
	t.Helper()
	root := t.TempDir()
	for _, e := range entries {
		if err := os.WriteFile(filepath.Join(root, e), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return atomkit.In{Root: root, Files: files, Exec: s.exec}
}

func TestTheDeclarationValidatesAndKeepsTheRowItReplaces(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LanePython || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "python" || Def.NotOnForks != "" || Def.NeedsManifest || !Def.WritesTree || Def.Surface != "any:**/*.py" ||
		Def.Desc != "pytest passes, and there is something for it to pass." ||
		!slices.Equal(Def.Needs, []string{"tool:git", "tool:uv", "tool:jq", "tool:curl"}) || Def.Timeout != 30*time.Minute {
		t.Errorf("%+v", Def)
	}
}

func TestPytestRunsAProjectAgainstItsExtras(t *testing.T) {
	s := &script{}
	in := tree(t, s, []string{"pyproject.toml"}, "tests/test_x.py", "src/x.py")
	if v := check(context.Background(), in); v.State != 0 || v.Reason != "python:pytest: PASS" {
		t.Fatalf("%+v", v)
	}
	c := s.calls[1]
	if c.Name != "uv" || c.Dir != in.Root || c.Both || !slices.Equal(c.Args, []string{"run", "--all-extras", "pytest", "-q"}) {
		t.Errorf("%+v", c)
	}
	if p := s.calls[0]; p.Name != "uv" || !slices.Equal(p.Args, []string{"--version"}) {
		t.Errorf("the probe: %+v", p)
	}
}

// uv run resolves a project or it fails, so a tree that is not one fetches
// pytest at its pin.
func TestPytestRunsInATreeThatIsNotAProject(t *testing.T) {
	s := &script{}
	in := tree(t, s, nil, "hooks/test_hook.py")
	if v := check(context.Background(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	want := []string{"run", "--no-project", "--with", "pytest==" + checks.PytestVersion, "pytest", "-q"}
	if got := s.calls[1].Args; !slices.Equal(got, want) || slices.Contains(got, "--all-extras") {
		t.Errorf("%v", got)
	}
}

// NOTHING IS BUILT WITHOUT TESTS: the emptiness is decided off the tree, before
// pytest is asked, and it is a finding.
func TestPytestFindsTestsByEitherNamingConventionAndRefusesNone(t *testing.T) {
	for _, f := range []string{"pkg/test_a.py", "pkg/a_test.py"} {
		s := &script{}
		in := tree(t, s, []string{"pyproject.toml"}, f)
		if v := check(context.Background(), in); v.State != 0 || len(s.calls) != 2 {
			t.Errorf("%s: %+v %v", f, v, s.calls)
		}
	}
	s := &script{}
	in := tree(t, s, []string{"pyproject.toml"}, "src/x.py", "tests/helper.go")
	v := check(context.Background(), in)
	if v.State != 1 || v.Result == "absent" || !strings.Contains(v.Reason, "python:pytest: FINDINGS - no test_*.py or *_test.py anywhere; nothing is built without tests") || len(s.calls) != 0 {
		t.Errorf("%+v %v", v, s.calls)
	}
}

func TestPytestsExitMapsAsTheShellBodyFoldedIt(t *testing.T) {
	run := func(code int, out string) checks.Verdict {
		s := &script{exits: []int{0, code}, out: []string{"", out}}
		return check(context.Background(), tree(t, s, []string{"pyproject.toml"}, "tests/test_x.py"))
	}
	// exit 5 is "no tests ran" and carries its own sentence
	if v := run(5, "no tests ran"); v.State != 1 || !strings.Contains(v.Reason, "python:pytest: FINDINGS - pytest collected no tests (exit 5); nothing is built without tests") {
		t.Errorf("%+v", v)
	}
	if v := run(1, "1 failed, 2 passed"); v.State != 1 || !strings.Contains(v.Reason, "1 failed, 2 passed") {
		t.Errorf("%+v", v)
	}
	// every other non-zero code is also a finding
	for _, code := range []int{2, 3, 4, 192} {
		if v := run(code, "x"); v.State != 1 {
			t.Errorf("exit %d: %+v", code, v)
		}
	}
	// AN OOM KILL IS NOT IN THAT FOLD
	if v := run(137, "Killed"); v.State != 2 || !strings.Contains(v.Reason, "the atom never ran") || !strings.Contains(v.Reason, "exit code: 137") {
		t.Errorf("%+v", v)
	}
	if v := run(-1, "uv: signal: killed"); v.State != 2 || !strings.Contains(v.Reason, "the atom never ran") {
		t.Errorf("%+v", v)
	}
}

func TestPytestCannotRunWithoutItsToolOrItsTree(t *testing.T) {
	s := &script{exits: []int{127}, out: []string{"uv: not found"}}
	in := tree(t, s, []string{"pyproject.toml"}, "tests/test_x.py")
	if v := check(context.Background(), in); v.State != 2 || !strings.Contains(v.Reason, "the atom never ran") || len(s.calls) != 1 {
		t.Errorf("%+v", v)
	}

	s = &script{}
	in = tree(t, s, []string{"pyproject.toml"})
	in.FilesErr = errors.New("git went away")
	if v := check(context.Background(), in); v.State != 2 || !strings.Contains(v.Reason, "python:pytest: CANNOT RUN - could not enumerate the repository: git went away") {
		t.Errorf("%+v", v)
	}
	in.Root = filepath.Join(in.Root, "absent")
	if v := check(context.Background(), in); v.State != 2 || !strings.Contains(v.Reason, "python:pytest: CANNOT RUN - could not read the repository root") {
		t.Errorf("%+v", v)
	}
}
