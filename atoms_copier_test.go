// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// EVERY TEST HERE IS ABOUT A GREEN THAT SHOULD HAVE BEEN RED, because that is
// the only failure mode either of these guards has. Neither can produce a false
// red that costs anything — a repo told to delete a comment line, or to copy in
// the ruleset its template ships, has a two-minute fix. What they exist to stop
// is the other direction: a tree that passes while carrying evidence it was
// never actually checked.
//
// Both were paid for before they were written. 49 of 55 repos sat latched on a
// crashed copier render for six weeks with every gate green (renovate#36147),
// and two repos carried NO SAST ruleset at all while the atoms answered
// "ABSENT" at exit 0, which is a pass (foundry-stocks#4415's shape).

const answersClean = `_commit: v0.23.0
_src_path: https://forgejo.notusmi.com/rob/go-repo-template.git
project_name: X
`

// The marker as renovate leaves it: appended after a blank line, with no
// trailing newline — which is separately why the file then reds
// end-of-file-fixer for the next PR in that repo.
const answersLatched = answersClean + "\n#copier updated"

// ---- fleet:copier-answers-intact ----

func TestCopierAnswersIntactIsAbsentWhenNothingStampedTheTree(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(nil, ".copier-answers.yml"))
	v := runAtom(t, "fleet:copier-answers-intact", "")
	wantState(t, v, 0, "ABSENT", "not copier-stamped")
	fleetNoContainer(t, "no .copier-answers.yml")
}

// A clean answers file is a PASS, not an ABSENCE, and the distinction is the
// point: "absent" means there was nothing here to check, and there WAS — the
// file exists, it was read, and it carried no receipt. VerdictOf discards a
// passing atom's output, so the Result is the only thing left to assert on.
func TestCopierAnswersIntactHoldsOnAFileWithNoMarker(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{".copier-answers.yml": answersClean}))
	v := runAtom(t, "fleet:copier-answers-intact", "")
	wantState(t, v, 0)
	if v.Result != "pass" {
		t.Errorf("a read, clean answers file should PASS, not %q", v.Result)
	}
	fleetNoContainer(t, "the answers file is read from the Directory")
}

// THE INCIDENT. A commit titled "update dependency <template> to v0.36.0" whose
// entire diff is this one comment line is green by every other measure — the
// tree parses, nothing conflicts, no lane regressed — and it automerges. This is
// the only thing that disagrees with it.
func TestCopierAnswersIntactRefusesTheCrashReceipt(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{".copier-answers.yml": answersLatched}))
	v := runAtom(t, "fleet:copier-answers-intact", "")
	wantState(t, v, 1, "#copier updated", "COPIER CRASHED")
}

// The finding has to say what to DO, because the fix is not the obvious one: the
// instinct on a stale marker is to re-run renovate, and re-running is precisely
// what cannot work — the latch means the artifact step never fires again.
func TestCopierAnswersIntactNamesTheFixAndTheLatch(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{".copier-answers.yml": answersLatched}))
	out := runAtom(t, "fleet:copier-answers-intact", "").Reason
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

// ---- absentRuleset, via both atoms that call it ----

// A tree nobody stamped may legitimately ship no SAST. This is the case the old
// unconditional exit 0 was right about, and it stays right.
func TestMissingRulesetStillPassesWhenNoTemplateStampedTheTree(t *testing.T) {
	for _, id := range []string{"fleet:opengrep-sast", "fleet:sast-ruleset-lanes"} {
		engine.reset()
		engine.withTree(fleetTree(nil, "rules/sast/go.yml", ".copier-answers.yml"))
		wantState(t, runAtom(t, id, ""), 0, "ABSENT", "no fleet template stamped it")
		fleetNoContainer(t, "absence decided from the Directory")
	}
}

// THE HOLE THIS CLOSES. stellar-core-rust and vault-mcp were both stamped from a
// fleet template, both had no rules/ directory at all, and both gates were
// green — 0 findings from 0 files, which is an unexamined repo rather than a
// clean one.
func TestMissingRulesetRefusesWhenAFleetTemplateStampedTheTree(t *testing.T) {
	for _, id := range []string{"fleet:opengrep-sast", "fleet:sast-ruleset-lanes"} {
		engine.reset()
		engine.withTree(fleetTree(
			map[string]string{".copier-answers.yml": answersClean},
			"rules/sast/go.yml",
		))
		v := runAtom(t, id, "")
		wantState(t, v, 2, "CANNOT RUN", "go-repo-template", "never received the file")
		if !strings.Contains(v.Reason, "rules/sast/dataflow.yml") {
			t.Errorf("finding does not name the file to copy:\n%s", v.Reason)
		}
	}
}

