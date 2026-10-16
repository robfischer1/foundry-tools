package atomtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// fakeTB records what a helper reports instead of failing the test that calls it,
// so the helpers' failure branches run. A Fatal ends the goroutine it ran in.
type fakeTB struct {
	testing.TB
	errs []string
}

func (f *fakeTB) Helper() {}
func (f *fakeTB) Errorf(format string, a ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, a...))
}
func (f *fakeTB) Fatalf(format string, a ...any) { f.Errorf(format, a...); runtime.Goexit() }
func (f *fakeTB) Fatal(a ...any)                 { f.Errorf("%s", fmt.Sprint(a...)); runtime.Goexit() }

// try runs fn against a fakeTB in a goroutine of its own and answers what it reported.
func try(t *testing.T, fn func(tb testing.TB)) []string {
	t.Helper()
	f := &fakeTB{TB: t}
	done := make(chan struct{})
	go func() { defer close(done); fn(f) }()
	<-done
	return f.errs
}

func wantErr(t *testing.T, errs []string, needle string) {
	t.Helper()
	if !strings.Contains(strings.Join(errs, "\n"), needle) {
		t.Errorf("reported %q, want one naming %q", errs, needle)
	}
}

func TestRepositoriesAndFiles(t *testing.T) {
	dir := NewRepo(t)
	Put(t, dir, "a/b.txt", "x")
	if sha := CommitAll(t, dir, "one"); len(sha) != 40 {
		t.Errorf("sha %q", sha)
	}
	if got := ListTree(t, dir); len(got) != 1 || got[0] != "a/b.txt" {
		t.Errorf("tree %v", got)
	}
	wantErr(t, try(t, func(tb testing.TB) { GitIn(tb, dir, "no-such-verb") }), "git no-such-verb")
	wantErr(t, try(t, func(tb testing.TB) { ListTree(tb, filepath.Join(dir, "gone")) }), "no such file")
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	wantErr(t, try(t, func(tb testing.TB) { Put(tb, blocker, "x", "y") }), "not a directory")
	wantErr(t, try(t, func(tb testing.TB) { Put(tb, dir, "a", "y") }), "is a directory")
	wantErr(t, try(t, func(tb testing.TB) { BrokenLink(tb, blocker, "x") }), "not a directory")
	BrokenLink(t, dir, "l/dangling")
	wantErr(t, try(t, func(tb testing.TB) { BrokenLink(tb, dir, "l/dangling") }), "file exists")
}

func TestNewRepoNeedsGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	wantErr(t, try(t, func(tb testing.TB) { NewRepo(tb) }), "need git on PATH")
}

func TestTreesAndVerdicts(t *testing.T) {
	in := TreeIn(t, map[string]string{"b": "2", "a": "1"})
	if len(in.Files) != 2 || in.Committable[0] != "a" || in.Now.IsZero() {
		t.Errorf("%+v", in)
	}
	if MissingRoot(t).Root == "" || StateOf(2) != checks.StateCannotRun {
		t.Error("missing root or state")
	}
	v := checks.Verdict{Atom: "x:y", State: 0, Result: Pass, Reason: "x:y: PASS", Logs: []string{"hello"}}
	Expect(t, v, StateOf(0), Pass, "hello")
	errs := try(t, func(tb testing.TB) { Expect(tb, v, StateOf(1), Findings, "absent word") })
	wantErr(t, errs, "state 0")
	wantErr(t, errs, `lack "absent word"`)
	if StarData("a") != `{"verb_prefix":"a"}` || DiesAt(t, map[string]string{"x": "y"}) == "" {
		t.Error("roster or dies")
	}
}

func TestTheDeclarationCheck(t *testing.T) {
	ok := atomkit.Def{
		ID: "ops:x", Stage: checks.StagePrecommit, Lane: checks.LaneAny, Group: checks.GroupLanguage,
		Surface: "any:**", Toolchain: "none", Check: func(context.Context, atomkit.In) checks.Verdict { return checks.Verdict{} },
	}
	DeclaresItsRow(t, ok, checks.StagePrecommit, "none")
	wantErr(t, try(t, func(tb testing.TB) { DeclaresItsRow(tb, ok, checks.StagePrepush, "none") }), "ops:x")
	bad := ok
	bad.Check = nil
	wantErr(t, try(t, func(tb testing.TB) { DeclaresItsRow(tb, bad, checks.StagePrecommit, "none") }), "check is nil")
}

