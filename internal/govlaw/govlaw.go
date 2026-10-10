// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

// Package govlaw holds the first governance CI lanes: the checks that run on a
// foundry-stocks law commit (lint, verb liveness, the context budget).
//
// foundry-stocks is the tablet the fleet's governance is inscribed on: the block
// library under governance/blocks, the composition tables (kits.toml,
// bundles.toml) and, from Nomos F5, renders/<consumer>/<provider>/ with a span
// map beside each context document. These lanes grade that tree. They are pure
// over a Tree (a path list and a reader), so the Dagger chain and the in-process
// binary adapt their own tree to it and print one judgement.
//
// ONE REPOSITORY'S BUSINESS. Every lane here is ABSENT on a tree that is not
// foundry-stocks (IsStocks), and a stocks tree whose surface moved settles
// CANNOT RUN: "0 blocks, 0 findings" is the zero-file scan this module refuses.
package govlaw

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// The three settled states, the same ladder every atom speaks.
const (
	StatePass       = 0
	StateFindings   = 1
	StateCannotRun  = 2
	ledgerFile      = "retired-verbs.toml"
	kitsFile        = "kits.toml"
	bundlesFile     = "bundles.toml"
	blocksDir       = "governance/blocks/"
	rendersDir      = "renders/"
	budgetFile      = "renders/.budget.toml"
	surfaceFile     = "verb-surface.toml"
	contextDocName  = "AGENTS.md"
	appendDocName   = "APPEND_SYSTEM.md"
	shimDocName     = "CLAUDE.md"
	spansFileInside = ".furnace/spans.json"
)

// Tree is what a lane reads: every tracked path (slash-separated, relative to
// the root) and a reader for one of them.
type Tree struct {
	Paths []string
	Read  func(path string) (string, error)
}

// Result is one lane's verdict: the state and the report that explains it.
type Result struct {
	State  int
	Report string
}

// IsStocks reports whether the tree is foundry-stocks: the two files nothing
// else carries side by side, the kit library and the retired-verb ledger.
func IsStocks(t Tree) bool {
	return t.has(kitsFile) && t.has(ledgerFile)
}

func (t Tree) has(path string) bool {
	for _, p := range t.Paths {
		if p == path {
			return true
		}
	}
	return false
}

// absent is the pass a lane gives a tree that is not foundry-stocks.
func absent(id string) Result {
	return Result{StatePass, id + ": ABSENT - this tree carries no " + kitsFile + " beside a " + ledgerFile +
		", so it is not foundry-stocks and holds no law this lane grades"}
}

func cannotRun(id, format string, args ...any) Result {
	return Result{StateCannotRun, fmt.Sprintf("%s: CANNOT RUN - ", id) + fmt.Sprintf(format, args...)}
}

// verdict folds a lane's findings and warnings into its result. Warnings ride
// on a pass: they are the lane saying something a reader should see and a gate
// should not stop for.
func verdict(id, passed string, findings, warnings []string) Result {
	sort.Strings(findings)
	sort.Strings(warnings)
	var b strings.Builder
	if len(findings) == 0 {
		b.WriteString(id + ": " + passed)
	} else {
		fmt.Fprintf(&b, "%s: FINDINGS - %d problem(s) in the law tree.\n\n", id, len(findings))
		for _, f := range findings {
			b.WriteString("  " + f + "\n")
		}
	}
	if len(warnings) > 0 {
		fmt.Fprintf(&b, "\nWARNINGS (%d, not gating):\n", len(warnings))
		for _, w := range warnings {
			b.WriteString("  " + w + "\n")
		}
	}
	state := StatePass
	if len(findings) > 0 {
		state = StateFindings
	}
	return Result{state, strings.TrimRight(b.String(), "\n")}
}

// matching returns the tree's paths that match re, in the tree's order, with
// the submatches of each.
func (t Tree) matching(re *regexp.Regexp) (paths []string, groups map[string][]string) {
	groups = map[string][]string{}
	for _, p := range t.Paths {
		if m := re.FindStringSubmatch(p); m != nil {
			paths = append(paths, p)
			groups[p] = m[1:]
		}
	}
	return paths, groups
}

var blockRe = regexp.MustCompile(`^` + regexp.QuoteMeta(blocksDir) + `([^/]+)\.md$`)

// renderDoc is renders/<consumer>/<provider>/<file> for one of the three
// always-on documents.
var renderDocRe = regexp.MustCompile(`^renders/([^/]+)/([^/]+)/(` +
	regexp.QuoteMeta(contextDocName) + `|` + regexp.QuoteMeta(shimDocName) + `|` + regexp.QuoteMeta(appendDocName) + `)$`)

// blockStems is the block library's names, sorted. The assembler names a block
// by its file stem.
func (t Tree) blockStems() []string {
	paths, groups := t.matching(blockRe)
	stems := make([]string, len(paths))
	for i, p := range paths {
		stems[i] = groups[p][0]
	}
	return stems
}

// renderKey is the budget's and the reports' name for a render: consumer/provider.
func renderKey(consumer, provider string) string { return consumer + "/" + provider }

func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
