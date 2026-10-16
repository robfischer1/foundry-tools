// Package diescapabilitiesserved is the atom dies:capabilities-served: the capabilities the die declares are
// exactly the verbs the stars serve (Capabilities plan S3). It reads the tree in
// Go with no program; the judgement is checks.GradeCapabilitiesServed.
package diescapabilitiesserved

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:capabilities-served", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every star's declared capabilities (policy/capabilities data: prefix, verbs, tier) are exactly the verbs its code serves, from the served-verbs export committed under capabilities/served/: a served verb nobody declared is denied to every caller, and a declared verb nothing serves is a grant surface for a verb that is gone.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "dies:capabilities-served"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check hands the shared grader the tree. A read that fails is the grader's 2,
// not a missing file.
func check(_ context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	if stop := dieskit.Shape(t, row); stop != nil {
		return *stop
	}
	return checks.GradeCapabilitiesServed(row, checks.TreeReader{Read: t.Read, Glob: t.Glob})
}