// An answers file pointing somewhere that is not one of our repo templates —
// speckit ships its own — must not make the SAST atom stricter. The question is
// "did a template that SHIPS a ruleset stamp this", not "is copier involved".
func TestMissingRulesetIgnoresANonTemplateAnswersFile(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(
		map[string]string{".copier-answers.yml": "_src_path: https://forgejo.notusmi.com/rob/speckit-overrides.git\n"},
		"rules/sast/go.yml",
	))
	wantState(t, runAtom(t, "fleet:opengrep-sast", ""), 0, "no fleet template stamped it")
}

// config-repo-template ships no ruleset (a config tree has no code language), so
// a tree it stamped with no rules/ is ABSENT and passes. Closed set, per kind.
func TestMissingRulesetPassesForAConfigTemplateStamp(t *testing.T) {
	for _, id := range []string{"fleet:opengrep-sast", "fleet:sast-ruleset-lanes"} {
		engine.reset()
		engine.withTree(fleetTree(
			map[string]string{".copier-answers.yml": "_src_path: https://git.notusmi.com/config-repo-template.git\n"},
			"rules/sast/go.yml",
		))
		wantState(t, runAtom(t, id, ""), 0, "ABSENT")
	}
}

// A ruleset that IS present is unaffected by any of this — the helper is only
// consulted on absence, so a stamped repo with its ruleset runs the real scan.
func TestAPresentRulesetIsNotTouchedByTheStampCheck(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{".copier-answers.yml": answersClean}))
	if v := runAtom(t, "fleet:sast-ruleset-lanes", ""); v.State == 2 {
		t.Errorf("a present ruleset was refused: %s", v.Reason)
	}
}

// ---- the CANNOT RUN paths ----
//
// EVERY TEST BELOW EXISTS BECAUSE THE MUTATION GATE ASKED FOR IT, and each one
// was a real gap rather than a tax. The first run of this file killed 27 of 33
// viable mutants and left six, all here: two `if err != nil` branches nothing
// reached, one `return` NOT COVERED outright, and an arithmetic mutant in the
// template-name slice that lived because the assertion below used to be a
// substring loose enough to accept a leading slash. A survivor is a hypothesis,
// not a verdict — but these six were all the same verdict, and it was right.

// The answers file exists and will not read. Guessing it is clean is exactly how
// the marker got six weeks of cover, so this must refuse rather than pass.
func TestCopierAnswersIntactRefusesAFileItCannotRead(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{".copier-answers.yml": answersClean}))
	engine.fail(`file(path:".copier-answers.yml")`, "i/o error")
	wantState(t, runAtom(t, "fleet:copier-answers-intact", ""), 2,
		"CANNOT RUN", "would not read", "i/o error")
}

// A tree that will not enumerate is not a tree with no answers file.
func TestCopierAnswersIntactRefusesATreeItCannotEnumerate(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail("{directory{entries}}", "the tree went away")
	wantState(t, runAtom(t, "fleet:copier-answers-intact", ""), 2,
		"CANNOT RUN", "would not enumerate", "the tree went away")
}

// THE ASYMMETRY IS DELIBERATE AND THIS IS WHERE IT IS PINNED. The atom above
// REFUSES an unreadable answers file; copierTemplate FAILS OPEN on the same
// file, so a missing ruleset stays a pass rather than becoming a refusal. A
// helper that decides whether to make another atom stricter must not invent
// strictness from a failed read — the loud complaint is the other atom's job,
// and it is already making it on the same tree.
func TestMissingRulesetPassesWhenTheAnswersFileWillNotRead(t *testing.T) {
	for _, id := range []string{"fleet:opengrep-sast", "fleet:sast-ruleset-lanes"} {
		engine.reset()
		engine.withTree(fleetTree(
			map[string]string{".copier-answers.yml": answersClean},
			"rules/sast/go.yml",
		))
		engine.fail(`file(path:".copier-answers.yml")`, "i/o error")
		wantState(t, runAtom(t, id, ""), 0, "no fleet template stamped it")
	}
}

// The template NAME is sliced out of `_src_path`, and the slice arithmetic has
// to be exact: an off-by-one leaves "/go-repo-template", which still ends in
// "-repo-template" and still reads as a match. The finding would then tell
// someone to copy "/go-repo-template's" ruleset. Asserting the possessive with
// no leading slash is what pins the index.
func TestTheTemplateNameIsSlicedExactly(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(
		map[string]string{".copier-answers.yml": answersClean},
		"rules/sast/go.yml",
	))
	out := runAtom(t, "fleet:opengrep-sast", "").Reason
	if !strings.Contains(out, "copy go-repo-template's template/rules/sast/dataflow.yml") {
		t.Errorf("the template name is not sliced cleanly:\n%s", out)
	}
	if strings.Contains(out, "/go-repo-template's") {
		t.Errorf("the template name kept its leading slash:\n%s", out)
	}
}
