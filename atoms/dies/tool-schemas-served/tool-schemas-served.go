// Package diestoolschemasserved is the atom dies:tool-schemas-served: the input
// schemas the stars serve are exactly the schema/tool-input files the die holds
// (F11 A1-P4, the twin of dies:capabilities-served). It reads the tree in Go with
// no program; the judgement is checks.GradeToolSchemasServed.
package diestoolschemasserved

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:tool-schemas-served", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every served verb's input schema (the served-inputs export committed under capabilities/served-inputs/) is the schema/tool-input file the die holds, once the die's root keys are dropped; every served verb has its tool-output file; no tool file is an orphan; the owed rows of tools/tool_schemas.lock.json are not stale. Served bytes are the source, so a file that drifts from them is the finding.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "dies:tool-schemas-served"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check hands the shared grader the tree. A read that fails is the grader's 2,
// not a missing file.
func check(_ context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	if stop := dieskit.Shape(t, row); stop != nil {
		return *stop
	}
	return checks.GradeToolSchemasServed(row, checks.TreeReader{Read: t.Read, Glob: t.Glob})
}
