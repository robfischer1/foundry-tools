// Package pythonruffcheck is the atom python:ruff-check: ruff lint is clean
// over every .py in the tree, under the fleet's ruleset.
package pythonruffcheck

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/rulesets"
)

// Def is the atom. ruff is installed once by the python lane at its pin
// (checks.RuffVersion), so the atom execs it by name.
var Def = atomkit.Def{
	ID: "python:ruff-check", Stage: checks.StagePrecommit, Lane: checks.LanePython,
	Group:      checks.GroupLanguage,
	Desc:       "ruff lint is clean over every .py in the tree, under the fleet's ruleset.",
	Surface:    "any:**/*.py",
	Toolchain:  "python",
	Needs:      []string{"tool:ruff"},
	NotOnForks: checks.ForkFleetStyle,
	Check:      check,
}

const id = "python:ruff-check"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LanePython}

// check runs ruff over the tree under the fleet's ruleset.
//
// THE RULESET IS THE FLEET'S. `--config <file>` makes ruff ignore every
// pyproject.toml and ruff.toml in the tree, so the repository's copy (the
// template pours one, and seven repos had drifted from it by 2026-09-11)
// decides nothing here. internal/checks/rulesets/ruff.toml is the template's
// ruleset with one home, embedded in this module and written beside the atom
// before ruff runs, so the tool and the rules it enforces ship as one artifact.
//
// The provisioning probe comes first: the pinned ruff is on PATH, and one that
// is not is a could-not-run rather than a finding.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	cfg, err := atoms.WriteRuleset("ruff.toml", rulesets.Ruff)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the fleet's ruleset could not be written: "+err.Error())
	}
	if bad := atoms.Probe(ctx, in, row, "ruff", "--version"); bad != nil {
		return *bad
	}
	return atoms.SettleTool(ctx, in, row, atoms.Cmd{Dir: in.Root, Name: "ruff", Args: []string{"check", "--config", cfg, "."}})
}
