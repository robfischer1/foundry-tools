package atomkit

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// The tests here swap Main's package-level seams, so none of them is parallel.

// captureStreams points Main's stdout and stderr at buffers for the test.
func captureStreams(t *testing.T) (out, errOut *bytes.Buffer) {
	t.Helper()
	out, errOut = &bytes.Buffer{}, &bytes.Buffer{}
	oldOut, oldErr := stdout, stderr
	stdout, stderr = out, errOut
	t.Cleanup(func() { stdout, stderr = oldOut, oldErr })
	return out, errOut
}

// treeFile writes TF-1 (or t) as tree@1 and points ATOM_TREE at it.
func treeFile(t *testing.T, tree Tree) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tree.json")
	if err := WriteFile(p, tree); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATOM_TREE", p)
}

// runAtom runs d over TF-1 and answers the exit code and the verdict it wrote.
func runAtom(t *testing.T, d Def) (int, checks.Verdict) {
	t.Helper()
	treeFile(t, TF1())
	out := filepath.Join(t.TempDir(), "out", "x.json")
	t.Setenv("ATOM_OUT", out)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	code := run(d, []string{"atom", filepath.Join(t.TempDir(), "src")})
	return code, readVerdict(t, out)
}

func readVerdict(t *testing.T, p string) checks.Verdict {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var v checks.Verdict
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func defWith(f func(context.Context, In) checks.Verdict) Def {
	return Def{ID: "st:x", Stage: "prepush", Lane: checks.LaneGo, Surface: "any:**", Toolchain: "none", Check: f}
}

func TestUT_449_DescribePrintsTheEntryAndExitsZero(t *testing.T) {
	out, errOut := captureStreams(t)
	d := goDef()
	if code := run(d, []string{"atom", "--describe"}); code != 0 || errOut.Len() != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	want, _ := Describe(d)
	if out.String() != string(want)+"\n" {
		t.Fatalf("stdout %q", out)
	}
}

func TestUT_450_DescribeOfAnInvalidDefExitsTwo(t *testing.T) {
	out, errOut := captureStreams(t)
	if code := run(Def{}, []string{"atom", "--describe"}); code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "id  is not <ns>:<name>") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestUT_451_TheCheckVerdictIsWrittenAndItsStateIsTheExit(t *testing.T) {
	d := defWith(func(context.Context, In) checks.Verdict {
		return checks.Verdict{State: 1, Result: "findings", Reason: "st:x: FINDINGS (exit 1)", Logs: []string{"l"}}
	})
	code, v := runAtom(t, d)
	if code != 1 || v.Atom != "st:x" || v.State != 1 || v.Result != "findings" || v.Reason != "st:x: FINDINGS (exit 1)" || v.Logs[0] != "l" {
		t.Fatalf("exit %d, %+v", code, v)
	}
}

func TestUT_452_APanicIsCouldNotRun(t *testing.T) {
	code, v := runAtom(t, defWith(func(context.Context, In) checks.Verdict { panic("boom") }))
	if code != 2 || v.State != 2 || v.Result != "cannot-run" || v.Reason != "panic: boom" || v.Atom != "st:x" {
		t.Fatalf("exit %d, %+v", code, v)
	}
}

func TestUT_453_ADeadlineIsCouldNotRun(t *testing.T) {
	d := defWith(func(ctx context.Context, _ In) checks.Verdict { <-ctx.Done(); return checks.Verdict{} })
	d.Timeout = time.Second
	began := time.Now()
	code, v := runAtom(t, d)
	if code != 2 || v.Reason != "deadline 1s passed" || time.Since(began) > 1500*time.Millisecond {
		t.Fatalf("exit %d, %+v after %v", code, v, time.Since(began))
	}
}

func TestADeadlineCutsACheckThatIgnoresItsContext(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	d := defWith(func(context.Context, In) checks.Verdict { <-block; return checks.Verdict{} })
	d.Timeout = 50 * time.Millisecond
	if code, v := runAtom(t, d); code != 2 || v.Reason != "deadline 0.05s passed" {
		t.Fatalf("exit %d, %+v", code, v)
	}
}

// A Def's Deadline gives the check the longer of it and Timeout over this input:
// an atom whose worst case grows with the change is not cut off at its timeout,
// and a deadline shorter than the timeout shortens nothing.
func TestADeadlineThatGrowsWithTheInputLengthensTheCheck(t *testing.T) {
	slow := func(ctx context.Context, _ In) checks.Verdict {
		select {
		case <-time.After(150 * time.Millisecond):
			return checks.Verdict{State: 0, Result: "pass"}
		case <-ctx.Done():
			return checks.Verdict{State: 2, Result: "cannot-run"}
		}
	}
	for _, tc := range []struct {
		name     string
		deadline func(In) time.Duration
		want     int
	}{
		{"none: the timeout cuts it", nil, 2},
		{"a longer deadline lets it finish", func(In) time.Duration { return 5 * time.Second }, 0},
		{"a shorter deadline does not shorten the timeout", func(In) time.Duration { return time.Millisecond }, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := defWith(slow)
			d.Timeout = 50 * time.Millisecond
			d.Deadline = tc.deadline
			if v := check(d, In{}); v.State != tc.want {
				t.Errorf("state %d, want %d: %+v", v.State, tc.want, v)
			}
		})
	}
}

