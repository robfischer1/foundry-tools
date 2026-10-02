// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"strings"
	"testing"
)

// THE LEDGER IS ONLY A CHECK WHILE SOMETHING READS IT. It had no reader from
// 2026-09-18 until this atom, so most of these tests are about the ways a
// reader can report a pass over something it never graded: a tree it could not
// list, a ledger it could not parse, a surface that moved out from under it.

const stocksLedger = `[retired.tethys_pnl]
star = "tethys"
successor = 'model_pnl(by="job")'
retired_by = "F12"

[retired.ourea_pr]
star = "ourea"
successor = "git_pr(...)"
retired_by = "the prefix rename"
`

// stocksTree is a tree that looks like foundry-stocks to this atom: the ledger
// at the root and units beside it.
func stocksTree(units map[string]string) map[string]string {
	extra := map[string]string{"retired-verbs.toml": stocksLedger}
	for k, v := range units {
		extra[k] = v
	}
	return fleetTree(extra)
}

func TestRetiredVerbsIsAbsentWithNoLedger(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{"skills/todo/todo.md": "call ourea_pr\n"}))
	wantState(t, runAtom(t, "fleet:retired-verbs", ""), 0,
		"ABSENT", "retired-verbs.toml", "not the fleet's composition source")
	fleetNoContainer(t, "an absence read off the tree")
}

func TestRetiredVerbsPassesACleanSurface(t *testing.T) {
	engine.reset()
	engine.withTree(stocksTree(map[string]string{
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
	// the three paths outside the surface were not read as units.
	v := runAtom(t, "fleet:retired-verbs", "")
	wantState(t, v, 0)
	if logs := fmt.Sprint(v.Logs); !strings.Contains(logs, "5 unit(s) name none of the 2 retired verbs") {
		t.Errorf("the pass does not say what it graded: %s", logs)
	}
	fleetNoContainer(t, "a scan read off the tree")
}

func TestRetiredVerbsFindsEachLineAndNamesTheSuccessor(t *testing.T) {
	engine.reset()
	engine.withTree(stocksTree(map[string]string{
		"skills/todo/skill.toml":   "allowed_tools = [\"mcp__hades__ourea_pr\"]\n",
		"skills/dispatch/pnl.md":   "first line\nreach for tethys_pnl\n",
		"agents/clean.md":          "nothing retired here\n",
		"commands/land.md":         "ourea_pr(action=\"open\")\n",
		"rules/metric-not-verb.md": "ourea_pr_thesis_total\n",
	}))
	v := runAtom(t, "fleet:retired-verbs", "")
	wantState(t, v, 1,
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

// ---- a reader that cannot read has not checked ----

func TestRetiredVerbsRefusesATreeItCannotEnumerate(t *testing.T) {
	engine.reset()
	engine.withTree(stocksTree(map[string]string{"skills/a.md": "x\n"}))
	engine.fail("{directory{entries}}", "the tree went away")
	wantState(t, runAtom(t, "fleet:retired-verbs", ""), 2,
		"CANNOT RUN", "would not enumerate", "the tree went away")
}

func TestRetiredVerbsRefusesALedgerItCannotRead(t *testing.T) {
	engine.reset()
	engine.withTree(stocksTree(map[string]string{"skills/a.md": "x\n"}))
	engine.fail(`file(path:"retired-verbs.toml")`, "i/o error")
	wantState(t, runAtom(t, "fleet:retired-verbs", ""), 2,
		"CANNOT RUN", "retired-verbs.toml would not read", "i/o error")
}

func TestRetiredVerbsRefusesALedgerItCannotUse(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"not toml", "[retired.x\n", "not TOML"},
		{"empty", "# every entry was deleted\n", "no [retired.<name>] entry"},
		{"no successor", "[retired.a_b]\nstar = \"a\"\n", "[retired.a_b] names no successor"},
	} {
		t.Run(c.name, func(t *testing.T) {
			engine.reset()
			engine.withTree(fleetTree(map[string]string{
				"retired-verbs.toml": c.body,
				"skills/a.md":        "x\n",
			}))
			wantState(t, runAtom(t, "fleet:retired-verbs", ""), 2,
				"CANNOT RUN", "not a ledger this atom can grade against", c.want)
		})
	}
}

func TestRetiredVerbsRefusesASurfaceItCannotList(t *testing.T) {
	engine.reset()
	engine.withTree(stocksTree(map[string]string{"skills/a.md": "x\n"}))
	engine.fail(`glob(pattern:"**")`, "the scan broke")
	wantState(t, runAtom(t, "fleet:retired-verbs", ""), 2,
		"CANNOT RUN", "composition surface would not enumerate", "the scan broke")
}

// The ledger with no unit beside it is a surface that moved, and "0 files, 0
// findings" would be a green over nothing.
func TestRetiredVerbsRefusesALedgerWithNoUnitsBesideIt(t *testing.T) {
	engine.reset()
	engine.withTree(stocksTree(map[string]string{"specs/001/plan.md": "ourea_pr\n"}))
	wantState(t, runAtom(t, "fleet:retired-verbs", ""), 2,
		"CANNOT RUN", "no unit is", "the composition surface moved")
}

func TestRetiredVerbsRefusesAUnitItCannotRead(t *testing.T) {
	engine.reset()
	engine.withTree(stocksTree(map[string]string{
		"skills/a.md": "clean\n",
		"skills/b.md": "ourea_pr\n",
	}))
	engine.fail(`file(path:"skills/b.md")`, "i/o error")
	wantState(t, runAtom(t, "fleet:retired-verbs", ""), 2,
		"CANNOT RUN", "skills/b.md would not read", "i/o error")
}
