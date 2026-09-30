// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

func init() {
	register("fleet:copier-answers-intact", fleetCopierAnswersIntact)
}

// answersFile is copier's record of which template a repo was stamped from and
// at what version. Renovate's copier manager edits it, so it is also where that
// manager leaves evidence when it fails.
const answersFile = ".copier-answers.yml"

// copierSentinel is the line renovate's copier manager appends to the answers
// file. It is NOT a record that anything was updated, despite what it says.
//
// Renovate needs the answers file to look MODIFIED, because a modified package
// file is what triggers `updateArtifacts` — and `updateArtifacts` is the step
// that actually runs copier. So it appends this, then renders. A successful
// render rewrites the whole file and the line goes with it. The line survives
// exactly when copier CRASHED (renovatebot/renovate#36147, answered there by
// the manager's own author).
const copierSentinel = "#copier updated"

// THE MARKER IS A CRASH RECEIPT, AND IT LATCHES — which is why it is worth a
// gate atom rather than a lint.
//
// Once the line is COMMITTED, renovate adding the same line again is not a
// change it can detect, so `updateArtifacts` never fires and the repo takes NO
// further template update — not a modified file, not a new one. One crashed
// render disables template delivery for that repo permanently. Upstream has no
// fix; the sanctioned remedy is to remove the line by hand.
//
// MEASURED 2026-09-29/30: 49 of 55 copier-stamped repos in this fleet were
// latched this way, all 49 also missing the trailing newline the marker ate
// (which reds end-of-file-fixer for the next PR in each, on a file it never
// touched). They were latched by a SINGLE renovate cycle — the 00:17Z run on
// 2026-09-29, whose artifact step could not install copier at all because the
// container ran readOnlyRootFilesystem while containerbase writes its toolchain
// under /opt/containerbase (fixed in foundry/flux aca3fa1, 22:11Z the same day,
// ~22h too late for that cycle). Six weeks of template fixes reached nobody,
// and nothing said so.
//
// WHY NOTHING SAID SO IS THE WHOLE POINT. A PR titled "update dependency
// <template> to v0.36.0" whose entire diff is one comment line is green by
// every measure the gate had: the tree parses, nothing conflicts, no lane
// regressed. It is a truthful-looking claim to have done something it did not
// do, and it automerges. The only thing wrong with it is the marker, so the
// marker is what this reads.
//
// EXIT 1, NOT 2. This is a finding about the tree — a file carries something it
// should not — rather than a check that could not run. `git rm` is not the fix;
// deleting the marker line and restoring the trailing newline is, and then the
// next renovate cycle renders for real.
func fleetCopierAnswersIntact(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:copier-answers-intact")

	entries, err := r.src.Entries(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(entries, answersFile) {
		return checks.VerdictOf(a, 0,
			"fleet:copier-answers-intact: ABSENT - this tree carries no "+answersFile+
				", so it is not copier-stamped and has no render to crash")
	}

	body, err := r.src.File(answersFile).Contents(ctx)
	if err != nil {
		// A file the atom could not read is a file it did not examine, and
		// guessing it is clean is how the marker got six weeks of cover.
		return checks.VerdictOf(a, 2, fmt.Sprintf(
			"%s: CANNOT RUN - %s would not read: %v", a.ID, answersFile, err))
	}

	if !strings.Contains(body, copierSentinel) {
		return checks.VerdictOf(a, 0,
			"fleet:copier-answers-intact: no crash receipt in "+answersFile)
	}

	return checks.VerdictOf(a, 1, fmt.Sprintf(
		"%s carries renovate's %q marker.\n\n"+
			"That line is not a record of a template update. Renovate appends it to make\n"+
			"the file look modified so `updateArtifacts` fires, and `updateArtifacts` is\n"+
			"what actually runs copier; a successful render rewrites the file and the line\n"+
			"goes with it. Its survival means COPIER CRASHED, and the render this repo's\n"+
			"last copier PR claimed to perform did not happen.\n\n"+
			"It also LATCHES: re-adding an identical line is not a change renovate can\n"+
			"detect, so the artifact step never fires again and this repo takes no further\n"+
			"template update at all until the line is removed (renovatebot/renovate#36147,\n"+
			"no upstream fix; removing it by hand is the sanctioned remedy).\n\n"+
			"FIX: delete the marker line and any blank lines above it, and make sure the\n"+
			"file ends in a newline — the marker is appended without one, which separately\n"+
			"reds end-of-file-fixer for the next PR here. `_commit` and every answer value\n"+
			"stay as they are; copier itself moves the pin on the next real render.",
		answersFile, copierSentinel))
}

// copierTemplate names the template a tree was stamped from, or "" when the tree
// is not copier-stamped from one of the fleet's repo templates.
//
// It reads `_src_path` out of the answers file rather than trusting the file's
// mere presence: speckit ships its own `.copier-answers.speckit.yml` and a tree
// can carry an answers file pointing anywhere, so the question this answers is
// narrower than "is copier involved here" — it is "did one of OUR templates
// stamp this, and therefore ship this repo a SAST ruleset".
//
// An unreadable or unparseable file answers "" and the caller treats the tree as
// unstamped. That is deliberate: this helper decides whether to make another
// atom STRICTER, so a failure to read must not invent strictness out of nothing.
// The atom above is what fails loudly on a file it cannot read.
func copierTemplate(ctx context.Context, r *run, entries []string) string {
	if !checks.HasEntry(entries, answersFile) {
		return ""
	}
	body, err := r.src.File(answersFile).Contents(ctx)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(body, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "_src_path:")
		if !ok {
			continue
		}
		src := strings.Trim(strings.TrimSpace(rest), `"'`)
		name := strings.TrimSuffix(src[strings.LastIndex(src, "/")+1:], ".git")
		if strings.HasSuffix(name, "-repo-template") {
			return name
		}
		return ""
	}
	return ""
}

