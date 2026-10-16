package fleetcopieranswersintact

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
	"dagger/foundry-tools/internal/checks"
)

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

func TestCopierAnswersIntact(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"no answers file is absent", map[string]string{"a": "x"}, 0, absent, []string{"carries no .copier-answers.yml", "not copier-stamped"}},
		{"a clean answers file passes", map[string]string{".copier-answers.yml": "_commit: v1\n"}, 0, pass, []string{"no crash receipt"}},
		{"the crash receipt is a finding", map[string]string{".copier-answers.yml": "_commit: v1\n#copier updated"}, 1, findings,
			[]string{"#copier updated", "COPIER CRASHED", "FIX: delete the marker line"}},
		{"an answers file that will not read is a file not examined",
			map[string]string{".copier-answers.yml/x": "x"}, 2, cannot, []string{".copier-answers.yml would not read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expect(t, run(t, treeIn(t, tc.files)), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		expect(t, run(t, missingRoot(t)), stateOf(2), cannot, "the tree would not enumerate")
	})
}

const answersClean = `_commit: v0.23.0
_src_path: https://forgejo.notusmi.com/rob/go-repo-template.git
project_name: X
`

// The marker as renovate leaves it: appended after a blank line, with no
// trailing newline.
const answersLatched = answersClean + "\n#copier updated"

// A clean answers file is a PASS, not an ABSENCE: "absent" means there was
// nothing here to check, and there WAS.
func TestCopierAnswersIntactHoldsOnAFileWithNoMarker(t *testing.T) {
	expect(t, run(t, treeIn(t, map[string]string{".copier-answers.yml": answersClean})), stateOf(0), pass, "no crash receipt")
}

// THE INCIDENT. A commit titled "update dependency <template> to v0.36.0" whose
// entire diff is this one comment line is green by every other measure.
func TestCopierAnswersIntactRefusesTheCrashReceipt(t *testing.T) {
	v := run(t, treeIn(t, map[string]string{".copier-answers.yml": answersLatched}))
	expect(t, v, stateOf(1), findings, "#copier updated", "COPIER CRASHED")
}

// The finding has to say what to DO, because the fix is not the obvious one: the
// instinct on a stale marker is to re-run renovate, and re-running cannot work.
func TestCopierAnswersIntactNamesTheFixAndTheLatch(t *testing.T) {
	out := run(t, treeIn(t, map[string]string{".copier-answers.yml": answersLatched})).Reason
	for _, want := range []string{
		"delete the marker line", // what to do
		"ends in a newline",      // the second, separate breakage
		"LATCHES",                // why re-running renovate will not help
		"renovate#36147",         // where the behaviour is documented
		"_commit",                // what NOT to touch
	} {
		if !strings.Contains(out, want) {
			t.Errorf("finding does not mention %q:\n%s", want, out)
		}
	}
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
	if Def.Desc != "No crashed copier render is recorded in .copier-answers.yml." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, nil) {
		t.Errorf("needs %v", Def.Needs)
	}
}
