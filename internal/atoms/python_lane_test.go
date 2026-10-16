package atoms

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

const testkitProject = "[project]\nname = \"x\"\ndependencies = [\n    \"forge-testkit>=1\",\n]\n"

func TestThePythonRootIsListedOrACouldNotRun(t *testing.T) {
	in := treeIn(t, map[string]string{"pyproject.toml": "x", "src/a.py": ""})
	got, stop := PythonRootEntries(toolRow, in)
	slices.Sort(got)
	if stop != nil || !slices.Equal(got, []string{"pyproject.toml", "src/"}) {
		t.Errorf("%v %+v", got, stop)
	}
	in.Root += "/absent"
	if _, stop := PythonRootEntries(toolRow, in); stop == nil || stop.State != 2 || !strings.Contains(stop.Reason, "python:x: CANNOT RUN - could not read the repository root") {
		t.Errorf("%+v", stop)
	}
}

func TestThePythonTreeIsThePopulationsPythonLessWhatIsVendored(t *testing.T) {
	in := Input{Files: []string{"b.py", "README.md", "a/c.py", "vendor/d.py", "_x/e.py"}}
	got, stop := PythonTree(toolRow, in)
	if stop != nil || !slices.Equal(got, []string{"a/c.py", "b.py"}) {
		t.Errorf("%v %+v", got, stop)
	}
	in.FilesErr = errors.New("git went away")
	if _, stop := PythonTree(toolRow, in); stop == nil || stop.State != 2 ||
		!strings.Contains(stop.Reason, "python:x: CANNOT RUN - could not enumerate the repository: git went away") {
		t.Errorf("%+v", stop)
	}
}

func TestUvRunsAProjectAgainstItsExtrasAndANonProjectWithTheToolFetched(t *testing.T) {
	c := UvCmd(Input{Root: "/src"}, []string{"pyproject.toml"}, "pytest", "-q")
	if c.Dir != "/src" || c.Name != "uv" || strings.Join(c.Args, " ") != "run --all-extras pytest -q" {
		t.Errorf("%+v", c)
	}
	c = UvCmd(Input{Root: "/src"}, []string{"README.md"}, "pytest", "-q")
	if c.Name != "uv" || strings.Join(c.Args, " ") != "run --no-project --with pytest=="+checks.PytestVersion+" pytest -q" {
		t.Errorf("%+v", c)
	}
}

func TestForgeTestkitLintsTheFilesItsPatternMatchesAsArguments(t *testing.T) {
	in := treeIn(t, map[string]string{"pyproject.toml": testkitProject, "tests/test_x.py": "", "src/x.py": ""})
	s := &scripted{}
	in.Exec = s.exec
	v := ForgeTestkitLint(context.Background(), in, toolRow, "assertion-free", "tests/**/*.py")
	if v.State != 0 || len(s.calls) != 2 {
		t.Fatalf("%+v %v", v, s.calls)
	}
	if c := s.calls[0]; c.Name != "uv" || !slices.Equal(c.Args, []string{"--version"}) {
		t.Errorf("the probe is the version: %+v", c)
	}
	c := s.calls[1]
	if c.Name != "uv" || strings.Join(c.Args, " ") != "run --extra dev forge-testkit-lint assertion-free tests/test_x.py" || c.Dir != in.Root || !c.Both {
		t.Errorf("%+v", c)
	}

	s = (&scripted{}).then("uv 0.12", 0).then("tests/test_x.py:1: no assertion in this test body", 1)
	in.Exec = s.exec
	if v := ForgeTestkitLint(context.Background(), in, toolRow, "assertion-free", "tests/**/*.py"); v.State != 1 || !strings.Contains(v.Reason, "no assertion in this test body") {
		t.Errorf("%+v", v)
	}

	s = (&scripted{}).then("", 127)
	in.Exec = s.exec
	if v := ForgeTestkitLint(context.Background(), in, toolRow, "assertion-free", "tests/**/*.py"); v.State != 2 || len(s.calls) != 1 {
		t.Errorf("a probe that fails stops the lint: %+v %v", v, s.calls)
	}
}

func TestForgeTestkitStandsDownWhereItHasNothingToRead(t *testing.T) {
	absent := func(v checks.Verdict, why string) {
		t.Helper()
		if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "python:x: ABSENT - "+why) {
			t.Errorf("%+v", v)
		}
	}
	// no manifest at all
	in := treeIn(t, map[string]string{"tests/test_x.py": ""})
	in.Exec = (&scripted{}).exec
	absent(ForgeTestkitLint(context.Background(), in, toolRow, "m", "tests/**/*.py"), "forge-testkit is not a dependency of this project")

	// A MENTION IS NOT AN ENTRY
	in = treeIn(t, map[string]string{"pyproject.toml": "[project]\n# forge-testkit lints tests\ndependencies = []\n", "tests/test_x.py": ""})
	absent(ForgeTestkitLint(context.Background(), in, toolRow, "m", "tests/**/*.py"), "forge-testkit is not a dependency")

	// declared, no file matches
	in = treeIn(t, map[string]string{"pyproject.toml": testkitProject, "src/x.py": ""})
	s := &scripted{}
	in.Exec = s.exec
	absent(ForgeTestkitLint(context.Background(), in, toolRow, "m", "tests/**/*.py"), "no files match tests/**/*.py")
	if len(s.calls) != 0 {
		t.Errorf("an empty population runs nothing: %v", s.calls)
	}

	// the enumeration is a could-not-run
	in.FilesErr = errors.New("git went away")
	if v := ForgeTestkitLint(context.Background(), in, toolRow, "m", "tests/**/*.py"); v.State != 2 || !strings.Contains(v.Reason, "could not enumerate the repository: git went away") {
		t.Errorf("%+v", v)
	}
}
