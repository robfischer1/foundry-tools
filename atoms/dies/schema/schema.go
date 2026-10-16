// Package diesschema is the atom dies:schema: every catalog entry (fleet/repos/<name>.json)
// satisfies the fleet-star schema, and a tree that still carries fleet/stars/
// (retired slag) is refused: a stale writer, not a record to validate.
package diesschema

import (
	"context"
	"os"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
	"dagger/foundry-tools/internal/checks/scripts"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:schema", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every catalog entry fleet/repos/<name>.json satisfies schema/fleet-star.schema.json, and a tree that still carries fleet/stars/ (retired slag) is refused.",
	Surface:   "any:**",
	Toolchain: "python",
	Needs:     []string{"tool:uv"},
	Check:     check,
}

const id = "dies:schema"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check runs the embedded validator (checks/scripts/dies_schema.py), written to
// a file of this run's own for python to read.
//
// TWO ASSERTIONS ABOUT EACH SCHEMA, and the second is the one check_schema does
// not make. `required` naming a property that is not DEFINED is legal to the
// metaschema and, under additionalProperties false, makes the schema reject
// EVERY document. AND THE DOCUMENTS ARE VALIDATED: zero documents is a
// could-not-run, not a pass.
//
// THE PROVISION IS PROBED BEFORE THE GATE RUNS, uv and then an `import
// jsonschema` under the same resolver, so a resolver that could not reach an
// index is a 2 rather than a schema finding nobody made.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	if stop := dieskit.Shape(t, row); stop != nil {
		return *stop
	}
	if stop := atoms.RequirePaths(t, row, [][2]string{
		{"schema/fleet-star.schema.json", "schema/fleet-star.schema.json is absent, so there is no schema to validate the catalog entries against."},
	}); stop != nil {
		return *stop
	}
	if stop := dieskit.Probe(ctx, row, in); stop != nil {
		return *stop
	}
	if stop := dieskit.Imports(ctx, row, in, dieskit.WithJSONSchema, "jsonschema"); stop != nil {
		return *stop
	}
	script := atoms.TempPath("dies-schema", ".py")
	defer func() { _ = os.Remove(script) }()
	if err := os.WriteFile(script, []byte(scripts.DiesSchema), 0o600); err != nil {
		return atoms.NeverRan(row, nil, -1, "the validator could not be written for the run: "+err.Error())
	}
	return dieskit.Settle(ctx, row, in, dieskit.WithJSONSchema, script)
}
