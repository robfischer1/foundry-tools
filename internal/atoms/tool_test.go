package atoms

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// scripted is an Exec that records each Cmd and answers from a list, then with
// success.
type scripted struct {
	calls   []Cmd
	answers []struct {
		out  string
		code int
	}
}

func (s *scripted) then(out string, code int) *scripted {
	s.answers = append(s.answers, struct {
		out  string
		code int
	}{out, code})
	return s
}

func (s *scripted) exec(_ context.Context, c Cmd) (string, int) {
	s.calls = append(s.calls, c)
	if len(s.answers) == 0 {
		return "", 0
	}
	a := s.answers[0]
	s.answers = s.answers[1:]
	return a.out, a.code
}

var toolRow = checks.AtomDef{ID: "python:x", Stage: checks.StagePrecommit, Lane: checks.LanePython}

func TestMatchingIsTheDaggerGlob(t *testing.T) {
	files := []string{"a.py", "src/b.py", "src/pkg/c.py", "tests/test_d.py", "tests/deep/e.py", "src/notes.md", "mytests/f.py"}
	for _, tc := range []struct {
		pattern string
		want    []string
	}{
		{"**/*.py", []string{"a.py", "src/b.py", "src/pkg/c.py", "tests/test_d.py", "tests/deep/e.py", "mytests/f.py"}},
		{"tests/**/*.py", []string{"tests/test_d.py", "tests/deep/e.py"}},
		{"src/**/*.py", []string{"src/b.py", "src/pkg/c.py"}},
		{"*.py", []string{"a.py"}},
		{"**/*_test_*", nil},
		{"[", nil},
	} {
		if got := Matching(files, tc.pattern); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.pattern, got, tc.want)
		}
	}
	if got := Matching([]string{"x/y_test_z.ts", "y_spec_.js"}, "**/*_test_*"); !slices.Equal(got, []string{"x/y_test_z.ts"}) {
		t.Errorf("a test file below the root: %v", got)
	}
}

func TestTheRootIsListedAsTheEngineListedIt(t *testing.T) {
	in := treeIn(t, map[string]string{"pyproject.toml": "x", "src/a.py": "x"})
	got, err := RootEntries(in.Root)
	slices.Sort(got)
	if err != nil || !slices.Equal(got, []string{"pyproject.toml", "src/"}) {
		t.Errorf("got %v, %v", got, err)
	}
	if _, err := RootEntries(filepath.Join(in.Root, "absent")); err == nil {
		t.Error("a root that is not there lists")
	}
}

func TestAFileIsPresentOnlyWhenItIsARegularFileThatReads(t *testing.T) {
	in := treeIn(t, map[string]string{"pyproject.toml": "body", "src/a.py": "x"})
	if body, ok := ReadIfPresent(in.Root, "pyproject.toml"); !ok || body != "body" {
		t.Errorf("a file: %q %v", body, ok)
	}
	for _, rel := range []string{"src", "nope.toml"} {
		if body, ok := ReadIfPresent(in.Root, rel); ok || body != "" {
			t.Errorf("%s: %q %v", rel, body, ok)
		}
	}
}

func TestARulesetIsWrittenUnderAtomWorkOrADirectoryOfItsOwn(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work", "python__x")
	t.Setenv("ATOM_WORK", work)
	p, err := WriteRuleset("ruff.toml", "line-length = 1\n")
	if got, rerr := os.ReadFile(p); err != nil || p != filepath.Join(work, "ruff.toml") || rerr != nil || string(got) != "line-length = 1\n" {
		t.Errorf("%s %v %v", p, err, rerr)
	}

	t.Setenv("ATOM_WORK", "")
	t.Setenv("TMPDIR", t.TempDir())
	p, err = WriteRuleset("mypy.ini", "[mypy]\n")
	if got, rerr := os.ReadFile(p); err != nil || rerr != nil || string(got) != "[mypy]\n" || !strings.HasPrefix(p, os.Getenv("TMPDIR")) {
		t.Errorf("%s %v %v", p, err, rerr)
	}
}

func TestARulesetThatCannotBeWrittenIsAnError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATOM_WORK", filepath.Join(file, "under-a-file"))
	if _, err := WriteRuleset("a", "x"); err == nil {
		t.Error("a work directory below a file was made")
	}

	dir := t.TempDir()
	t.Setenv("ATOM_WORK", dir)
	if err := os.Mkdir(filepath.Join(dir, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteRuleset("taken", "x"); err == nil {
		t.Error("a file was written over a directory")
	}

	t.Setenv("ATOM_WORK", "")
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "absent"))
	if _, err := WriteRuleset("a", "x"); err == nil {
		t.Error("a temp directory was made where there is no temp root")
	}
}