func TestTheToolFake(t *testing.T) {
	in, f := ToolTree(t, map[string]string{"a": "x"}, nil)
	if out, code := in.Exec(context.Background(), atoms.Cmd{Name: "git"}); out != "" || code != 0 {
		t.Errorf("%q %d", out, code)
	}
	if f.Call("git").Name != "git" || strings.Join(f.Ran(), ",") != "git" || f.LastCall(t).Name != "git" {
		t.Errorf("%v", f.Ran())
	}
	f.Answer = func(atoms.Cmd) (string, int) { return "said", 3 }
	if out, code := f.Exec(context.Background(), atoms.Cmd{Name: "git"}); out != "said" || code != 3 {
		t.Errorf("%q %d", out, code)
	}
	if Flagged(atoms.Cmd{Args: []string{"-a", "b"}}) != "-a b" {
		t.Error("flagged")
	}
	wantErr(t, try(t, func(tb testing.TB) {
		g := &ToolFake{T: tb}
		g.Exec(context.Background(), atoms.Cmd{Name: "no-such-program"})
		g.Call("no-such-program2")
	}), "no layer of the tools container carries it")
	wantErr(t, try(t, func(tb testing.TB) { (&ToolFake{T: tb}).LastCall(tb) }), "no program was exec'd")
}

func TestPyTree(t *testing.T) {
	in, _ := PyTree(t, map[string]string{"a": "x"}, nil)
	if out, code := in.Exec(context.Background(), atoms.Cmd{Name: "python3", Args: []string{"--version"}}); code != 0 || !strings.HasPrefix(out, "Python") {
		t.Errorf("%q %d", out, code)
	}
	if _, code := in.Exec(context.Background(), atoms.Cmd{Name: "python3", Args: []string{"x"}}); code != 0 {
		t.Error("nil script answers 0")
	}
	in, _ = PyTree(t, map[string]string{"a": "x"}, func(atoms.Cmd) (string, int) { return "s", 4 })
	if out, code := in.Exec(context.Background(), atoms.Cmd{Name: "python3", Args: []string{"x"}}); out != "s" || code != 4 {
		t.Errorf("%q %d", out, code)
	}
}

func TestComposedTree(t *testing.T) {
	in := ComposedTree(t, nil, nil)
	if in.Dies == "" || len(in.Committable) == 0 {
		t.Errorf("%+v", in)
	}
	in = ComposedTree(t, func(m map[string][]byte) { m["x.orbit.toml"] = []byte("y") }, map[string]string{"extra": "e"})
	if len(in.Committable) < 3 {
		t.Errorf("%v", in.Committable)
	}
}

// The suite holds a checker atom to its shape: a real one passes, a check that
// answers pass for everything is reported.
func TestTheCheckerSuite(t *testing.T) {
	good := func(ctx context.Context, in atoms.Input) checks.Verdict {
		row := checks.AtomDef{ID: "ops:x", Stage: checks.StagePrecommit, Lane: checks.LaneAny}
		return atomlib.RunChecker(ctx, row, in, atomlib.Checker{Phase: "x", Tool: "chk", Python: "python3"})
	}
	CheckerSuite(t, good, Checker{ID: "ops:x", Tool: "chk", Python: "python3"})
	noisy := func(ctx context.Context, in atoms.Input) checks.Verdict {
		in.Run(ctx, atoms.Cmd{Name: "python3", Args: []string{"a"}, Dir: "/elsewhere"})
		in.Run(ctx, atoms.Cmd{Name: "python3", Args: []string{"b"}})
		return checks.Verdict{}
	}
	errs := try(t, func(tb testing.TB) { CheckerSuite(tb, noisy, Checker{ID: "ops:x", Tool: "chk", Python: "python3"}) })
	for _, want := range []string{"state 0", "the checker ran as", "the first call was", "may write bytecode", "ran on an unprovisioned", "an absent atom touched"} {
		wantErr(t, errs, want)
	}
}
