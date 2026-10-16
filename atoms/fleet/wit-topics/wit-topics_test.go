package fleetwittopics

import (
	"context"
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

// missingRoot is an input over a directory that is not there: the atoms that
// open with the root's listing settle 2.
func missingRoot(t *testing.T) atomkit.In {
	t.Helper()
	return atomkit.In{Root: t.TempDir() + "/not-there", Now: time.Now()}
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

// flux is a tree that looks like the fleet's flux checkout to this atom, with
// the checker the tree itself holds.
func init() { toolchainPrograms = []string{"uv"} }

var flux = map[string]string{"prime/orbits/a.yaml": "x", checker: "#!/usr/bin/env python3\n"}

// uvTree is toolTree for the atom: `uv --version` (the provisioning probe) is
// answered, and every other call is the script. A nil script answers 0 and nothing.
func uvTree(t *testing.T, files map[string]string, answer func(atoms.Cmd) (string, int)) (atomkit.In, *toolFake) {
	t.Helper()
	return toolTree(t, files, func(c atoms.Cmd) (string, int) {
		if c.Name == "uv" && len(c.Args) == 1 && c.Args[0] == "--version" {
			return "uv 0.12.13", 0
		}
		if answer == nil {
			return "", 0
		}
		return answer(c)
	})
}

func TestFleetWitTopics(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    int
		out     string
		state   int
		result  string
		needles []string
	}{
		{"a checker that holds is a pass", 0, "wit-topics: redpanda/wit matches redpanda/schemas", 0, pass, []string{"redpanda/wit matches"}},
		{"stale is the checker's exit 1, unmapped", 1, "wit-topics: STALE - aiws-topics.wit", 1, findings, []string{"STALE - aiws-topics.wit"}},
		{"could not run is the checker's exit 2, unmapped", 2, "wit-topics: COULD NOT RUN - no schemas", 2, cannot, []string{"COULD NOT RUN - no schemas"}},
		{"a uv that would not start never ran", -1, "uv: gone", 2, cannot, []string{"the atom never ran: uv: gone"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := uvTree(t, flux, func(atoms.Cmd) (string, int) { return tc.out, tc.code })
			expect(t, run(t, in), stateOf(tc.state), tc.result, tc.needles...)
			c := f.calls[len(f.calls)-1]
			// the checker runs under uv with no package resolved, from the root, both streams, bytecode off
			if c.Name != "uv" || c.Dir != in.Root || !c.Both || !slices.Contains(c.Env, "PYTHONDONTWRITEBYTECODE=1") {
				t.Errorf("call %+v: want uv in %s with both streams and no bytecode", c, in.Root)
			}
			if got := flagged(c); got != "run --no-project --quiet python3 tools/wit-topics" {
				t.Errorf("uv ran as %q: nothing is resolved with --with", got)
			}
		})
	}
	t.Run("a container without uv never ran the checker", func(t *testing.T) {
		in, f := toolTree(t, flux, func(atoms.Cmd) (string, int) { return "uv: not found", 127 })
		expect(t, run(t, in), stateOf(2), cannot, "the atom never ran: uv --version exited 127: uv: not found")
		if len(f.calls) != 1 {
			t.Errorf("the checker ran after a failed probe: %v", f.ran())
		}
	})
	t.Run("a tree with no prime/ is not flux's: absent, and uv is not touched", func(t *testing.T) {
		in, f := uvTree(t, map[string]string{"a.txt": "x", checker: "x"}, nil)
		expect(t, run(t, in), stateOf(0), absent, "carries no prime/", "not the fleet's flux tree")
		if len(f.calls) != 0 {
			t.Errorf("an absent atom touched %v", f.ran())
		}
	})
	t.Run("flux's tree without the checker is a could-not-run naming it, never an absence", func(t *testing.T) {
		in, f := uvTree(t, map[string]string{"prime/orbits/a.yaml": "x"}, nil)
		expect(t, run(t, in), stateOf(2), cannot, checker+" is absent, so there is no checker to run")
		if len(f.calls) != 0 {
			t.Errorf("python ran with no checker: %v", f.ran())
		}
	})
	t.Run("a root that cannot be read", func(t *testing.T) {
		expect(t, run(t, missingRoot(t)), stateOf(2), cannot, "the tree would not enumerate")
	})
}

// The declaration is the catalogue row it replaced: the same stage, group, text and
// fork lane, and the needs the split table gives it.
func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "python" || Def.NotOnForks != "" || Def.SubsumedBy != "" || Def.NeedsManifest {
		t.Errorf("%+v", Def)
	}
	if Def.Desc != "flux's committed redpanda/wit is current with redpanda/schemas - a topic schema changed, added or removed without regenerating, or a hand edit of aiws-topics.wit, fails (stellar-core F2)." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, []string{"tool:uv"}) {
		t.Errorf("needs %v", Def.Needs)
	}
}
