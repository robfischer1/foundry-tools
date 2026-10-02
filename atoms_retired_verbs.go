// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/retiredverbs"
)

func init() {
	register("fleet:retired-verbs", fleetRetiredVerbs)
}

// retiredVerbsLedger is the ledger's one home: the root of foundry-stocks.
const retiredVerbsLedger = "retired-verbs.toml"

// No unit in the composition surface names a retired MCP verb.
//
// THE LEDGER HAD NO READER. foundry-stocks kept retired-verbs.toml and a shell
// check, compose-integrity.sh, that failed any skill, agent, command, hook,
// setting, governance block or rule still naming a retired wire name. That
// script was deleted on 2026-09-18 with "compose:* atoms" named as its
// replacement, and the compose atoms grade docker-compose files. From then
// until this atom the ledger was a note: a verb could be retired, recorded, and
// still be granted by a skill's allowed-tools row, with nothing red anywhere.
//
// ONE REPOSITORY'S BUSINESS. The ledger lives at foundry-stocks' root and the
// units it grades live beside it, so a tree without the ledger is ABSENT.
//
// A LEDGER WITH NOTHING TO GRADE IS NOT A PASS. If the ledger is here and the
// scan finds no unit, the composition surface moved, and "0 files, 0 findings"
// is the zero-file scan this module exists to refuse.
//
// EXIT 1, NOT 2, for a named verb: the line is there and the fix is to write
// the successor the ledger gives.
func fleetRetiredVerbs(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:retired-verbs")

	entries, err := r.src.Entries(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(entries, retiredVerbsLedger) {
		return checks.VerdictOf(a, 0, string(a.ID)+
			": ABSENT - this tree carries no "+retiredVerbsLedger+", so it is not the fleet's composition source and holds no unit this ledger grades")
	}
	body, err := r.src.File(retiredVerbsLedger).Contents(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf(
			"%s: CANNOT RUN - %s would not read: %v", a.ID, retiredVerbsLedger, err))
	}
	ledger, err := retiredverbs.Parse(body)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf(
			"%s: CANNOT RUN - %s is not a ledger this atom can grade against: %v", a.ID, retiredVerbsLedger, err))
	}

	paths, err := r.src.Glob(ctx, "**")
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf(
			"%s: CANNOT RUN - the composition surface would not enumerate: %v", a.ID, err))
	}
	var units []string
	for _, p := range paths {
		if retiredverbs.Scanned(p) {
			units = append(units, p)
		}
	}
	if len(units) == 0 {
		return checks.VerdictOf(a, 2, string(a.ID)+
			": CANNOT RUN - "+retiredVerbsLedger+" is here and no unit is: nothing under skills/, agents/, commands/, hooks/, settings/, governance/ or rules/ was found to grade, so the composition surface moved and this atom grades nothing until it knows where")
	}
	sort.Strings(units)

	var hits []retiredverbs.Hit
	for _, p := range units {
		text, err := r.src.File(p).Contents(ctx)
		if err != nil {
			return checks.VerdictOf(a, 2, fmt.Sprintf(
				"%s: CANNOT RUN - %s would not read: %v", a.ID, p, err))
		}
		hits = append(hits, retiredverbs.Find(p, text, ledger)...)
	}
	if len(hits) == 0 {
		return checks.VerdictOf(a, 0, fmt.Sprintf(
			"%s: %d unit(s) name none of the %d retired verbs", a.ID, len(units), len(ledger)))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: FINDINGS - %d line(s) still name a verb the gateway no longer serves.\n\n", a.ID, len(hits))
	for _, h := range hits {
		fmt.Fprintf(&b, "  %s:%d  %s\n    call instead: %s\n\n", h.Path, h.Line, h.Verb, ledger[h.Verb].Successor)
	}
	b.WriteString(
		"A retired verb does not fail its caller loudly. An allowed-tools row that grants\n" +
			"it grants nothing, a hook that matches it never fires, and a skill that tells a\n" +
			"session to call it sends the session to a name that does not resolve.\n\n" +
			"FIX: write the successor in each line above. If the name is prose ABOUT the\n" +
			"retirement and must stay, it does not belong in a unit a session loads.")
	return checks.VerdictOf(a, 1, b.String())
}
