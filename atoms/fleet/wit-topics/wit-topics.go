// Package fleetwittopics is the atom fleet:wit-topics: flux's redpanda/wit is the WIT of
// redpanda/schemas (stellar-core F2). One file holds the atom, and both runners execute it
// (lld.md FN-085).
package fleetwittopics

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. The checker is the tree's own python, run under uv with no
// package resolved, so it needs the python toolchain and uv.
var Def = atomkit.Def{
	ID: "fleet:wit-topics", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "flux's committed redpanda/wit is current with redpanda/schemas - a topic schema changed, added or removed without regenerating, or a hand edit of aiws-topics.wit, fails (stellar-core F2).",
	Surface:   "any:**",
	Toolchain: "python",
	Needs:     []string{"tool:uv"},
	Check:     check,
}

const id = "fleet:wit-topics"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// checker is flux's own gate for redpanda/wit (stellar-core F2).
const checker = "tools/wit-topics"

// pythonEnv keeps python from writing __pycache__ beside the scripts it imports:
// a checker that imports its sibling modules would otherwise leave tools/__pycache__
// in the tree it grades.
var pythonEnv = []string{"PYTHONDONTWRITEBYTECODE=1"}

// check runs flux's own checker over the tree.
//
// THE CHECKER IS THE TREE'S, NOT EMBEDDED. flux holds neither the generator nor
// a way to run it, so tools/wit-topics compares the digests the generator
// recorded in aiws-topics.report.json: a schema edited and not regenerated, a
// topic added or removed and a hand edit of aiws-topics.wit are one failure each.
// foundry-dies is NOT graded here: its dies:wit-regenerated regenerates from its
// own schema/ and reads no flux tree, so flux is the repo that goes red when the
// topic schemas part from their WIT.
//
// THE EXIT CODE IS THE VERDICT, unmapped: 0 current, 1 stale, 2 could not run.
// It runs through `uv run --no-project --quiet python3`, as the lane images are
// uv-managed and carry no bare python3, and asks for no packages, so a gate that
// only reads files cannot be broken by a registry.
//
// ABSENT ONLY ON A TREE THAT IS NOT FLUX's, decided before python is touched:
// prime/ is the fleet's flux tree and nothing else's. On that tree a missing
// checker is a CANNOT RUN naming it, never an absence: a gate that goes quiet
// when its script moves is how this one graded nothing.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	roots, err := t.Entries(".")
	if err != nil {
		return checks.CannotEnumerate(row, err)
	}
	if !checks.HasEntry(roots, checks.OureaConfigDir) {
		return checks.VerdictOf(row, int(checks.StatePass), id+
			": ABSENT - this tree carries no "+checks.OureaConfigDir+"/, so it is not the fleet's flux tree and holds no topic schemas to render")
	}
	if len(t.Glob(checker)) == 0 {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - "+checker+
			" is absent, so there is no checker to run and the topics' WIT is ungraded.")
	}
	if out, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "uv", Args: []string{"--version"}}); code != 0 {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("the atom never ran: uv --version exited %d: %s", code, out))
	}
	out, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "uv", Args: []string{"run", "--no-project", "--quiet", "python3", checker}, Env: pythonEnv, Both: true})
	if code < 0 {
		return atoms.NeverRan(row, nil, code, out)
	}
	return checks.VerdictOf(row, code, out)
}
