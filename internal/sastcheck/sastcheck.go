// Package sastcheck is the scan fleet:opengrep-sast and dev:go-sast both run: the
// SAST scan, which refuses a zero-file scan. The two atoms hold a declaration
// each and call Check with their own row, so the verdict says the id that was
// asked and the logic exists once (lld.md FN-085).
package sastcheck

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// refusal is the zero-file refusal.
const refusal = "opengrep: REFUSING a zero-file scan. The ruleset matched no file in this repo, so 0 findings means NOTHING WAS EXAMINED - not that the code is clean. Check that rules/sast declares the language(s) this repo is actually written in."

// env is the tool's locale: opengrep decodes source files by it, and a
// process with none set reads UTF-8 bytes as ASCII.
var env = []string{"LANG=C.UTF-8", "LC_ALL=C.UTF-8"}

// writeRuleset is os.WriteFile, a variable so a test says the disk refused.
var writeRuleset = os.WriteFile

// Check is the SAST scan, which refuses a zero-file scan.
//
// THE REFUSAL IS THE POINT. "Ran N rules on 0 files: 0 findings" exits 0 and
// renders as Passed, indistinguishable from a clean scan; checks.OpengrepZeroFiles
// reads the summary line, from BOTH streams on every exit (the summary lands on
// whichever stream opengrep chose, and a zero-file scan exits zero).
//
// No `opengrep --version` probe: a program that would not start is the exec's
// own -1, a 2, which is what a failed provisioning layer was.
func Check(ctx context.Context, in atomkit.In, row checks.AtomDef) checks.Verdict {
	t := in.Tree()
	entries, err := t.Entries(".")
	if err != nil {
		return checks.CannotEnumerate(row, err)
	}
	if !checks.HasEntry(entries, "rules") {
		return noRules(ctx, in, row)
	}
	rulesEntries, err := t.Entries("rules")
	if err != nil {
		return checks.CannotEnumerate(row, err)
	}
	if !checks.HasEntry(rulesEntries, "sast") {
		return noRules(ctx, in, row)
	}
	out, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "opengrep", Args: []string{"scan", "--config", "rules/sast", "--error", "."}, Env: env, Both: true})
	if code < 0 {
		return atoms.NeverRan(row, nil, code, out)
	}
	out = strings.TrimSpace(out)
	if checks.OpengrepZeroFiles(out) {
		return checks.VerdictOf(row, int(checks.StateCannotRun), out+"\n"+refusal)
	}
	return checks.VerdictOf(row, code, out)
}

// noRules answers a tree with no rules/sast: a fork (Input.Fork) is scanned with
// the fleet's built-in rulesets, anything else gets the stamp-aware absence.
func noRules(ctx context.Context, in atomkit.In, row checks.AtomDef) checks.Verdict {
	if in.Fork == "" {
		return in.Tree().AbsentRuleset(row)
	}
	return scanFork(ctx, in, row)
}

// scanFork scans a fork with the built-in rulesets for the languages its files
// hold. Nothing is stamped into the fork's tree (fork-only files stay out of
// upstream PRs and syncs), and findings are advisory (checks.ForkSastVerdict).
// The rulesets are written to a scratch directory outside the tree, so the scan
// never reads its own rules.
func scanFork(ctx context.Context, in atomkit.In, row checks.AtomDef) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	sets := checks.ForkSastRulesets(in.Files)
	if len(sets) == 0 {
		return checks.VerdictOf(row, int(checks.StatePass), row.ID+": ABSENT - this fork holds no file in a language the fleet's built-in SAST rulesets cover")
	}
	dir, err := os.MkdirTemp(atoms.WorkDir(), "fork-sast-")
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), row.ID+": CANNOT RUN - the fleet's built-in rulesets could not be written for the run: "+err.Error())
	}
	defer func() { _ = os.RemoveAll(dir) }()
	for _, s := range sets {
		if err := writeRuleset(filepath.Join(dir, s.File), []byte(s.Body), 0o644); err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), row.ID+": CANNOT RUN - the fleet's built-in rulesets could not be written for the run: "+err.Error())
		}
	}
	out, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "opengrep", Args: []string{"scan", "--config", dir, "--error", "."}, Env: env, Both: true})
	if code < 0 {
		return atoms.NeverRan(row, nil, code, out)
	}
	return checks.ForkSastVerdict(row, in.Fork, sets, out, code)
}
