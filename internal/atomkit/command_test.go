package atomkit

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// call is one exec the fake saw.
type call struct {
	argv []string
	dir  string
}

// fakeExec answers each exec from reply (by the argv joined by a space) and
// records it; an argv with no reply exits 0 with no output.
type fakeExec struct {
	calls []call
	reply func(argv []string, dir string) ExecResult
}

func (f *fakeExec) exec(_ context.Context, argv []string, dir string) ExecResult {
	f.calls = append(f.calls, call{argv, dir})
	if f.reply == nil {
		return ExecResult{}
	}
	return f.reply(argv, dir)
}

func vetCommand(f *fakeExec, m Manifest) Command {
	if m.Argv == nil {
		m.Argv = []string{"go", "vet", "./..."}
	}
	if m.Judge == "" {
		m.Judge = "exit"
	}
	if m.Toolchain == "" {
		m.Toolchain = "go"
	}
	return Command{ID: "go:vet", Stage: "precommit", Lane: "go", Manifest: m, Facts: Tree{Version: 1, Root: "/nowhere", Files: []string{"go.mod"}}, Exec: f.exec}
}

func TestUT_324_NoModuleManifestIsAbsent(t *testing.T) {
	f := &fakeExec{}
	c := vetCommand(f, Manifest{PerModule: true})
	c.Facts.Files = []string{"README.md"}
	v := RunCommand(context.Background(), c)
	if v.State != 0 || v.Result != "absent" || v.Reason != "no go.mod in the tree" || v.Logs == nil || len(f.calls) != 0 {
		t.Fatalf("%+v, %d calls", v, len(f.calls))
	}
	c.Manifest.Toolchain = "none"
	if v := RunCommand(context.Background(), c); v.Reason != "no module manifest in the tree" {
		t.Fatalf("%q", v.Reason)
	}
}

func TestUT_325_PreflightFailureIsCouldNotRun(t *testing.T) {
	f := &fakeExec{reply: func(argv []string, _ string) ExecResult {
		if argv[0] == "st-fail" {
			return ExecResult{Code: 1, Stderr: "retrying\nproxy down\n"}
		}
		return ExecResult{}
	}}
	c := vetCommand(f, Manifest{Argv: []string{"true"}})
	c.Preflight = []string{"st-fail"}
	v := RunCommand(context.Background(), c)
	if v.State != 2 || v.Result != "cannot-run" || v.Reason != "go:vet: CANNOT RUN - preflight `st-fail` exited 1: proxy down" || len(f.calls) != 1 {
		t.Fatalf("%+v, %d calls", v, len(f.calls))
	}
}

func TestUT_700_NoPreflightRunsTheArgvAlone(t *testing.T) {
	f := &fakeExec{}
	RunCommand(context.Background(), vetCommand(f, Manifest{}))
	if len(f.calls) != 1 || f.calls[0].argv[0] != "go" || f.calls[0].dir != "/src" {
		t.Fatalf("%+v", f.calls)
	}
}

func TestUT_701_PreflightRunsInEachModuleDirBeforeItsArgv(t *testing.T) {
	f := &fakeExec{}
	c := vetCommand(f, Manifest{PerModule: true})
	c.Facts.Files = []string{"go.mod", "tools/go.mod"}
	c.Preflight = []string{"go", "mod", "download"}
	RunCommand(context.Background(), c)
	want := []call{
		{[]string{"go", "mod", "download"}, "/src"}, {[]string{"go", "vet", "./..."}, "/src"},
		{[]string{"go", "mod", "download"}, "/src/tools"}, {[]string{"go", "vet", "./..."}, "/src/tools"},
	}
	if len(f.calls) != 4 {
		t.Fatalf("%+v", f.calls)
	}
	for i, w := range want {
		if !slices.Equal(f.calls[i].argv, w.argv) || f.calls[i].dir != w.dir {
			t.Errorf("call %d = %+v, want %+v", i, f.calls[i], w)
		}
	}
}

func TestUT_326_ArgsAppend(t *testing.T) {
	f := &fakeExec{}
	c := vetCommand(f, Manifest{Argv: []string{"go", "test", "./..."}, Args: "append"})
	c.Args = []string{"-run", "TestX"}
	RunCommand(context.Background(), c)
	if got := f.calls[0].argv; !slices.Equal(got, []string{"go", "test", "./...", "-run", "TestX"}) {
		t.Fatalf("%v", got)
	}
	if len(c.Manifest.Argv) != 3 {
		t.Fatal("append wrote into the manifest's argv")
	}
	f.calls = nil
	c.Manifest.Args = "none"
	RunCommand(context.Background(), c)
	if got := f.calls[0].argv; len(got) != 3 {
		t.Fatalf("args = none still appended: %v", got)
	}
}

func TestUT_327_ACommandThatDidNotStart(t *testing.T) {
	f := &fakeExec{reply: func([]string, string) ExecResult {
		return ExecResult{Code: 127, Stderr: "nosuchtool not found", NotRun: true}
	}}
	v := RunCommand(context.Background(), vetCommand(f, Manifest{Argv: []string{"nosuchtool"}}))
	if v.State != 2 || v.Reason != "exit 127: nosuchtool not found" || v.Logs == nil {
		t.Fatalf("%+v", v)
	}
}

