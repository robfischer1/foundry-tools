package fleetvale

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

// expect holds a verdict to its state, its result word (an ABSENT pass reads
// "absent") and the words it must carry, in Reason or Logs.
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

// stateOf is a table's plain int as the catalogue's State.
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
// it, WITHOUT git: the population is the file list, as Collect would build it
// for a tree with nothing ignored. The dies mount carries the Forge style.
func treeIn(t *testing.T, files map[string]string) atomkit.In {
	t.Helper()
	dir := t.TempDir()
	var names []string
	for name, body := range files {
		put(t, dir, name, body)
		names = append(names, name)
	}
	sort.Strings(names)
	dies := t.TempDir()
	put(t, dies, filepath.Join("styles", checks.ValeStyle, "Shouting.yml"), "extends: existence\n")
	return atomkit.In{
		Root: dir, Files: checks.GatePopulation(names), Committable: names, Tracked: names, Dies: dies,
		Now: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC),
	}
}

// toolFake stands in for the programs on PATH through In.Exec: it answers each
// call from a script, records it, and fails the test for a program the tools
// container does not carry (atoms.Programs).
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

// toolTree is treeIn with the exec seam set to the script.
func toolTree(t *testing.T, files map[string]string, answer func(atoms.Cmd) (string, int)) (atomkit.In, *toolFake) {
	t.Helper()
	in := treeIn(t, files)
	f := &toolFake{t: t, answer: answer}
	in.Exec = f.exec
	return in, f
}

// ran is the program names the atom exec'd, in order.
func (f *toolFake) ran() []string {
	var names []string
	for _, c := range f.calls {
		names = append(names, c.Name)
	}
	return names
}

// flagged is an argv as one string, for a needle.
func flagged(c atoms.Cmd) string { return strings.Join(c.Args, " ") }

var errBoom = errors.New("boom")

// prose is a tree with two model-read files and one that is not.
var prose = map[string]string{"SKILL.md": "# S\n", "docs/AGENTS.md": "# A\n", "main.go": "x"}

func TestFleetVale(t *testing.T) {
	pinned := "vale version " + checks.ValeVersion
	for _, tc := range []struct {
		name     string
		version  string
		vcode    int
		lint     string
		lcode    int
		state    int
		result   string
		needles  []string
		wantLint bool
	}{
		{"a clean lint passes", pinned, 0, "", 0, 0, pass, nil, true},
		{"a warning alone still passes, and is printed", pinned, 0, "SKILL.md:3:1:Forge.Shouting:'MUST' is shouting.", 0, 0, pass, []string{"Forge.Shouting"}, true},
		{"an error-level alert is exit 1", pinned, 0, "SKILL.md:9:4:Forge.ArgumentSlot:'$1' is read as an argument slot", 1, 1, findings, []string{"Forge.ArgumentSlot"}, true},
		{"vale's own runtime error is exit 2", pinned, 0, "E100 [--config] Runtime error", 2, 2, cannot, nil, true},
		{"any other exit is a could-not-run", pinned, 0, "oom", 137, 2, cannot, nil, true},
		{"a vale that is not the pinned one", "vale version 3.0.0", 0, "", 0, 2, cannot,
			[]string{"does not answer \"vale version " + checks.ValeVersion + "\"", "A file that was never linted"}, false},
		{"a version probe that fails", "vale: not found", -1, "", 0, 2, cannot, []string{"the vale version probe never ran: vale: not found"}, false},
		{"a lint that would not start", pinned, 0, "vale: gone", -1, 2, cannot, []string{"the atom never ran: vale: gone"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var config string
			in, f := toolTree(t, prose, func(c atoms.Cmd) (string, int) {
				if c.Args[0] == "--version" {
					return tc.version, tc.vcode
				}
				// The configuration must be on disk while vale runs.
				body, err := os.ReadFile(c.Args[5])
				if err != nil {
					t.Errorf("--config %q is not readable while vale runs: %v", c.Args[5], err)
				}
				config = string(body)
				return tc.lint, tc.lcode
			})
			expect(t, run(t, in), stateOf(tc.state), tc.result, tc.needles...)
			if !tc.wantLint {
				if len(f.calls) != 1 {
					t.Errorf("a failed probe went on to run %v", f.ran())
				}
				return
			}
			c := f.calls[1]
			args := c.Args
			if strings.Join(args[:4], " ") != "--no-global --no-color --no-wrap --output=line" || args[4] != "--config" ||
				strings.Join(args[6:], " ") != "-- SKILL.md docs/AGENTS.md" {
				t.Errorf("vale was run as %q", flagged(c))
			}
			if config != checks.ValeConfig(filepath.Join(in.Dies, "styles")) {
				t.Errorf("the configuration handed over is not the fleet's:\n%s", config)
			}
			if _, err := os.Stat(args[5]); err == nil {
				t.Errorf("the configuration %s was left behind", args[5])
			}
			if !c.Both || c.Dir != in.Root {
				t.Errorf("vale ran in %q (both streams %v)", c.Dir, c.Both)
			}
		})
	}
	t.Run("no model-read file is absent, and vale is not touched", func(t *testing.T) {
		in, f := toolTree(t, map[string]string{"main.go": "x", "README.md": "# r\n", "docs/design.md": "d"}, nil)
		expect(t, run(t, in), stateOf(0), absent, "tracks no file a model reads as instructions")
		if len(f.calls) != 0 {
			t.Errorf("an absent atom touched %v", f.ran())
		}
	})
	t.Run("a population that failed is a could-not-run", func(t *testing.T) {
		in, _ := toolTree(t, prose, nil)
		in.FilesErr = errBoom
		expect(t, run(t, in), stateOf(2), cannot, "the tree would not enumerate")
	})
	t.Run("no dies mount is a could-not-run before vale is touched", func(t *testing.T) {
		in, f := toolTree(t, prose, nil)
		in.Dies = ""
		expect(t, run(t, in), stateOf(2), cannot, "ATOM_DIES is empty", "A file that was never linted")
		if len(f.calls) != 0 {
			t.Errorf("vale ran with no style to read: %v", f.ran())
		}
	})
	t.Run("a dies mount without the Forge style is a could-not-run", func(t *testing.T) {
		in, f := toolTree(t, prose, nil)
		in.Dies = t.TempDir()
		expect(t, run(t, in), stateOf(2), cannot, "carries no styles/"+checks.ValeStyle)
		if len(f.calls) != 0 {
			t.Errorf("vale ran with no style to read: %v", f.ran())
		}
	})
	t.Run("a configuration that cannot be written is a could-not-run", func(t *testing.T) {
		// atoms.WorkDir reads ATOM_WORK before TMPDIR, and the dev lane sets it.
		gone := filepath.Join(t.TempDir(), "gone")
		t.Setenv("ATOM_WORK", gone)
		t.Setenv("TMPDIR", gone)
		in, f := toolTree(t, prose, func(atoms.Cmd) (string, int) { return pinned, 0 })
		expect(t, run(t, in), stateOf(2), cannot, "the fleet's configuration could not be written")
		if len(f.calls) != 1 {
			t.Errorf("vale linted without its configuration: %v", f.ran())
		}
	})
}

// The declaration: the same shape as its sibling fleet:hadolint, plus the dies
// mount the style is read from.
func TestTheDeclarationIsWellFormed(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "none" || Def.NotOnForks != "" || Def.SubsumedBy != "" || Def.NeedsManifest || Def.Surface != "any:**" {
		t.Errorf("%+v", Def)
	}
	if !slices.Equal(Def.Needs, []string{"dies", "tool:vale"}) {
		t.Errorf("needs %v", Def.Needs)
	}
}
