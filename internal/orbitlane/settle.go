// Package orbitlane holds the orbit lane's judgements as pure functions over
// bytes: whether the seam contracts are well formed (orbit:contracts), whether
// a star's laid orbit.toml is what the contracts compose to (orbit:repo), and
// whether a star's code still matches its contracts (orbit:surface). The
// atoms in package main read the trees and the analyzer's answers; every
// decision is here, where a test reaches it without an engine.
//
// THE LANE IS orbit, its own Job and its own row in the CI record, not atoms
// buried in the gate (Rob, 2026-10-03: "Ultimately want orbit validation to be
// a CI check/lane of its own").
package orbitlane

import (
	"fmt"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// Enforce is the lane's one switch. FALSE IS REPORT-ONLY: a finding that
// would be `violated` is recorded as `drifted`, with the reason it would have
// failed, and the atom's state stays 0 — the lane runs and records and blocks
// nothing. Turning it true makes a violated a red atom (state 1). Whether the
// door then WAITS on the lane is the door's expected set, a separate choice.
// Rob decides when this flips; nothing else in the lane changes with it.
const Enforce = false

// reportOnly prefixes a demoted finding's detail, so a reader of the record
// can tell a drift from a violation the lane was told not to fail on.
const reportOnly = "[report-only: would be violated] "

// Settle folds an atom's findings into its state and its reason: 1 when
// Enforce is on and anything is violated, otherwise 0. The findings come back
// with every violated demoted to drifted when Enforce is off. A list with
// nothing to say about the tree is the caller's to have made `inert`.
func Settle(atom string, found []checks.Finding) (int, string, []checks.Finding) {
	return settle(atom, found, Enforce)
}

// settle is Settle with the switch as an argument, so both settings are
// tested while the constant holds one.
func settle(atom string, found []checks.Finding, enforce bool) (int, string, []checks.Finding) {
	out := make([]checks.Finding, len(found))
	counts := map[string]int{}
	state := 0
	for i, f := range found {
		if f.Verdict == checks.VerdictViolated {
			if enforce {
				state = 1
			} else {
				f.Verdict, f.Detail = checks.VerdictDrifted, reportOnly+f.Detail
			}
		}
		f.Probe = atom
		counts[f.Verdict]++
		out[i] = f
	}
	return state, atom + ": " + summary(counts) + lines(out), out
}

// summary is the counts in the schema's precedence order.
func summary(counts map[string]int) string {
	var parts []string
	for _, v := range []string{checks.VerdictUnanalyzable, checks.VerdictViolated, checks.VerdictDrifted,
		checks.VerdictHolds, checks.VerdictInert} {
		if counts[v] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[v], v))
		}
	}
	if len(parts) == 0 {
		return "nothing to check"
	}
	return strings.Join(parts, ", ")
}

// lines is every finding that is not a holds, one per line, so the reason
// reads as what to fix.
func lines(found []checks.Finding) string {
	var b strings.Builder
	for _, f := range found {
		if f.Verdict != checks.VerdictHolds && f.Verdict != checks.VerdictInert {
			fmt.Fprintf(&b, "\n  %s  %s — %s", f.Verdict, f.Subject, f.Detail)
		}
	}
	return b.String()
}

// finding is one row, in the schema's shape (the probe is Settle's).
func finding(verdict, subject, cause, detail string) checks.Finding {
	return checks.Finding{Verdict: verdict, Subject: subject, Cause: cause, Detail: detail}
}

// sortedKeys is a set's members in order.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
