package fleetsastrulesetlanes

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

const stamped = "_src_path: https://git.notusmi.com/rob/go-repo-template.git\n"

func TestSastRulesetLanes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"no rules at all is absent when nobody stamped it", map[string]string{"main.go": "x"}, 0, absent,
			[]string{"no rules/sast in this tree, and no SAST-shipping fleet template stamped it"}},
		{"a template that ships no ruleset leaves absence the correct state",
			map[string]string{".copier-answers.yml": "_src_path: https://git.notusmi.com/rob/config-repo-template.git\n"}, 0, absent, nil},
		{"a rules directory without sast is absent too", map[string]string{"rules/other.txt": "x"}, 0, absent,
			[]string{"no rules/sast in this tree"}},
		{"a stamped tree without the ruleset never received it", map[string]string{".copier-answers.yml": stamped}, 2, cannot,
			[]string{"go-repo-template stamped it", "NOTHING SCANNED THIS REPO"}},
		{"an answers file pointing at a template that is not ours does not make it stricter",
			map[string]string{".copier-answers.yml": "_src_path: https://x/y/speckit.git\n"}, 0, absent, nil},
		{"a lane the ruleset never names is a scan that did not happen",
			map[string]string{"rules/sast/a.yml": "rules:\n- languages: [python]\n", "go.mod": "module x\n"}, 2, cannot,
			[]string{"rules/sast declares [python] but this repo also builds: go"}},
		{"a commented-out rule is not coverage",
			map[string]string{"rules/sast/a.yml": "# languages: [go]\n", "go.mod": "module x\n"}, 2, cannot, []string{"declares []"}},
		{"every lane declared passes",
			map[string]string{"rules/sast/a.yml": "languages: [\"go\", rust]\n", "go.mod": "module x\n", "Cargo.toml": "x"}, 0, pass,
			[]string{"ruleset declares every lane this repo builds"}},
		{"a rules that is not a directory will not list", map[string]string{"rules": "x"}, 2, cannot, []string{"the tree would not enumerate"}},
		{"a ruleset file that will not read is a ruleset not examined",
			map[string]string{"rules/sast/a.yml/inner.txt": "x"}, 2, cannot, []string{"rules/sast/a.yml/ would not read"}},
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

// A present ruleset is unaffected by the stamp: the answers file is consulted on
// absence only.
func TestAPresentRulesetIsNotTouchedByTheStampCheck(t *testing.T) {
	in := treeIn(t, map[string]string{".copier-answers.yml": answersClean, "rules/sast/a.yml": "languages: [go]\n", "go.mod": "module x\n"})
	expect(t, run(t, in), stateOf(0), pass)
}

// A stamped tree without the ruleset names the file to copy.
func TestAStampedTreeWithoutTheRulesetNamesTheFileToCopy(t *testing.T) {
	v := run(t, treeIn(t, map[string]string{".copier-answers.yml": answersClean}))
	expect(t, v, stateOf(2), cannot, "CANNOT RUN", "go-repo-template", "never received the file", "rules/sast/dataflow.yml")
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
	if Def.Desc != "The SAST ruleset declares every lane this repository actually builds." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, nil) {
		t.Errorf("needs %v", Def.Needs)
	}
}
