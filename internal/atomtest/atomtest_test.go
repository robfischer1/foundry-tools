package atomtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// rec is a TB that records what the helpers report, so the helpers can be held
// to reporting it.
type rec struct {
	t        *testing.T
	errs     []string
	fatals   []string
	cleanups []func()
}

type stop struct{}

func newRec(t *testing.T) *rec { return &rec{t: t} }

func (r *rec) Helper()                      {}
func (r *rec) TempDir() string              { return r.t.TempDir() }
func (r *rec) Cleanup(f func())             { r.cleanups = append(r.cleanups, f) }
func (r *rec) Errorf(f string, args ...any) { r.errs = append(r.errs, fmt.Sprintf(f, args...)) }
func (r *rec) Fatal(args ...any)            { r.fatals = append(r.fatals, fmt.Sprint(args...)); panic(stop{}) }
func (r *rec) clean() {
	for _, f := range r.cleanups {
		f()
	}
}
func (r *rec) quiet() {
	r.t.Helper()
	r.require(len(r.errs) == 0 && len(r.fatals) == 0, "reported %v %v", r.errs, r.fatals)
}
func (r *rec) require(ok bool, f string, a ...any) {
	r.t.Helper()
	if !ok {
		r.t.Errorf(f, a...)
	}
}

// run runs f, answering whether it stopped on a Fatal.
func (r *rec) run(f func()) (fatal bool) {
	defer func() {
		if x := recover(); x != nil {
			if _, ok := x.(stop); !ok {
				panic(x)
			}
			fatal = true
		}
	}()
	f()
	return false
}

func TestAssertReportsOnlyAFalseCondition(t *testing.T) {
	r := newRec(t)
	Assert(r, true, "never")
	r.quiet()
	Assert(r, false, "wrong %d", 7)
	r.require(len(r.errs) == 1 && r.errs[0] == "wrong 7", "%v", r.errs)
}

