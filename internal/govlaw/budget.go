// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package govlaw

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

// BudgetID is the budget lane's catalogue id.
const BudgetID = "law:budget"

// Budget holds the always-on context of every render to a ceiling kept in
// renders/.budget.toml:
//
//	[budget.<consumer>]
//	<provider> = <bytes>
//
// WHAT IS COUNTED. The bytes a session loads on every turn from a render: its
// AGENTS.md, and its APPEND_SYSTEM.md where the provider has one. The CLAUDE.md
// shim is a constant import line and is not counted.
//
// THE RATCHET. A render over its ceiling is a finding, so a commit that grows a
// context document past it can only land by raising the ceiling in the same
// commit, where the raise is a line in the diff a reviewer reads. A render
// that has no ceiling is a finding too (a new consumer sets its own). A render
// under its ceiling is a warning, never a stop: the ceiling should come down to
// meet it, and the next growth would otherwise have the slack for free.
//
// A tree with no renders is ABSENT. A tree with renders and no budget file is a
// finding on every render: nothing holds them.
func Budget(t Tree) Result {
	if !IsStocks(t) {
		return absent(BudgetID)
	}
	docs, groups := t.matching(renderDocRe)
	sizes := map[string]int{}
	for _, p := range docs {
		g := groups[p]
		if g[2] == shimDocName {
			continue
		}
		text, err := t.Read(p)
		if err != nil {
			return cannotRun(BudgetID, "%s would not read: %v", p, err)
		}
		sizes[renderKey(g[0], g[1])] += len(text)
	}
	if len(sizes) == 0 {
		return Result{StatePass, BudgetID + ": ABSENT - no render under " + rendersDir + " carries a context document, so there is nothing to hold to a budget"}
	}
	ceilings := map[string]int64{}
	if t.has(budgetFile) {
		body, err := t.Read(budgetFile)
		if err != nil {
			return cannotRun(BudgetID, "%s would not read: %v", budgetFile, err)
		}
		var doc struct {
			Budget map[string]map[string]int64 `toml:"budget"`
		}
		if _, err := toml.Decode(body, &doc); err != nil {
			return cannotRun(BudgetID, "%s is not a budget this lane can read: %v", budgetFile, err)
		}
		for consumer, providers := range doc.Budget {
			for provider, bytes := range providers {
				if bytes <= 0 {
					return cannotRun(BudgetID, "%s: [budget.%s] %s = %d is not a ceiling (it must be a positive byte count)", budgetFile, consumer, provider, bytes)
				}
				ceilings[renderKey(consumer, provider)] = bytes
			}
		}
	}
	var findings, warnings []string
	for _, key := range sortedKeys(sizes) {
		size := int64(sizes[key])
		ceiling, ok := ceilings[key]
		switch {
		case !ok:
			findings = append(findings, fmt.Sprintf("renders/%s: %d bytes of always-on context and no ceiling in %s", key, size, budgetFile))
		case size > ceiling:
			findings = append(findings, fmt.Sprintf("renders/%s: %d bytes of always-on context is %d over its ceiling of %d; raise the ceiling in %s in the same commit, or cut the context", key, size, size-ceiling, ceiling, budgetFile))
		case size < ceiling:
			warnings = append(warnings, fmt.Sprintf("renders/%s: %d bytes is %d under its ceiling of %d; lower the ceiling to hold the gain", key, size, ceiling-size, ceiling))
		}
	}
	for _, key := range sortedKeys(ceilings) {
		if _, ok := sizes[key]; !ok {
			warnings = append(warnings, fmt.Sprintf("%s: a ceiling for %s, which has no render", budgetFile, key))
		}
	}
	return verdict(BudgetID, fmt.Sprintf("%d render(s) are within their always-on ceilings", len(sizes)), findings, warnings)
}
