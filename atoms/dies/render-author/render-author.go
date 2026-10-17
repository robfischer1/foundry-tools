// Package diesrenderauthor is the atom dies:render-author: a pull themis made
// changes the two rendered grant documents and nothing else.
//
// themis's render loop (themis internal/rendergit) commits a render as
// themis <themis@pantheon.foundry> on a render/grants-tx-<tx> branch and opens
// the pull itself, so the click on a grant is the only step (Rob, 2026-10-16:
// "Clicking approve should be the singular step here, no separate hand edit
// and PR"). Its SVID is a git_edge pusher, and that row is unscoped: the door's
// input carries no repo or ref. This atom is the scope. A pull carrying a commit
// authored or committed as themis may touch only policy/grants/data.json and
// policy/principals/data.json; dies:grants-stamp, on the same gate, holds the
// documents to a render's stamp against the pull's merge base.
//
// The gate hands an atom no pusher or head ref, so the commit identity is the
// signal. It fails safe in the direction that matters: anyone can write the
// themis address on a commit, and doing so only narrows what their pull may land.
package diesrenderauthor

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:render-author", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "A pull with a commit authored or committed as themis (its render loop) changes only policy/grants/data.json and policy/principals/data.json; dies:grants-stamp holds those to a render's stamp against the merge base.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "dies:render-author"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

// Themis is the address themis's render loop commits as.
const Themis = "themis@pantheon.foundry"

// Rendered are the only paths a themis pull may change.
var Rendered = []string{"policy/grants/data.json", "policy/principals/data.json"}

// check reads the commits since the pull's merge base (the tip's parent with
// no base), and when any is themis's, the paths the pull changes.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	settle := func(state checks.State, reason string) checks.Verdict {
		return checks.VerdictOf(row, int(state), id+": "+reason)
	}
	if stop := dieskit.Shape(in.Tree(), row); stop != nil {
		return *stop
	}
	since, err := baseOf(ctx, in)
	if err != nil {
		return settle(checks.StateCannotRun, "CANNOT RUN - "+err.Error())
	}
	who, err := git(ctx, in, "log", "--no-merges", "--format=%ae%x00%ce", since+"..HEAD")
	if err != nil {
		return settle(checks.StateCannotRun, "CANNOT RUN - the commits since "+since+" would not list: "+err.Error())
	}
	if !themisIn(who) {
		return settle(checks.StatePass, "PASS - no commit since "+since+" is themis's, so the pull is not a render")
	}
	// Deleted and renamed paths are changes too: --no-renames lists both sides.
	diff, err := git(ctx, in, "diff", "-z", "--name-only", "--no-renames", since, "HEAD")
	if err != nil {
		return settle(checks.StateCannotRun, "CANNOT RUN - the paths changed since "+since+" would not list: "+err.Error())
	}
	var outside []string
	for _, p := range checks.SplitNul(diff) {
		if !slices.Contains(Rendered, p) {
			outside = append(outside, p)
		}
	}
	if len(outside) > 0 {
		return settle(checks.StateFindings, fmt.Sprintf(
			"FINDINGS - a pull with a commit by %s may change only %s; this one also changes:\n  %s",
			Themis, strings.Join(Rendered, " and "), strings.Join(outside, "\n  ")))
	}
	return settle(checks.StatePass, "PASS - a themis render since "+since+", changing only the rendered grant documents")
}

// themisIn says whether any author or committer of a `%ae%x00%ce` listing is themis.
func themisIn(listing string) bool {
	for _, line := range strings.Split(listing, "\n") {
		for _, addr := range strings.Split(line, "\x00") {
			if strings.EqualFold(strings.TrimSpace(addr), Themis) {
				return true
			}
		}
	}
	return false
}

// baseOf is the commit the pull branched from: the merge base of the run's base
// and the tip, or the tip's parent when the run names no base.
func baseOf(ctx context.Context, in atomkit.In) (string, error) {
	if in.Base == "" {
		return "HEAD~1", nil
	}
	out, err := git(ctx, in, "merge-base", in.Base, "HEAD")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("the base %s is not in this history: %v", in.Base, err)
	}
	return strings.TrimSpace(out), nil
}

// git runs one git command at the root; a non-zero exit is an error carrying its output.
func git(ctx context.Context, in atomkit.In, args ...string) (string, error) {
	out, code := in.Runner()(ctx, atoms.Cmd{Dir: in.Root, Name: "git", Args: append([]string{"-c", "safe.directory=*"}, args...)})
	if code != 0 {
		return out, fmt.Errorf("git %s exited %d: %.200s", args[0], code, strings.TrimSpace(out))
	}
	return out, nil
}