func TestPutAndTreeInLayATreeOnDisk(t *testing.T) {
	r := newRec(t)
	in := TreeIn(r, map[string]string{"b/x.txt": "bee", "a.txt": "ay"})
	r.quiet()
	if body, err := os.ReadFile(filepath.Join(in.Root, "b/x.txt")); err != nil || string(body) != "bee" {
		t.Errorf("%q %v", body, err)
	}
	if strings.Join(in.Files, ",") != "a.txt,b/x.txt" || strings.Join(in.Committable, ",") != "a.txt,b/x.txt" || in.Now.Year() != 2026 {
		t.Errorf("%+v", in)
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !r.run(func() { Put(r, blocker, "sub/x", "") }) {
		t.Error("a directory that cannot be made did not stop the test")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !r.run(func() { Put(r, dir, "x", "body") }) {
		t.Error("a file that cannot be written did not stop the test")
	}
	if len(r.fatals) != 2 {
		t.Errorf("%v", r.fatals)
	}
}

func TestMissingRootIsADirectoryThatIsNotThere(t *testing.T) {
	in := MissingRoot(newRec(t))
	if _, err := os.Stat(in.Root); err == nil {
		t.Errorf("%s is there", in.Root)
	}
}

func TestExpectHoldsAVerdictToItsStateResultAndWords(t *testing.T) {
	v := checks.Verdict{Atom: "x:y", State: 1, Result: "findings", Reason: "x:y: FINDINGS - bad", Logs: []string{"a log line"}}
	r := newRec(t)
	Expect(r, v, checks.StateFindings, Findings, "bad", "a log line")
	r.quiet()
	Expect(r, v, checks.StatePass, Pass, "bad", "missing words")
	r.require(len(r.errs) == 2 && strings.Contains(r.errs[0], "state 1 (findings), want 0 (pass)") && strings.Contains(r.errs[1], `lack "missing words"`), "%v", r.errs)
}

func TestFailingFSFailsToListOnlyTheNamedDirectory(t *testing.T) {
	dir := t.TempDir()
	Put(newRec(t), dir, "ok/f", "x")
	fsys := FailingFS{FS: os.DirFS(dir), Dir: "bad"}
	if _, err := fsys.ReadDir("bad"); err != ErrBoom {
		t.Errorf("%v", err)
	}
	if es, err := fsys.ReadDir("ok"); err != nil || len(es) != 1 {
		t.Errorf("%v %v", es, err)
	}
}

func TestTheDoorsAnswerAndRefuseAsTheirNamesSay(t *testing.T) {
	ctx := context.Background()
	r := newRec(t)
	door := DoorOf(r, map[string]string{"rob/x a.json": "{}"})
	if status, body, err := door.Get(ctx, "rob/x", "a.json"); err != nil || status != 200 || string(body) != "{}" {
		t.Errorf("%d %q %v", status, body, err)
	}
	if status, _, err := door.Get(ctx, "rob/x", "other.json"); err != nil || status != 404 {
		t.Errorf("%d %v", status, err)
	}
	breaking := DoorBreaking(r, map[string]string{"rob/x a.json": "{}", "rob/x b.json": "{}"}, "rob/x b.json")
	if _, _, err := breaking.Get(ctx, "rob/x", "b.json"); err == nil {
		t.Error("the dropped connection answered")
	}
	if status, _, err := breaking.Get(ctx, "rob/x", "a.json"); err != nil || status != 200 {
		t.Errorf("%d %v", status, err)
	}
	if _, _, err := DeadDoor(r).Get(ctx, "rob/x", "a.json"); err == nil {
		t.Error("a dead door answered")
	}
	r.clean()
	r.quiet()
}

func TestBrokenLinkIsAFileTheListingShowsAndNothingReads(t *testing.T) {
	r := newRec(t)
	root := t.TempDir()
	BrokenLink(r, root, "a/b.md")
	r.quiet()
	if _, err := os.ReadFile(filepath.Join(root, "a/b.md")); err == nil {
		t.Error("the link reads")
	}
	if !r.run(func() { BrokenLink(r, root, "a/b.md") }) {
		t.Error("a link laid twice did not stop the test")
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !r.run(func() { BrokenLink(r, blocker, "sub/x") }) {
		t.Error("a directory that cannot be made did not stop the test")
	}
}

func TestTheTreesCarryWhatTheirAtomsAskFor(t *testing.T) {
	stocks := StocksTree(map[string]string{"budget.toml": "x", "kits.toml": "over"})
	if stocks["kits.toml"] != "over" || stocks["budget.toml"] != "x" || stocks["retired-verbs.toml"] != Ledger || len(stocks) != 4 {
		t.Errorf("%v", stocks)
	}
	die := DieTree(map[string]string{"x": "y", "policy/.manifest": "over"})
	if die["policy/.manifest"] != "over" || die["x"] != "y" || die["fleet/repos/chaos.json"] == "" {
		t.Errorf("%v", die)
	}
}

func TestFakeRecordsAnswersAndRefusesAProgramNoLayerCarries(t *testing.T) {
	r := newRec(t)
	in, f := ToolTree(r, map[string]string{"a": "b"}, nil)
	if out, code := in.Exec(context.Background(), atoms.Cmd{Name: "opa", Args: []string{"version"}}); out != "" || code != 0 {
		t.Errorf("%q %d", out, code)
	}
	r.quiet()
	f.Answer = func(c atoms.Cmd) (string, int) { return "said " + c.Name, 3 }
	if out, code := in.Exec(context.Background(), atoms.Cmd{Name: "tar"}); out != "said tar" || code != 3 {
		t.Errorf("%q %d", out, code)
	}
	r.quiet()
	in.Exec(context.Background(), atoms.Cmd{Name: "uv"})
	r.require(len(r.errs) == 1 && strings.Contains(r.errs[0], `exec of "uv"`), "%v", r.errs)
	f.Free = []string{"uv"}
	in.Exec(context.Background(), atoms.Cmd{Name: "uv"})
	r.require(len(r.errs) == 1 && strings.Join(f.Ran(), ",") == "opa,tar,uv,uv", "%v %v", r.errs, f.Ran())
	r.require(f.Last().Name == "uv" && Flagged(atoms.Cmd{Args: []string{"a", "b"}}) == "a b", "last")
	empty := &Fake{T: r}
	if !r.run(func() { empty.Last() }) {
		t.Error("no call did not stop the test")
	}
}

func TestUVTreeAnswersTheProbeItselfAndTheRestFromTheScript(t *testing.T) {
	r := newRec(t)
	in, f := UVTree(r, map[string]string{"a": "b"}, nil)
	if out, code := in.Exec(context.Background(), atoms.Cmd{Name: "uv", Args: []string{"--version"}}); !strings.HasPrefix(out, "uv ") || code != 0 {
		t.Errorf("%q %d", out, code)
	}
	if out, code := in.Exec(context.Background(), atoms.Cmd{Name: "uv", Args: []string{"run"}}); out != "" || code != 0 {
		t.Errorf("a nil script answered %q %d", out, code)
	}
	in2, _ := UVTree(r, map[string]string{"a": "b"}, func(atoms.Cmd) (string, int) { return "scripted", 5 })
	if out, code := in2.Exec(context.Background(), atoms.Cmd{Name: "uv", Args: []string{"run"}}); out != "scripted" || code != 5 {
		t.Errorf("%q %d", out, code)
	}
	r.quiet()
	r.require(len(f.Calls) == 2, "%v", f.Ran())
}

func TestWantUVHoldsACallToTheShapeEveryPythonAtomRunsIn(t *testing.T) {
	good := dieskit.UV("/src", "tomli>=2.0", "x.py", "--flag")
	r := newRec(t)
	WantUV(r, good, "/src", "tomli>=2.0", "x.py", "--flag")
	WantUV(r, dieskit.UV("/src", "", "x.py"), "/src", "", "x.py")
	r.quiet()
	WantUV(r, good, "/elsewhere", "tomli>=2.0", "x.py", "--flag")
	WantUV(r, good, "/src", "", "x.py", "--flag")
	r.require(len(r.errs) == 2, "%v", r.errs)
}

func TestOpaScriptAnswersForOpaTarAndGit(t *testing.T) {
	s := OpaScript{Version: "Version: 9", VCode: 4, On: map[string]func(atoms.Cmd) (string, int){"test": Const0("suite", 2)}}
	for _, tc := range []struct {
		cmd  atoms.Cmd
		out  string
		code int
	}{
		{atoms.Cmd{Name: "git"}, "abc123", 0},
		{atoms.Cmd{Name: "tar"}, GoodData, 0},
		{atoms.Cmd{Name: "opa", Args: []string{"version"}}, "Version: 9", 4},
		{atoms.Cmd{Name: "opa", Args: []string{"test"}}, "suite", 2},
		{atoms.Cmd{Name: "opa", Args: []string{"build"}}, "", 0},
	} {
		if out, code := s.Answer(tc.cmd); out != tc.out || code != tc.code {
			t.Errorf("%s %v: %q %d", tc.cmd.Name, tc.cmd.Args, out, code)
		}
	}
	if !strings.Contains(PinnedOpa(), checks.OpaVersion) {
		t.Error(PinnedOpa())
	}
}

// CheckerSuite is held to the real helper it exists to hold atoms to.
func TestCheckerSuiteRunsAgainstTheSharedChecker(t *testing.T) {
	check := func(ctx context.Context, in atoms.Input) checks.Verdict {
		return dieskit.Checker(ctx, checks.AtomDef{ID: "dies:x", Stage: checks.StagePrecommit, Lane: checks.LaneAny}, in, dieskit.WithJSONSchema, "tools/check_x.py")
	}
	CheckerSuite(t, check, "tools/check_x.py", dieskit.WithJSONSchema, map[string]string{"extra.txt": "x"})
}
