// Package diesverbhints is the atom dies:verb-hints: the MCP tool annotation
// table (schema/verb-hints.json and its kit projection) is what the dies' own
// facts derive, every served verb has a row and no row is stale.
package diesverbhints

import (
	"context"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:verb-hints", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every served verb has a row in schema/verb-hints.json, no row is stale, and both that table and the kit projection schema/verb-annotations.json equal what the dies' facts derive (tools/verb_hints.py --check).",
	Surface:   "any:**",
	Toolchain: "python",
	Needs:     []string{"tool:uv"},
	Check:     check,
}

const id = "dies:verb-hints"

// checker is the tree's own program. It WRITES the table when run bare, so the
// atom never runs it without --check.
const checker = "tools/verb_hints.py"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// crashed is python's own mark of an uncaught exception. The script maps only
// OSError, ValueError and KeyError to its 2; any other exception leaves python
// to exit 1, which would read as a defect nobody found.
const crashed = "Traceback (most recent call last)"

// check runs `verb_hints.py --check` in the die's source. The script's ladder is
// the fleet's (0 holds, 1 a defect, 2 could not run), so its exit is the verdict
// except for a crash: python exits 1 on an uncaught exception, and that is a 2.
// Off the die's source (no policy/.manifest and fleet/repos/) it is ABSENT.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	if stop := dieskit.Shape(t, row); stop != nil {
		return *stop
	}
	if stop := atoms.RequirePaths(t, row, [][2]string{{checker, checker + " is absent, so there is no checker to run."}}); stop != nil {
		return *stop
	}
	v := dieskit.Verdict(ctx, row, in, "", checker, "--check")
	if v.State == int(checks.StateFindings) && strings.Contains(v.Reason+"\n"+strings.Join(v.Logs, "\n"), crashed) {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - "+checker+" crashed instead of grading the table, so nothing was checked.\n"+v.Reason)
	}
	return v
}
