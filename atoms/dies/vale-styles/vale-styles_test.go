package diesvalestyles

import (
	"context"
	"errors"
	"os"
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

// run runs the atom over an input.
func run(t *testing.T, in atomkit.In) checks.Verdict {
	t.Helper()
	return check(context.Background(), in)
}

// expect holds a verdict to its state, its result word and the words it must
// carry, in Reason or Logs.
func expect(t *testing.T, v checks.Verdict, state checks.State, result string, needles ...string) {
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

var pass = checks.StatePass.String()

var findings = checks.StateFindings.String()

var cannot = checks.StateCannotRun.String()

const absent = "absent"

func stateOf(n int) checks.State { return checks.State(n) }

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

// treeIn writes files to a directory and answers the In an atom is handed over
// it, WITHOUT git: the population is the file list.
func treeIn(t *testing.T, files map[string]string) atomkit.In {
	t.Helper()
	dir := t.TempDir()
	var names []string
	for name, body := range files {
		put(t, dir, name, body)
		names = append(names, name)
	}
	sort.Strings(names)
	return atomkit.In{
		Root: dir, Files: checks.GatePopulation(names), Committable: names, Tracked: names,
		Now: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC),
	}
}

type toolFake struct {
	t      *testing.T
	calls  []atoms.Cmd
	answer func(c atoms.Cmd) (string, int)
}

func (f *toolFake) exec(_ context.Context, c atoms.Cmd) (string, int) {
	f.t.Helper()
	if !slices.Contains(atoms.Programs, c.Name) {
		f.t.Errorf("exec of %q: no layer of the tools container carries it", c.Name)
	}
	f.calls = append(f.calls, c)
	if f.answer == nil {
		return "", 0
	}
	return f.answer(c)
}

func toolTree(t *testing.T, files map[string]string, answer func(atoms.Cmd) (string, int)) (atomkit.In, *toolFake) {
	t.Helper()
	in := treeIn(t, files)
	f := &toolFake{t: t, answer: answer}
	in.Exec = f.exec
	return in, f
}

func (f *toolFake) ran() []string {
	var names []string
	for _, c := range f.calls {
		names = append(names, c.Name)
	}
	return names
}

var errBoom = errors.New("boom")

// pkg is a tree shaped like foundry-dies: the config at the root, the style
// under styles/.
var pkg = map[string]string{
	".vale.ini":                     "StylesPath = styles\n",
	"styles/Forge/Shouting.yml":     "extends: existence\n",
	"styles/Forge/Forge.test.yml":   "- name: x\n",
	"styles/config/scripts/A.tengo": "matches := []\n",
	"README.md":                     "# r\n",
}

func TestHasStylePackageNeedsBothHalves(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		files []string
		want  bool
	}{
		"both":        {[]string{".vale.ini", "styles/Forge/A.yml"}, true},
		"ini alone":   {[]string{".vale.ini", "README.md"}, false},
		"style alone": {[]string{"styles/Forge/A.yml"}, false},
		"nested ini":  {[]string{"docs/.vale.ini", "styles/Forge/A.yml"}, false},
		"empty":       {nil, false},
	} {
		if got := HasStylePackage(tc.files); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

func TestDiesValeStyles(t *testing.T) {
	pinned := "vale version " + checks.ValeVersion
	for _, tc := range []struct {
		name     string
		version  string
		vcode    int
		out      string
		code     int
		state    int
		result   string
		needles  []string
		wantTest bool
	}{
		{"every case passing and every rule covered passes", pinned, 0, " SUCCESS  1 file — 28 passed, 0 failed", 0, 0, pass, []string{"28 passed"}, true},
		{"a failed case is exit 1", pinned, 0, "✗ shouting flags caps — output does not contain \"Forge.Shouting\"\n  ERROR   1 file — 27 passed, 1 failed", 1, 1, findings, []string{"1 failed"}, true},
		{"an uncovered rule is exit 1", pinned, 0, "✗ 1 rule produced no alert in any case\n    Forge.Hedging\n  ERROR   1 file — 28 passed, 0 failed, 1 uncovered", 1, 1, findings, []string{"uncovered"}, true},
		{"a case that could not run is exit 2", pinned, 0, "E100 [test] Runtime error\n\n2 cases could not run", 2, 2, cannot, nil, true},
		{"a vale that is not the pinned one", "vale version 3.0.0", 0, "", 0, 2, cannot,
			[]string{"does not answer \"vale version " + checks.ValeVersion + "\"", "never ran is not a style that passed"}, false},
		{"a version probe that fails", "vale: not found", -1, "", 0, 2, cannot, []string{"the vale version probe never ran"}, false},
		{"a run that would not start", pinned, 0, "vale: gone", -1, 2, cannot, []string{"the atom never ran: vale: gone"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := toolTree(t, pkg, func(c atoms.Cmd) (string, int) {
				if c.Args[0] == "--version" {
					return tc.version, tc.vcode
				}
				return tc.out, tc.code
			})
			expect(t, run(t, in), stateOf(tc.state), tc.result, tc.needles...)
			if !tc.wantTest {
				if len(f.calls) != 1 {
					t.Errorf("a failed probe went on to run %v", f.ran())
				}
				return
			}
			c := f.calls[1]
			if strings.Join(c.Args, " ") != "test --no-global --no-color --coverage styles/" {
				t.Errorf("vale was run as %q", strings.Join(c.Args, " "))
			}
			if !c.Both || c.Dir != in.Root {
				t.Errorf("vale ran in %q (both streams %v); the package root is the tree root", c.Dir, c.Both)
			}
		})
	}
	t.Run("a tree without the package is absent, and vale is not touched", func(t *testing.T) {
		in, f := toolTree(t, map[string]string{"README.md": "# r\n", "SKILL.md": "# s\n"}, nil)
		expect(t, run(t, in), stateOf(0), absent, "no root .vale.ini beside a styles/ directory")
		if len(f.calls) != 0 {
			t.Errorf("an absent atom touched %v", f.ran())
		}
	})
	t.Run("a population that failed is a could-not-run", func(t *testing.T) {
		in, _ := toolTree(t, pkg, nil)
		in.FilesErr = errBoom
		expect(t, run(t, in), stateOf(2), cannot, "the tree would not enumerate")
	})
}

func TestTheDeclarationIsWellFormed(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "none" || Def.NotOnForks != "" || Def.SubsumedBy != "" || Def.NeedsManifest || Def.Surface != "any:**" {
		t.Errorf("%+v", Def)
	}
	if !slices.Equal(Def.Needs, []string{"tool:vale"}) {
		t.Errorf("needs %v", Def.Needs)
	}
}