func TestUT_328_ExitJudge(t *testing.T) {
	for _, c := range []struct {
		code   int
		state  int
		result string
		reason string
	}{
		{0, 0, "pass", "go:vet: PASS"},
		{1, 1, "findings", "go:vet: FINDINGS (exit 1)"},
		{3, 2, "cannot-run", "go:vet: CANNOT RUN (exit 3) — a check that could not run is not a check that passed."},
	} {
		f := &fakeExec{reply: func([]string, string) ExecResult { return ExecResult{Code: c.code} }}
		v := RunCommand(context.Background(), vetCommand(f, Manifest{}))
		if v.State != c.state || v.Result != c.result || v.Reason != c.reason {
			t.Errorf("exit %d: %+v", c.code, v)
		}
	}
}

func TestUT_329_OutputJudge(t *testing.T) {
	for _, c := range []struct {
		out   ExecResult
		state int
		logs  []string
	}{
		{ExecResult{Stdout: "x\ny\n"}, 1, []string{"x", "y"}},
		{ExecResult{}, 0, []string{}},
		{ExecResult{Code: 1}, 2, []string{}},
	} {
		f := &fakeExec{reply: func([]string, string) ExecResult { return c.out }}
		v := RunCommand(context.Background(), vetCommand(f, Manifest{Judge: "output"}))
		if v.State != c.state || !slices.Equal(v.Logs, c.logs) {
			t.Errorf("%+v: %+v", c.out, v)
		}
	}
}

func TestUT_330_PerModuleFoldsToTheWorstAndPrefixesLogs(t *testing.T) {
	f := &fakeExec{reply: func(_ []string, dir string) ExecResult {
		if dir == "/src/tools" {
			return ExecResult{Code: 1, Stdout: "b\n", Stderr: "c\n"}
		}
		return ExecResult{Stdout: "a\n"}
	}}
	c := vetCommand(f, Manifest{PerModule: true})
	c.Facts.Files = []string{"go.mod", "tools/go.mod"}
	v := RunCommand(context.Background(), c)
	if v.State != 1 || !slices.Equal(v.Logs, []string{".: a", "tools: b", "tools: c"}) {
		t.Fatalf("%+v", v)
	}
}

func TestUT_331_OneDirHasUnprefixedLogs(t *testing.T) {
	f := &fakeExec{reply: func([]string, string) ExecResult { return ExecResult{Stdout: "o1\no2\n", Stderr: "e1\n"} }}
	if v := RunCommand(context.Background(), vetCommand(f, Manifest{})); !slices.Equal(v.Logs, []string{"o1", "o2", "e1"}) {
		t.Fatalf("%v", v.Logs)
	}
}

func TestUT_332_LogCap(t *testing.T) {
	big := strings.Repeat("y\n", 1<<20)
	f := &fakeExec{reply: func([]string, string) ExecResult { return ExecResult{Stdout: big} }}
	v := RunCommand(context.Background(), vetCommand(f, Manifest{}))
	if !v.Truncated || v.OriginalBytes != 2097152 {
		t.Fatalf("truncated %v, original %d", v.Truncated, v.OriginalBytes)
	}
	if want, _, _ := checks.CaptureLogs(big); !slices.Equal(v.Logs, want) {
		t.Fatal("logs are not checks.CaptureLogs of the output")
	}
}

func TestUT_333_StageAndLaneAreTheCommands(t *testing.T) {
	v := RunCommand(context.Background(), vetCommand(&fakeExec{}, Manifest{}))
	if v.Atom != "go:vet" || v.Stage != "precommit" || v.Lane != "go" || v.StartedAt != "" {
		t.Fatalf("%+v", v)
	}
}

func TestModuleRoots(t *testing.T) {
	root := t.TempDir()
	tf := func(files ...string) Tree { return Tree{Root: root, Files: files} }
	put(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/a\"]\n")
	put(t, root, "crates/a/Cargo.toml", "[package]\nname = \"a\"\n")
	for _, c := range []struct {
		row       string
		facts     Tree
		toolchain string
		want      []string
	}{
		{"UT-334", tf("a/b/go.mod", "go.mod", "tools/go.mod", "x.go"), "go", []string{"/src", "/src/a/b", "/src/tools"}},
		{"UT-335", tf("Cargo.toml", "crates/a/Cargo.toml"), "rust", []string{"/src"}},
		{"UT-337", tf("pyproject.toml", "svc/pyproject.toml"), "python", []string{"/src", "/src/svc"}},
		{"UT-337", tf("web/package.json"), "node", []string{"/src/web"}},
		{"UT-337", tf("web/package.json"), "bun", []string{"/src/web"}},
		{"UT-338", tf("go.mod"), "none", []string{}},
	} {
		if got := ModuleRoots(c.facts, c.toolchain); !slices.Equal(got, c.want) || got == nil {
			t.Errorf("%s %s: %v, want %v", c.row, c.toolchain, got, c.want)
		}
	}
	// UT-336: no [workspace] in the root Cargo.toml, so the crate is a root of its own.
	plain := t.TempDir()
	put(t, plain, "Cargo.toml", "[package]\nname = \"r\"\n")
	if got := ModuleRoots(Tree{Root: plain, Files: []string{"Cargo.toml", "crates/a/Cargo.toml"}}, "rust"); !slices.Equal(got, []string{"/src", "/src/crates/a"}) {
		t.Errorf("UT-336: %v", got)
	}
	// a Cargo.toml the tree lists but the disk lacks is no workspace
	if got := ModuleRoots(Tree{Root: filepath.Join(plain, "gone"), Files: []string{"Cargo.toml", "a/Cargo.toml"}}, "rust"); len(got) != 2 {
		t.Errorf("unreadable root: %v", got)
	}
}