func TestOnlyTheSignalRangeAndAProgramThatWillNotStartAreNotAnExit(t *testing.T) {
	for code, want := range map[int]bool{-1: false, 0: true, 1: true, 2: true, 127: true, 128: false, 137: false, 191: false, 192: true, 255: true} {
		if got := ToolRan(code); got != want {
			t.Errorf("exit %d: ToolRan %v, want %v", code, got, want)
		}
	}
}

func TestANeverRunNamesTheCommandAndTheTailOfWhatItSaid(t *testing.T) {
	var lines []string
	for i := range 30 {
		lines = append(lines, "line"+string(rune('A'+i%26))+string(rune('a'+i/26)))
	}
	v := NeverRan(toolRow, []string{"uv", "run", "pytest"}, 137, strings.Join(lines, "\n")+"\n")
	if v.State != 2 || !strings.Contains(v.Reason, "the atom never ran: exit code: 137") || !strings.Contains(v.Reason, "failed: uv run pytest") ||
		!strings.Contains(v.Reason, "lineDb") || strings.Contains(v.Reason, "lineAa\n") {
		t.Errorf("%+v", v)
	}
	v = NeverRan(toolRow, []string{"ruff"}, -1, "ruff: executable file not found in $PATH")
	if v.State != 2 || !strings.Contains(v.Reason, "the atom never ran: ruff: executable file not found") || strings.Contains(v.Reason, "failed:") {
		t.Errorf("a program that would not start: %+v", v)
	}
}

func TestAProbeThatFailsIsTheAtomNeverRunning(t *testing.T) {
	in := Input{Root: "/src"}
	s := (&scripted{}).then("ruff 0.16.3", 0)
	in.Exec = s.exec
	if v := Probe(context.Background(), in, toolRow, "ruff", "--version"); v != nil {
		t.Fatalf("a tool that answers is provisioned: %+v", v)
	}
	if c := s.calls[0]; c.Name != "ruff" || !slices.Equal(c.Args, []string{"--version"}) || c.Dir != "/src" || !c.Both {
		t.Errorf("%+v", c)
	}

	s = (&scripted{}).then("sh: ruff: not found", 127)
	in.Exec = s.exec
	if v := Probe(context.Background(), in, toolRow, "ruff", "--version"); v == nil || v.State != 2 || !strings.Contains(v.Reason, "failed: ruff --version") {
		t.Errorf("%+v", v)
	}
}

func TestAToolThatNeverRanIsReturnedAndOneThatExitedIsSettledOnItsExit(t *testing.T) {
	in := Input{Root: "/src"}
	s := (&scripted{}).then("boom", -1)
	in.Exec = s.exec
	if _, _, bad := RunTool(context.Background(), in, toolRow, Cmd{Name: "x"}); bad == nil || bad.State != 2 {
		t.Errorf("%+v", bad)
	}
	if v := SettleTool(context.Background(), in, toolRow, Cmd{Name: "x"}); v.State != 0 {
		t.Errorf("an exec that answers nothing and 0 passes: %+v", v)
	}

	s = (&scripted{}).then("x.py:1: F401", 1)
	in.Exec = s.exec
	v := SettleTool(context.Background(), in, toolRow, Cmd{Name: "ruff", Args: []string{"check"}})
	if v.State != 1 || !strings.Contains(v.Reason, "F401") || !s.calls[0].Both {
		t.Errorf("a finding carries both streams: %+v", v)
	}

	s = (&scripted{}).then("killed", 137)
	in.Exec = s.exec
	if v := SettleTool(context.Background(), in, toolRow, Cmd{Name: "ruff"}); v.State != 2 || !strings.Contains(v.Reason, "the atom never ran") {
		t.Errorf("the signal range: %+v", v)
	}
}

func TestTheInputsRunnerIsItsExecOrTheRealOne(t *testing.T) {
	s := &scripted{}
	if out, code := (Input{Exec: s.exec}).Runner()(context.Background(), Cmd{Name: "x"}); out != "" || code != 0 || len(s.calls) != 1 {
		t.Errorf("%q %d %v", out, code, s.calls)
	}
	if out, code := (Input{}).Runner()(context.Background(), Cmd{Name: "true"}); out != "" || code != 0 {
		t.Errorf("the real runner: %q %d", out, code)
	}
}
