// Package fleetspeckitrendercurrent is the atom fleet:speckit-render-current: the speckit
// template pin in .copier-answers.speckit.yml names the commit its files were rendered from.
// One file holds the atom, and both runners execute it (lld.md FN-085).
package fleetspeckitrendercurrent

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It runs in lead: the tree it reads is the pod's, and
// nothing else needs a toolchain.
var Def = atomkit.Def{
	ID: "fleet:speckit-render-current", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "The speckit pin in .copier-answers.speckit.yml is the commit its files were rendered from.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const (
	id = "fleet:speckit-render-current"
	// answersFile is the speckit template's answers file. Renovate's regex
	// manager rewrites its `_commit` and a postUpgrade `copier recopy` renders
	// the files; the template writes `_rendered` beside it, so copier alone can
	// move it.
	answersFile = ".copier-answers.speckit.yml"
)

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// valueOf is the value of a top-level `key: value` line, unquoted, and whether
// the key is there.
func valueOf(body, key string) (string, bool) {
	for _, line := range strings.Split(body, "\n") {
		if rest, ok := strings.CutPrefix(line, key+":"); ok {
			return strings.Trim(strings.TrimSpace(rest), `"'`), true
		}
	}
	return "", false
}

// speckitRenderCurrent: `_commit` and `_rendered` agree. EXIT 1, NOT 2: a pin
// that moved without its render is a finding about the tree, not a check that
// could not run. A file with no `_rendered` line predates the stamp and passes:
// its next recopy writes one.
func check(_ context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	entries, err := t.Entries(".")
	if err != nil {
		return checks.CannotEnumerate(row, err)
	}
	if !checks.HasEntry(entries, answersFile) {
		return checks.VerdictOf(row, int(checks.StatePass),
			"fleet:speckit-render-current: ABSENT - this tree carries no "+answersFile+", so it takes no speckit template")
	}
	body, err := t.Read(answersFile)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - %s would not read: %v", id, answersFile, err))
	}
	pin, hasPin := valueOf(body, "_commit")
	rendered, hasStamp := valueOf(body, "_rendered")
	if !hasPin || !hasStamp || pin == rendered {
		return checks.VerdictOf(row, int(checks.StatePass), "fleet:speckit-render-current: no pin without its render in "+answersFile)
	}
	return checks.VerdictOf(row, int(checks.StateFindings), fmt.Sprintf(
		"%s pins _commit %s but its files were rendered from %s.\n\n"+
			"The pin moved and the render did not: renovate's regex manager rewrites `_commit` and then\n"+
			"runs `copier recopy` to lay stages.just, dev.just and the hooks; when that command fails\n"+
			"(measured 2026-10-19, prometheus v0.231.0: 'Could not resolve host') the bump still opens\n"+
			"its pull, so the pin lands alone and the files stay at the old template.\n\n"+
			"FIX: run the render at the pinned commit, and commit what it changes:\n"+
			"  uvx --from copier==9.17.2 copier recopy --defaults --overwrite --trust --skip-tasks --vcs-ref %s --answers-file %s",
		answersFile, pin, rendered, pin, answersFile))
}