func TestUT_454_NoTreeFactsAndNoGit(t *testing.T) {
	t.Setenv("ATOM_TREE", "")
	t.Setenv("PATH", "/nonexistent")
	out := filepath.Join(t.TempDir(), "v.json")
	t.Setenv("ATOM_OUT", out)
	if code := run(defWith(passCheck), []string{"atom"}); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if v := readVerdict(t, out); v.State != 2 || v.Reason != "no tree facts and no git" {
		t.Fatalf("%+v", v)
	}
}

func TestUT_455_ATOMOUTDefaultsUnderWork(t *testing.T) {
	treeFile(t, TF1())
	t.Setenv("ATOM_OUT", "")
	old := workRoot
	workRoot = t.TempDir()
	t.Cleanup(func() { workRoot = old })
	if err := os.MkdirAll(filepath.Join(workRoot, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := defWith(passCheck)
	d.ID = "fleet:check-merge-conflict"
	if code := run(d, []string{"atom"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if v := readVerdict(t, filepath.Join(workRoot, "out", "fleet__check-merge-conflict.json")); v.Atom != d.ID {
		t.Fatalf("%+v", v)
	}
}

var e3 = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`)

func TestUT_456_TheAtomLeavesItsIdentityAndTimesToMain(t *testing.T) {
	d := defWith(func(context.Context, In) checks.Verdict { return checks.Verdict{Result: "pass"} })
	d.Stage = "prepush"
	_, v := runAtom(t, d)
	if v.Atom != "st:x" || v.Stage != "prepush" || v.Lane != "go" || v.State != 0 || v.Result != "pass" || v.Logs == nil || len(v.Logs) != 0 {
		t.Fatalf("%+v", v)
	}
	if !e3.MatchString(v.StartedAt) || !e3.MatchString(v.FinishedAt) {
		t.Fatalf("times %q %q", v.StartedAt, v.FinishedAt)
	}
}

func TestUT_456_TimesComeFromTheClock(t *testing.T) {
	ticks := []time.Time{time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC), time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC), time.Date(2026, 10, 15, 1, 0, 2, 500e6, time.FixedZone("EST", -5*3600))}
	i := 0
	old := now
	now = func() time.Time { i = min(i+1, len(ticks)); return ticks[i-1] }
	t.Cleanup(func() { now = old })
	_, v := runAtom(t, defWith(passCheck))
	if v.StartedAt != "2026-10-15T01:00:00.000Z" || v.FinishedAt != "2026-10-15T06:00:02.500Z" {
		t.Fatalf("%q %q", v.StartedAt, v.FinishedAt)
	}
}

func TestUT_457_ATimeTheCheckSetIsKept(t *testing.T) {
	d := defWith(func(context.Context, In) checks.Verdict {
		return checks.Verdict{StartedAt: "2026-10-15T00:00:00.000Z", FinishedAt: "2026-10-15T00:00:01.000Z"}
	})
	if _, v := runAtom(t, d); v.StartedAt != "2026-10-15T00:00:00.000Z" || v.FinishedAt != "2026-10-15T00:00:01.000Z" {
		t.Fatalf("%q %q", v.StartedAt, v.FinishedAt)
	}
}

func TestUT_458_AnUnwritableVerdictExitsTwo(t *testing.T) {
	_, errOut := captureStreams(t)
	treeFile(t, TF1())
	t.Setenv("ATOM_OUT", "/proc/x/v.json")
	if code := run(defWith(passCheck), []string{"atom"}); code != 2 || !strings.HasPrefix(errOut.String(), "atomkit: verdict unwritable: ") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestUT_459_AStateZeroExitsZero(t *testing.T) {
	if code, _ := runAtom(t, defWith(passCheck)); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

func TestMainExitsWithTheRunsCode(t *testing.T) {
	var got = -1
	oldExit, oldArgs := exit, os.Args
	exit, os.Args = func(c int) { got = c }, []string{"atom", "--describe"}
	t.Cleanup(func() { exit, os.Args = oldExit, oldArgs })
	captureStreams(t)
	Main(goDef())
	if got != 0 {
		t.Fatalf("exit %d", got)
	}
}

func TestACouldNotRunReasonIsOneLineAndCapped(t *testing.T) {
	if got := oneLine("a\nb"); got != "a b" {
		t.Fatalf("%q", got)
	}
	long := strings.Repeat("é", 1500) // 3000 bytes, every rune two bytes
	got := oneLine(long)
	if !strings.HasSuffix(got, "…") || len(got) > reasonCap+len("…") || strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("len %d", len(got))
	}
	odd := "a" + strings.Repeat("é", 1500) // the 2000-byte cut lands inside a rune
	if got := oneLine(odd); strings.ContainsRune(got, '\uFFFD') || len(got) > reasonCap+len("…") {
		t.Fatalf("len %d", len(got))
	}
}

// ---- LoadIn

func TestUT_460_LoadInFromTreeFacts(t *testing.T) {
	tf := TF1()
	tf.Fork = "gravity"
	treeFile(t, tf)
	in, err := LoadIn("/ignored")
	if err != nil {
		t.Fatal(err)
	}
	if in.Root != "/src" || len(in.Files) != 2 || len(in.Committable) != 2 || len(in.Tracked) != 2 || len(in.Changed) != 0 ||
		in.Base != "" || in.Origin != "http://door.test/rob/nyx.git" || in.Fork != "gravity" ||
		in.FilesErr != nil || in.TrackedErr != nil || in.ChangedErr != nil {
		t.Fatalf("%+v", in)
	}
}

func TestLoadInFallsBackToTheRootForATreeWithNone(t *testing.T) {
	tf := TF1()
	tf.Root = ""
	treeFile(t, tf)
	if in, err := LoadIn("/work/src"); err != nil || in.Root != "/work/src" {
		t.Fatalf("%q %v", in.Root, err)
	}
}

func TestUT_461_ATreeOfAnotherVersionIsRefused(t *testing.T) {
	tf := TF1()
	tf.Version = 2
	treeFile(t, tf)
	if _, err := LoadIn("/src"); err == nil || err.Error() != "tree@1 version 2" {
		t.Fatalf("%v", err)
	}
}

func TestLoadInRefusesAnUnreadableTree(t *testing.T) {
	t.Setenv("ATOM_TREE", filepath.Join(t.TempDir(), "missing.json"))
	if _, err := LoadIn("/src"); err == nil || !strings.Contains(err.Error(), "missing.json") {
		t.Fatalf("%v", err)
	}
}

func TestUT_462_ATreeErrorIsAnError(t *testing.T) {
	tf := TF1()
	tf.TrackedErr = "x"
	treeFile(t, tf)
	in, err := LoadIn("/src")
	if err != nil || in.TrackedErr == nil || in.TrackedErr.Error() != "x" || in.FilesErr != nil {
		t.Fatalf("%+v %v", in, err)
	}
}

func TestUT_463_LoadInComputesTheTreeWhenNoneIsGiven(t *testing.T) {
	dir := fx3(t)
	t.Setenv("ATOM_TREE", "")
	in, err := LoadIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(in.Files, ",") != "go.mod,main.go,notes.txt" || in.Root != dir {
		t.Fatalf("%+v", in)
	}
}

// TestLoadInComputesTheTreeAgainstTheEnvironment: with no tree@1 the adapter
// names the change set's origin and fork through the environment.
func TestLoadInComputesTheTreeAgainstTheEnvironment(t *testing.T) {
	dir := fx3(t)
	t.Setenv("ATOM_TREE", "")
	t.Setenv("ATOM_ORIGIN", "http://door.test/rob/nyx.git")
	t.Setenv("ATOM_FORK", "gravity")
	in, err := LoadIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if in.Origin != "http://door.test/rob/nyx.git" || in.Fork != "gravity" {
		t.Fatalf("origin %q fork %q", in.Origin, in.Fork)
	}
}

func TestUT_464_LoadInWithNoTreeAndNoGit(t *testing.T) {
	t.Setenv("ATOM_TREE", "")
	t.Setenv("PATH", "/nonexistent")
	if _, err := LoadIn("/src"); err == nil || err.Error() != "no tree facts and no git" {
		t.Fatalf("%v", err)
	}
}

func TestUT_465_LoadInReadsTheEnvironment(t *testing.T) {
	treeFile(t, TF1())
	t.Setenv("ATOM_DIES", "/dies")
	t.Setenv("SPIFFE_ENDPOINT_SOCKET", "unix:///run/spire/agent/public/api.sock")
	t.Setenv("ATOM_DOOR", "http://door.test")
	in, err := LoadIn("/src")
	if err != nil {
		t.Fatal(err)
	}
	if in.Dies != "/dies" || in.Spire != "/run/spire/agent/public/api.sock" || in.Door.Base != "http://door.test" || in.Exec != nil || in.FS != nil || in.Now.IsZero() {
		t.Fatalf("%+v", in)
	}
}

func TestUT_466_WriteVerdict(t *testing.T) {
	p := filepath.Join(t.TempDir(), "v.json")
	if err := WriteVerdict(p, checks.Verdict{Atom: "a:b", State: 0, Result: "pass"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if want := `{"atom":"a:b","stage":"","lane":"","state":0,"result":"pass","logs":[]}` + "\n"; string(got) != want {
		t.Fatalf("%q", got)
	}
	if left, _ := filepath.Glob(p + ".tmp.*"); len(left) != 0 {
		t.Fatalf("temp left: %v", left)
	}
}

// ---- helpers shared with the tree tests

// git runs one git command in dir with the developer's own configuration out of the way.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// put writes a file, making its directories.
func put(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commitAll stages everything and commits, answering the new sha.
func commitAll(t *testing.T, dir, msg string) string {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", msg)
	return git(t, dir, "rev-parse", "HEAD")
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

// fx1 puts FX-1 in dir; fx3 is FX-3 as a repository: FX-1 committed, then
// notes.txt added in a second commit.
func fx1(t *testing.T, dir string) {
	put(t, dir, "go.mod", "module example.com/stfix\n\ngo 1.26\n")
	put(t, dir, "main.go", "package main\n\nfunc main() {}\n")
}

func fx3(t *testing.T) string {
	t.Helper()
	dir := newRepo(t)
	fx1(t, dir)
	commitAll(t, dir, "p")
	put(t, dir, "notes.txt", "<<<<<<< HEAD\n")
	commitAll(t, dir, "h")
	return dir
}
