package fleetretiredverbs

import (
	"context"
	"errors"
	"io/fs"
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

var errBoom = errors.New("boom")

// failingFS is the disk, except that listing the named directory fails.
type failingFS struct {
	fs.FS
	dir string
}

func (f failingFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == f.dir {
		return nil, errBoom
	}
	return fs.ReadDir(f.FS, name)
}

const ledger = "[retired.old_verb]\nstar = \"x\"\nsuccessor = \"new_verb\"\nretired_by = \"y\"\n"

func TestRetiredVerbs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"no ledger is absent", map[string]string{"skills/a.md": "old_verb"}, 0, absent, []string{"carries no retired-verbs.toml"}},
		{"units that name no retired verb pass", map[string]string{"retired-verbs.toml": ledger, "skills/a.md": "new_verb only"}, 0, pass,
			[]string{"1 unit(s) name none of the 1 retired verbs"}},
		{"a unit naming a retired verb is a finding with its successor",
			map[string]string{"retired-verbs.toml": ledger, "skills/a.md": "ok\nuse old_verb here\n", "agents/b.toml": "fine"}, 1, findings,
			[]string{"1 line(s) still name a verb", "skills/a.md:2  old_verb", "call instead: new_verb"}},
		{"a name inside a longer identifier is not the verb",
			map[string]string{"retired-verbs.toml": ledger, "skills/a.md": "old_verb_total"}, 0, pass, nil},
		{"a file outside the composition surface is not a unit",
			map[string]string{"retired-verbs.toml": ledger, "docs/a.md": "old_verb", "skills/ok.md": "x"}, 0, pass, nil},
		{"a ledger that is not TOML cannot grade", map[string]string{"retired-verbs.toml": "= not toml", "skills/a.md": "x"}, 2, cannot,
			[]string{"is not a ledger this atom can grade against"}},
		{"a ledger with no unit beside it is a surface that moved", map[string]string{"retired-verbs.toml": ledger, "README.md": "x"}, 2, cannot,
			[]string{"is here and no unit is"}},
		{"a ledger that will not read", map[string]string{"retired-verbs.toml/x": "x"}, 2, cannot, []string{"retired-verbs.toml would not read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expect(t, run(t, treeIn(t, tc.files)), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a composition surface that will not walk", func(t *testing.T) {
		in := treeIn(t, map[string]string{"retired-verbs.toml": ledger, "skills/ok.md": "x"})
		in.FS = failingFS{FS: os.DirFS(in.Root), dir: "skills"}
		expect(t, run(t, in), stateOf(2), cannot, "the composition surface would not enumerate: boom")
	})
	t.Run("a unit that will not read", func(t *testing.T) {
		in := treeIn(t, map[string]string{"retired-verbs.toml": ledger, "skills/ok.md": "x"})
		if err := os.Symlink(filepath.Join(in.Root, "nowhere"), filepath.Join(in.Root, "skills", "broken.md")); err != nil {
			t.Fatal(err)
		}
		expect(t, run(t, in), stateOf(2), cannot, "skills/broken.md would not read")
	})
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		expect(t, run(t, missingRoot(t)), stateOf(2), cannot, "the tree would not enumerate")
	})
}

const stocksLedger = `[retired.tethys_pnl]
star = "tethys"
successor = 'model_pnl(by="job")'
retired_by = "F12"

[retired.ourea_pr]
star = "ourea"
successor = "git_pr(...)"
retired_by = "the prefix rename"
`

// stocksIn is a tree that looks like foundry-stocks to this atom: the ledger at
// the root and units beside it.
func stocksIn(t *testing.T, units map[string]string) atomkit.In {
	files := map[string]string{"retired-verbs.toml": stocksLedger}
	for k, v := range units {
		files[k] = v
	}
	return treeIn(t, files)
}

func TestRetiredVerbsPassesACleanSurface(t *testing.T) {
	v := run(t, stocksIn(t, map[string]string{
		"skills/todo/todo.md":           "call git_pr\n",
		"hooks/git_guard/git_guard.py":  "TOOL = 'git_merge'\n",
		"rules/worktree-check.md":       "the metric ourea_pr_node_mint_total is not a verb\n",
		"specs/001/plan.md":             "ourea_pr is named in a spec, which no session loads\n",
		"skills/dispatch/run.sh":        "ourea_pr in a file type the surface does not carry\n",
		"governance/blocks/forge.md":    "",
		"settings/base.json":            `{"allow": ["mcp__hades__git_pr"]}`,
		"retired/terms/Term/old-one.md": "tethys_pnl in the archive\n",
	}))
	// A pass keeps its output in Logs, not Reason. The count is the proof that
	// the paths outside the surface were not read as units.
	expect(t, v, stateOf(0), pass, "5 unit(s) name none of the 2 retired verbs")
}

func TestRetiredVerbsFindsEachLineAndNamesTheSuccessor(t *testing.T) {
	v := run(t, stocksIn(t, map[string]string{
		"skills/todo/skill.toml":   "allowed_tools = [\"mcp__hades__ourea_pr\"]\n",
		"skills/dispatch/pnl.md":   "first line\nreach for tethys_pnl\n",
		"agents/clean.md":          "nothing retired here\n",
		"commands/land.md":         "ourea_pr(action=\"open\")\n",
		"rules/metric-not-verb.md": "ourea_pr_thesis_total\n",
	}))
	expect(t, v, stateOf(1), findings,
		"FINDINGS - 3 line(s)",
		"commands/land.md:1  ourea_pr",
		"skills/dispatch/pnl.md:2  tethys_pnl",
		"skills/todo/skill.toml:1  ourea_pr",
		"call instead: git_pr(...)",
		`call instead: model_pnl(by="job")`,
		"FIX: write the successor")
	for _, clean := range []string{"agents/clean.md", "rules/metric-not-verb.md"} {
		if strings.Contains(v.Reason, clean) {
			t.Errorf("a clean unit was reported: %s\n%s", clean, v.Reason)
		}
	}
	// Units are read in path order, so the report is stable across runs.
	first, second := strings.Index(v.Reason, "commands/land.md"), strings.Index(v.Reason, "skills/dispatch/pnl.md")
	if first < 0 || second < first {
		t.Errorf("findings are not in path order:\n%s", v.Reason)
	}
}

func TestRetiredVerbsRefusesALedgerItCannotUse(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"not toml", "[retired.x\n", "not TOML"},
		{"empty", "# every entry was deleted\n", "no [retired.<name>] entry"},
		{"no successor", "[retired.a_b]\nstar = \"a\"\n", "[retired.a_b] names no successor"},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := treeIn(t, map[string]string{"retired-verbs.toml": c.body, "skills/a.md": "x\n"})
			expect(t, run(t, in), stateOf(2), cannot, "CANNOT RUN", "not a ledger this atom can grade against", c.want)
		})
	}
}

// The ledger with no unit beside it is a surface that moved, and "0 files, 0
// findings" would be a green over nothing.
func TestRetiredVerbsRefusesALedgerWithNoUnitsBesideIt(t *testing.T) {
	expect(t, run(t, stocksIn(t, map[string]string{"specs/001/plan.md": "ourea_pr\n"})), stateOf(2), cannot,
		"CANNOT RUN", "no unit is", "the composition surface moved")
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
	if Def.Desc != "No skill, agent, command, hook, setting, governance block or rule names a retired MCP verb." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, nil) {
		t.Errorf("needs %v", Def.Needs)
	}
}
