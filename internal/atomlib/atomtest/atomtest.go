// Package atomtest is what the tests of the ported atoms (atoms/<ns>/<name>)
// share: a tree on disk, a repository with history, a fake for the programs on
// PATH and the assertions on a verdict. It is not a test file so every atom's
// test package can import it; nothing outside tests does.
package atomtest

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// GitIn runs git in dir and answers its trimmed stdout; a failure is fatal.
func GitIn(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// NewRepo is an empty repository on main whose git ignores the developer's own
// configuration: a global gitignore, a signing key or a template would otherwise
// change what the tests see.
func NewRepo(t testing.TB) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("these tests build real repositories and need git on PATH: %v", err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	GitIn(t, dir, "init", "-q", "-b", "main")
	GitIn(t, dir, "config", "user.email", "t@example.invalid")
	GitIn(t, dir, "config", "user.name", "t")
	GitIn(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

// Put writes a file, making its directories.
func Put(t testing.TB, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// CommitAll stages everything and commits, answering the new sha.
func CommitAll(t testing.TB, dir, msg string) string {
	t.Helper()
	GitIn(t, dir, "add", "-A")
	GitIn(t, dir, "commit", "-q", "-m", msg)
	return GitIn(t, dir, "rev-parse", "HEAD")
}

// TreeIn writes files to a directory and answers the Input an atom is handed over
// it, WITHOUT git: the population is the file list, as Collect would build it for
// a tree with nothing ignored. Atoms that ask the root, a file or a glob read the
// disk; atoms that ask the lists read these.
func TreeIn(t testing.TB, files map[string]string) atoms.Input {
	t.Helper()
	dir := t.TempDir()
	var names []string
	for name, body := range files {
		Put(t, dir, name, body)
		names = append(names, name)
	}
	sort.Strings(names)
	return atoms.Input{
		Root: dir, Files: checks.GatePopulation(names), Committable: names, Tracked: names,
		Now: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
	}
}

// Expect holds a verdict to its state, its result word (an ABSENT pass reads
// "absent") and the words it must carry. A pass's Reason is only "<id>: PASS" -
// what the atom said rides in its Logs - so the words are looked for in both.
func Expect(t testing.TB, v checks.Verdict, state checks.State, result string, needles ...string) {
	t.Helper()
	if v.State != int(state) || v.Result != result {
		t.Errorf("%s: state %d (%s), want %d (%s)\n%s", v.Atom, v.State, v.Result, state, result, v.Reason)
	}
	said := v.Reason + "\n" + strings.Join(v.Logs, "\n")
	for _, n := range needles {
		if !strings.Contains(said, n) {
			t.Errorf("%s: reason and logs lack %q:\n%s", v.Atom, n, said)
		}
	}
}

// The result words a verdict carries.
var (
	Pass     = checks.StatePass.String()
	Findings = checks.StateFindings.String()
	Cannot   = checks.StateCannotRun.String()
)

// Absent is the result word of a pass that said nothing was checked.
const Absent = "absent"

// StateOf is a table's plain int as the catalogue's State.
func StateOf(n int) checks.State { return checks.State(n) }

// MissingRoot is an Input over a directory that is not there: the atoms that
// open with the root's listing settle 2.
func MissingRoot(t testing.TB) atoms.Input {
	t.Helper()
	return atoms.Input{Root: t.TempDir() + "/not-there", Now: time.Now()}
}

// ErrBoom is the error a failing double answers.
var ErrBoom = errors.New("boom")

// BrokenLink makes a file the glob lists and nothing can read: a symlink to
// nowhere.
func BrokenLink(t testing.TB, root, rel string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "nowhere"), p); err != nil {
		t.Fatal(err)
	}
}

// ToolFake stands in for the programs on PATH through Input.Exec: it answers each
// call from a script, records it, and fails the test for a program the tools
// container does not carry (atoms.Programs).
type ToolFake struct {
	T      testing.TB
	Calls  []atoms.Cmd
	Answer func(c atoms.Cmd) (string, int)
}

// Exec is the seam: set it as Input.Exec.
func (f *ToolFake) Exec(_ context.Context, c atoms.Cmd) (string, int) {
	f.T.Helper()
	if !slices.Contains(atoms.Programs, c.Name) {
		f.T.Errorf("exec of %q: no layer of the tools container carries it", c.Name)
	}
	f.Calls = append(f.Calls, c)
	if f.Answer == nil {
		return "", 0
	}
	return f.Answer(c)
}

// Ran is the program names the atom exec'd, in order.
func (f *ToolFake) Ran() []string {
	var names []string
	for _, c := range f.Calls {
		names = append(names, c.Name)
	}
	return names
}

// Call is the first call of a program, or a failed test.
func (f *ToolFake) Call(name string) atoms.Cmd {
	f.T.Helper()
	for _, c := range f.Calls {
		if c.Name == name {
			return c
		}
	}
	f.T.Fatalf("%s was never exec'd; ran %v", name, f.Ran())
	return atoms.Cmd{}
}

// LastCall is the call the atom made last.
func (f *ToolFake) LastCall(t testing.TB) atoms.Cmd {
	t.Helper()
	if len(f.Calls) == 0 {
		t.Fatal("no program was exec'd")
	}
	return f.Calls[len(f.Calls)-1]
}

// ToolTree is TreeIn with the exec seam set to the script.
func ToolTree(t testing.TB, files map[string]string, answer func(atoms.Cmd) (string, int)) (atoms.Input, *ToolFake) {
	t.Helper()
	in := TreeIn(t, files)
	f := &ToolFake{T: t, Answer: answer}
	in.Exec = f.Exec
	return in, f
}

// PyTree is ToolTree for the atoms that run python: `python3 --version` (the
// provisioning probe) is answered by the venv, and every other call by the
// script. A nil script answers 0 and nothing.
func PyTree(t testing.TB, files map[string]string, answer func(atoms.Cmd) (string, int)) (atoms.Input, *ToolFake) {
	t.Helper()
	return ToolTree(t, files, func(c atoms.Cmd) (string, int) {
		if c.Name == "python3" && len(c.Args) == 1 && c.Args[0] == "--version" {
			return "Python 3.14.0", 0
		}
		if answer == nil {
			return "", 0
		}
		return answer(c)
	})
}

// Flagged is an argv as one string, for a needle.
func Flagged(c atoms.Cmd) string { return strings.Join(c.Args, " ") }

// DeclaresItsRow holds an atom's declaration to the catalogue row it replaced:
// it validates, it is an any-lane atom of this stage and toolchain, and its group
// is the one checks.GroupOf gives its id.
func DeclaresItsRow(t testing.TB, d atomkit.Def, stage, toolchain string) {
	t.Helper()
	if err := atomkit.Validate(d); err != nil {
		t.Fatal(err)
	}
	if d.Stage != stage || d.Toolchain != toolchain || d.Lane != checks.LaneAny || d.Surface != "any:**" || d.Group != checks.GroupOf(d.ID) {
		t.Errorf("%+v", d)
	}
}

// ListTree is every file under dir, slash-separated and sorted.
func ListTree(t testing.TB, dir string) []string {
	t.Helper()
	files, err := atoms.Input{Root: dir}.Tree().Files()
	if err != nil {
		t.Fatal(err)
	}
	return files
}
