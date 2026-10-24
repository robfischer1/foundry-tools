package devpython

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

type script struct {
	calls []atoms.Cmd
	exits map[string]int
	outs  map[string]string
}

// tool is the type checker a call runs: the argument after the uv flags.
func tool(c atoms.Cmd) string {
	for _, t := range []string{"mypy", "pyright"} {
		if slices.Contains(c.Args, t) {
			return t
		}
	}
	return ""
}

func (s *script) exec(_ context.Context, c atoms.Cmd) (string, int) {
	s.calls = append(s.calls, c)
	return s.outs[tool(c)], s.exits[tool(c)]
}

// tree is the input over a temp root holding the files given (path -> body).
func tree(t *testing.T, s *script, files map[string]string, args ...string) atomkit.In {
	t.Helper()
	root := t.TempDir()
	for p, body := range files {
		if err := os.WriteFile(filepath.Join(root, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return atomkit.In{Root: root, Args: args, Files: []string{"src/a.py"}, Exec: s.exec}
}

// project is a pyproject.toml with no pyright configured.
var project = map[string]string{"pyproject.toml": "[project]\nname = \"x\"\n\n[tool.mypy]\nstrict = true\n"}

// row and cache are dev:python-typecheck's.
var (
	row   = checks.AtomDef{ID: "dev:python-typecheck", Stage: "dev", Lane: checks.LanePython}
	cache = "dev-python-typecheck"
)

// A tree with no pyproject.toml (the verb's surface keeps it out, but the run
// still answers) type checks in uv's no-project form, without pyright.
func TestATreeThatIsNotAProjectRunsMypyAlone(t *testing.T) {
	s := &script{}
	if v := Typecheck(context.Background(), tree(t, s, nil), row, cache); v.State != int(checks.StatePass) || len(s.calls) != 1 {
		t.Fatalf("%+v calls %v", v, s.calls)
	}
	if got := s.calls[0].Args; !slices.Equal(got, []string{"run", "--python", "python3", "--no-project", "--with", "mypy==" + checks.MypyVersion, "mypy", "src", "tests"}) {
		t.Errorf("%v", got)
	}
}

// mypy alone, as the manifest ran it, when the tree configures no pyright.
func TestMypyAloneWithoutPyright(t *testing.T) {
	s := &script{}
	in := tree(t, s, project)
	if v := Typecheck(context.Background(), in, row, cache); v.Atom != "dev:python-typecheck" || v.State != int(checks.StatePass) {
		t.Fatalf("%+v", v)
	}
	if len(s.calls) != 1 {
		t.Fatalf("calls %v", s.calls)
	}
	c := s.calls[0]
	if c.Name != "uv" || c.Dir != in.Root || !slices.Equal(c.Args, []string{"run", "--python", "python3", "--all-extras", "mypy", "src", "tests"}) {
		t.Errorf("%s %v", c.Name, c.Args)
	}
	for _, e := range []string{"MYPY_CACHE_DIR=/tmp/dev-python-typecheck/mypy-cache", "PYTHONDONTWRITEBYTECODE=1", "UV_PROJECT_ENVIRONMENT=/tmp/dev-python-typecheck/venv"} {
		if !slices.Contains(c.Env, e) {
			t.Errorf("env %v lacks %s", c.Env, e)
		}
	}
}

// pyright runs after mypy where the tree configures it: [tool.pyright] (or one
// of its sub-tables) in pyproject.toml, or a pyrightconfig.json.
func TestPyrightRunsWhereTheTreeConfiguresIt(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"tool.pyright":       {"pyproject.toml": "[project]\nname = \"x\"\n\n[tool.pyright]\nstrict = [\"src\"]\n"},
		"a sub-table only":   {"pyproject.toml": "[project]\nname = \"x\"\n\n[[tool.pyright.executionEnvironments]]\nroot = \"src\"\n"},
		"pyrightconfig.json": {"pyproject.toml": project["pyproject.toml"], "pyrightconfig.json": "{}"},
	} {
		s := &script{}
		if v := Typecheck(context.Background(), tree(t, s, files), row, cache); v.State != int(checks.StatePass) {
			t.Errorf("%s: %+v", name, v)
		}
		if len(s.calls) != 2 || tool(s.calls[0]) != "mypy" ||
			!slices.Equal(s.calls[1].Args, []string{"run", "--python", "python3", "--all-extras", "pyright"}) {
			t.Errorf("%s: calls %v", name, s.calls)
		}
	}
	s := &script{}
	Typecheck(context.Background(), tree(t, s, map[string]string{"pyproject.toml": "[project]\nname = \"x\"\n# [tool.pyright] is not configured here\n"}), row, cache)
	if len(s.calls) != 1 {
		t.Errorf("a comment is no configuration: %v", s.calls)
	}
}

// A caller's target replaces src and tests for mypy; pyright reads its own
// configuration and takes none of mypy's arguments.
func TestTheCallersArgumentsAreMypys(t *testing.T) {
	for _, tc := range []struct {
		args, want []string
	}{
		{[]string{"./src/pkg"}, []string{"./src/pkg"}},
		{[]string{"--strict"}, []string{"--strict", "src", "tests"}},
		{[]string{"-m", "pkg.mod"}, []string{"-m", "pkg.mod"}},
		{[]string{"--package=pkg"}, []string{"--package=pkg"}},
	} {
		s := &script{}
		Typecheck(context.Background(), tree(t, s, map[string]string{"pyproject.toml": "[tool.pyright]\n"}, tc.args...), row, cache)
		if got := s.calls[0].Args[5:]; !slices.Equal(got, tc.want) {
			t.Errorf("%v: mypy %v, want %v", tc.args, got, tc.want)
		}
		if got := s.calls[1].Args; got[len(got)-1] != "pyright" {
			t.Errorf("%v: pyright %v", tc.args, got)
		}
	}
}

// Either checker's finding is the verb's, with both outputs; the worse state wins.
func TestTheWorseOfTheTwoWins(t *testing.T) {
	files := map[string]string{"pyproject.toml": "[tool.pyright]\n"}
	for name, tc := range map[string]struct {
		exits map[string]int
		state checks.State
	}{
		"both clean":     {nil, checks.StatePass},
		"mypy finds":     {map[string]int{"mypy": 1}, checks.StateFindings},
		"pyright finds":  {map[string]int{"pyright": 1}, checks.StateFindings},
		"pyright can't":  {map[string]int{"mypy": 1, "pyright": 2}, checks.StateCannotRun},
		"mypy killed":    {map[string]int{"mypy": 137}, checks.StateCannotRun},
		"pyright killed": {map[string]int{"pyright": 137}, checks.StateCannotRun},
	} {
		s := &script{exits: tc.exits, outs: map[string]string{"mypy": "src/a.py:1: error: mypy says", "pyright": "src/a.py:1:1 - error: pyright says"}}
		v := Typecheck(context.Background(), tree(t, s, files), row, cache)
		if v.State != int(tc.state) {
			t.Errorf("%s: %+v", name, v)
		}
		if tc.state == checks.StateFindings && (!strings.Contains(v.Reason, "mypy says") || !strings.Contains(v.Reason, "pyright says")) {
			t.Errorf("%s: reason %q carries both outputs", name, v.Reason)
		}
	}
}

// A root, or a pyproject.toml that cannot be read (a dangling link), cannot run.
func TestAnUnreadableTreeCannotRun(t *testing.T) {
	s := &script{}
	in := tree(t, s, nil)
	in.Root = filepath.Join(in.Root, "missing")
	if v := Typecheck(context.Background(), in, row, cache); v.State != int(checks.StateCannotRun) || len(s.calls) != 0 {
		t.Errorf("root: %+v calls %v", v, s.calls)
	}
	s = &script{}
	in = tree(t, s, nil)
	if err := os.Symlink(filepath.Join(in.Root, "gone"), filepath.Join(in.Root, "pyproject.toml")); err != nil {
		t.Fatal(err)
	}
	if v := Typecheck(context.Background(), in, row, cache); v.State != int(checks.StateCannotRun) || len(s.calls) != 0 {
		t.Errorf("pyproject: %+v calls %v", v, s.calls)
	}
}
