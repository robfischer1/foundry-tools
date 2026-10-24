package devpythontest

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	pythonpytest "dagger/foundry-tools/atoms/python/pytest"
	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

type script struct {
	calls []atoms.Cmd
	exit  int
	out   string
}

func (s *script) exec(_ context.Context, c atoms.Cmd) (string, int) {
	s.calls = append(s.calls, c)
	if c.Name == "uv" && slices.Contains(c.Args, "pytest") {
		return s.out, s.exit
	}
	return "", 0
}

// tree is the input over a temp root holding the root entries given.
func tree(t *testing.T, s *script, entries []string, args ...string) atomkit.In {
	t.Helper()
	root := t.TempDir()
	for _, e := range entries {
		if err := os.WriteFile(filepath.Join(root, e), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return atomkit.In{Root: root, Args: args, Files: []string{"tests/test_a.py"}, Exec: s.exec}
}

// The row the TOML carried, but the surface is the gate twin's: a tree with
// .py and no pyproject.toml (paneless' tests/) is this verb's too.
func TestTheDeclarationIsTheDevVerbOfPytest(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	twin := pythonpytest.Def
	if Def.ID != "dev:python-test" || Def.Stage != "dev" || Def.Lane != checks.LanePython || Def.Group != checks.GroupLanguage ||
		Def.Toolchain != twin.Toolchain || Def.Surface != twin.Surface || !slices.Equal(Def.Needs, twin.Needs) ||
		Def.WritesTree || Def.Timeout != 30*time.Minute {
		t.Errorf("%+v", Def)
	}
}

// DEVSCOPE: a project runs against its extras; a tree with no pyproject.toml
// fetches pytest at the gate's pin (python:pytest's no-project form), both on
// the image's own interpreter.
func TestPytestRunsAProjectAndATreeThatIsNotOne(t *testing.T) {
	for name, tc := range map[string]struct {
		entries []string
		want    []string
	}{
		"project":    {[]string{"pyproject.toml"}, []string{"run", "--python", "python3", "--all-extras", "pytest", "-q"}},
		"no project": {nil, []string{"run", "--python", "python3", "--no-project", "--with", "pytest==" + checks.PytestVersion, "pytest", "-q"}},
	} {
		s := &script{}
		in := tree(t, s, tc.entries)
		v := check(context.Background(), in)
		if v.Atom != "dev:python-test" || v.State != int(checks.StatePass) {
			t.Errorf("%s: %+v", name, v)
		}
		c := s.calls[len(s.calls)-1]
		if c.Name != "uv" || c.Dir != in.Root || !slices.Equal(c.Args, tc.want) {
			t.Errorf("%s: %s %v, want %v", name, c.Name, c.Args, tc.want)
		}
		if !slices.Contains(c.Env, "UV_PROJECT_ENVIRONMENT=/tmp/dev-python-test/venv") ||
			!slices.Contains(c.Env, "PYTEST_ADDOPTS=-o cache_dir=/tmp/dev-python-test/pytest-cache") ||
			!slices.Contains(c.Env, "PYTHONDONTWRITEBYTECODE=1") {
			t.Errorf("%s: env %v keeps nothing off the read-only /src", name, c.Env)
		}
	}
}

// A caller's verbosity replaces -q; everything else is appended after it.
func TestTheCallersArgumentsComposeAsTheManifestDid(t *testing.T) {
	for _, tc := range []struct {
		args []string
		tail []string
	}{
		{[]string{"tests/test_a.py"}, []string{"-q", "tests/test_a.py"}},
		{[]string{"-k", "canon", "-x"}, []string{"-q", "-k", "canon", "-x"}},
		{[]string{"-v"}, []string{"-v"}},
		{[]string{"-vv", "tests/"}, []string{"-vv", "tests/"}},
		{[]string{"--quiet"}, []string{"--quiet"}},
	} {
		s := &script{}
		check(context.Background(), tree(t, s, []string{"pyproject.toml"}, tc.args...))
		got := s.calls[len(s.calls)-1].Args
		if want := append([]string{"run", "--python", "python3", "--all-extras", "pytest"}, tc.tail...); !slices.Equal(got, want) {
			t.Errorf("%v: %v, want %v", tc.args, got, want)
		}
	}
}

// A failing suite is a finding with its output; a uv killed by a signal never ran.
func TestAFailingSuiteIsAFinding(t *testing.T) {
	s := &script{exit: 1, out: "FAILED tests/test_a.py::test_x"}
	v := check(context.Background(), tree(t, s, nil))
	if v.State != int(checks.StateFindings) || !strings.Contains(v.Reason, "test_x") {
		t.Errorf("%+v", v)
	}
	s = &script{exit: 137, out: "Killed"}
	if v = check(context.Background(), tree(t, s, nil)); v.State != int(checks.StateCannotRun) {
		t.Errorf("%+v", v)
	}
}

// A root that cannot be read cannot be told a project from not.
func TestAnUnreadableRootCannotRun(t *testing.T) {
	s := &script{}
	in := tree(t, s, nil)
	in.Root = filepath.Join(in.Root, "missing")
	if v := check(context.Background(), in); v.State != int(checks.StateCannotRun) || len(s.calls) != 0 {
		t.Errorf("%+v calls %v", v, s.calls)
	}
}
