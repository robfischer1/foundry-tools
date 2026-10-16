package fleethadolint

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
// "absent") and the words it must carry. A pass's Reason is only "<id>: PASS" —
// what the atom said rides in its Logs — so the words are looked for in both.
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
// for a tree with nothing ignored.
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
		Now: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
	}
}

// toolFake stands in for the programs on PATH through In.Exec: it answers each
// call from a script, records it, and fails the test for a program the tools
// container or the atom's toolchain does not carry (atoms.Programs, and
// toolchainPrograms: what the toolchain image provides).
var toolchainPrograms []string

type toolFake struct {
	t      *testing.T
	calls  []atoms.Cmd
	answer func(c atoms.Cmd) (string, int)
}

func (f *toolFake) exec(_ context.Context, c atoms.Cmd) (string, int) {
	f.t.Helper()
	if !slices.Contains(atoms.Programs, c.Name) && !slices.Contains(toolchainPrograms, c.Name) {
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

func TestFleetHadolint(t *testing.T) {
	pinned := "Haskell Dockerfile Linter " + checks.HadolintVersion
	docker := map[string]string{"Dockerfile": "FROM x\n", "img/Containerfile": "FROM y\n"}
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
		{"a finding at warning or above is exit 1", pinned, 0, "Dockerfile:1 DL3006", 1, 1, findings, []string{"DL3006"}, true},
		{"a Dockerfile that does not parse is also exit 1", pinned, 0, "Dockerfile:1 DL1000 unexpected", 1, 1, findings, nil, true},
		{"any other exit is a could-not-run", pinned, 0, "oom", 137, 2, cannot, nil, true},
		{"a hadolint that is not the pinned one", "Haskell Dockerfile Linter 1.0.0", 0, "", 0, 2, cannot,
			[]string{"does not answer \"Haskell Dockerfile Linter " + checks.HadolintVersion + "\"", "A Dockerfile that was never linted"}, false},
		{"a version probe that fails", "hadolint: not found", -1, "", 0, 2, cannot, []string{"the hadolint version probe never ran: hadolint: not found"}, false},
		{"a lint that would not start", pinned, 0, "hadolint: gone", -1, 2, cannot, []string{"the atom never ran: hadolint: gone"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var config string
			in, f := toolTree(t, docker, func(c atoms.Cmd) (string, int) {
				if c.Args[0] == "--version" {
					return tc.version, tc.vcode
				}
				// The ruleset must be on disk while hadolint runs.
				body, err := os.ReadFile(c.Args[2])
				if err != nil {
					t.Errorf("--config %q is not readable while hadolint runs: %v", c.Args[2], err)
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
			if args[0] != "--no-color" || args[1] != "--config" || strings.Join(args[3:], " ") != "-- Dockerfile img/Containerfile" {
				t.Errorf("hadolint was run as %q", flagged(c))
			}
			if config != checks.HadolintConfig {
				t.Errorf("the ruleset handed over is not the fleet's:\n%s", config)
			}
			if _, err := os.Stat(args[2]); err == nil {
				t.Errorf("the ruleset %s was left behind", args[2])
			}
			if !c.Both || c.Dir != in.Root {
				t.Errorf("hadolint ran in %q (both streams %v)", c.Dir, c.Both)
			}
		})
	}
	t.Run("no Dockerfile is absent, and hadolint is not touched", func(t *testing.T) {
		in, f := toolTree(t, map[string]string{"main.go": "x"}, nil)
		expect(t, run(t, in), stateOf(0), absent, "tracks no Dockerfile or Containerfile")
		if len(f.calls) != 0 {
			t.Errorf("an absent atom touched %v", f.ran())
		}
	})
	t.Run("a population that failed is a could-not-run", func(t *testing.T) {
		in, _ := toolTree(t, docker, nil)
		in.FilesErr = errBoom
		expect(t, run(t, in), stateOf(2), cannot, "the tree would not enumerate")
	})
	t.Run("a ruleset that cannot be written is a could-not-run", func(t *testing.T) {
		t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "gone"))
		in, f := toolTree(t, docker, func(atoms.Cmd) (string, int) { return pinned, 0 })
		expect(t, run(t, in), stateOf(2), cannot, "the fleet's ruleset could not be written")
		if len(f.calls) != 1 {
			t.Errorf("hadolint linted without its ruleset: %v", f.ran())
		}
	})
}

// The declaration is the catalogue row it replaced: the same stage, group, text and
// fork lane, and the needs the split table gives it.
func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "none" || Def.NotOnForks != "" || Def.SubsumedBy != "" || Def.NeedsManifest {
		t.Errorf("%+v", Def)
	}
	if Def.Desc != "Every Dockerfile in the tree passes hadolint under the fleet's ruleset." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, []string{"tool:hadolint"}) {
		t.Errorf("needs %v", Def.Needs)
	}
}
