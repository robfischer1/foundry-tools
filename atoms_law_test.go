// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
)

// The judgement is tested in internal/govlaw; these hold what the chain adds:
// the three atoms read the tree and build no container, and a tree that will
// not list is could-not-run.

func lawStocks(extra map[string]string) map[string]string {
	files := map[string]string{
		"kits.toml":                  "[kits.root]\nblocks = [\"forge\"]\n",
		"retired-verbs.toml":         stocksLedger,
		"governance/blocks/forge.md": "CI logs through `repo_ci_logs`.\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return fleetTree(files)
}

var lawAtoms = []string{"law:lint", "law:verb-liveness", "law:budget"}

func TestLawLanesAreAbsentOffFoundryStocks(t *testing.T) {
	for _, id := range lawAtoms {
		engine.reset()
		engine.withTree(fleetTree(map[string]string{
			"governance/blocks/Bad_Name.md": "",
			"renders/c/claude/AGENTS.md":    "`athena_quick_add`",
		}))
		wantState(t, runAtom(t, id, ""), 0, "ABSENT", "not foundry-stocks")
		fleetNoContainer(t, id+": an absence read off the tree")
	}
}

func TestLawLanesPassCurrentLaw(t *testing.T) {
	for _, id := range lawAtoms {
		engine.reset()
		engine.withTree(lawStocks(nil))
		v := runAtom(t, id, "")
		wantState(t, v, 0)
		fleetNoContainer(t, id+": a scan read off the tree")
	}
}

func TestLawVerbLivenessIsRedOnTheAugustText(t *testing.T) {
	engine.reset()
	engine.withTree(lawStocks(map[string]string{
		"governance/blocks/forge.md": "CI logs through `repo_ci_logs`.\nPin a task with `athena_quick_add`.\n",
	}))
	wantState(t, runAtom(t, "law:verb-liveness", ""), 1,
		"governance/blocks/forge.md:2  athena_quick_add is not a verb on hades's surface")
}

func TestLawLanesFindWhatEachOwns(t *testing.T) {
	engine.reset()
	engine.withTree(lawStocks(map[string]string{
		"governance/blocks/empty.md": "",
		"renders/c/claude/AGENTS.md": "text of the render\n",
		"budget.toml":                "[budget.c]\nclaude = 5\n",
	}))
	wantState(t, runAtom(t, "law:lint", ""), 1, "governance/blocks/empty.md: the block is empty")
	wantState(t, runAtom(t, "law:budget", ""), 1, "renders/c/claude: 19 bytes of always-on context is 14 over its ceiling of 5")
}

func TestLawLanesRefuseATreeTheyCannotList(t *testing.T) {
	for _, id := range lawAtoms {
		engine.reset()
		engine.withTree(lawStocks(nil))
		engine.fail(`glob(pattern:"**")`, "the scan broke")
		wantState(t, runAtom(t, id, ""), 2, "CANNOT RUN", "would not enumerate", "the scan broke")
	}
}

func TestLawLanesRefuseAFileTheyCannotRead(t *testing.T) {
	engine.reset()
	engine.withTree(lawStocks(nil))
	engine.fail(`file(path:"governance/blocks/forge.md")`, "i/o error")
	wantState(t, runAtom(t, "law:lint", ""), 2, "CANNOT RUN", "governance/blocks/forge.md would not read", "i/o error")
}