// absentRuleset answers for a tree with no rules/sast, and it is the whole fix
// for a hole this fleet has already paid for twice.
//
// IT USED TO BE VERDICT 0 UNCONDITIONALLY — "ABSENT - no rules/sast in this
// tree" — and verdict 0 is a PASS. So a repo whose template ships a ruleset, and
// which simply never received it, was scanned by nothing and reported clean.
// MEASURED 2026-09-29: stellar-core-rust and vault-mcp were in exactly that
// state, and neither had ever had a rules/ directory — their copier manager was
// latched (see above), so the file the template has shipped since 2026-06-19
// never arrived. Their gates were green throughout.
//
// That is the foundry-stocks#4415 incident class — "every Go star had a security
// gate that had never examined a single file" — except worse, because #4415 was
// a ruleset pointed at the wrong language and this was no ruleset at all, which
// the atoms answered with a pass rather than a refusal.
//
// SO ABSENCE IS NOW READ AGAINST INTENT. A tree nobody stamped may legitimately
// have no SAST and still passes; a tree one of our templates stamped is supposed
// to carry the ruleset that template ships, and its absence is a check that did
// not run rather than a repo with nothing to check. Exit 2, the same verdict
// this file's neighbours give a scan they could not perform.
func absentRuleset(ctx context.Context, r *run, a checks.AtomDef, entries []string) checks.Verdict {
	tpl := copierTemplate(ctx, r, entries)
	if tpl == "" {
		return checks.VerdictOf(a, 0, string(a.ID)+
			": ABSENT - no rules/sast in this tree, and no fleet template stamped it")
	}
	return checks.VerdictOf(a, 2, fmt.Sprintf(
		"%s: CANNOT RUN - this tree has no rules/sast, and %s stamped it.\n\n"+
			"Every fleet repo template ships rules/sast/dataflow.yml, so a stamped repo\n"+
			"without one did not decline SAST — it never received the file. `_skip_if_exists`\n"+
			"only skips a path that ALREADY exists, so a working `copier update` creates it;\n"+
			"a repo missing it has taken no successful render since the template began\n"+
			"shipping it. Check fleet:copier-answers-intact on this same tree.\n\n"+
			"NOTHING SCANNED THIS REPO. A pass here would mean 0 findings from 0 files, which\n"+
			"is not a clean scan — it is an unexamined repo, and it is why this is exit 2 and\n"+
			"not exit 0 (foundry-stocks#4415).\n\n"+
			"FIX: copy %s's template/rules/sast/dataflow.yml to rules/sast/dataflow.yml.",
		a.ID, tpl, tpl))
}
