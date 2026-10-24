// Package pythonruffformat is the atom python:ruff-format: ruff format --check
// is clean over the product python, under the fleet's ruleset.
package pythonruffformat

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/rulesets"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "python:ruff-format", Stage: checks.StagePrecommit, Lane: checks.LanePython,
	Group:      checks.GroupLanguage,
	Desc:       "ruff format --check is clean over the product python, under the fleet's ruleset.",
	Surface:    "any:**/*.py",
	Toolchain:  "python",
	Needs:      []string{"tool:ruff"},
	NotOnForks: checks.ForkFleetStyle,
	Check:      check,
}

const id = "python:ruff-format"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LanePython}

// check is `ruff format --check` over the product python, under the fleet's
// ruleset.
//
// --check, NEVER the rewrite. The stock hook reformats in place and fails so you
// re-stage, which has aborted a commit in this fleet before (themis). The path
// scope is the measurement the go stars' hook records: incidental python
// (scripts/, conformance recorders) is linted but not formatted; product python
// under src/ and tests/ is. checks.RuffFormatTargets decides that, from the
// root entries.
//
// The width is the fleet's one width (checks.RuffLineLength), passed on the
// command line as well as carried in the ruleset so the argv names it.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	entries, stop := atoms.PythonRootEntries(row, in)
	if stop != nil {
		return *stop
	}
	targets, ok := checks.RuffFormatTargets(entries)
	if !ok {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - a go star with no product python under src/ or tests/")
	}
	cfg, err := atoms.WriteRuleset("ruff.toml", rulesets.Ruff)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the fleet's ruleset could not be written: "+err.Error())
	}
	if bad := atoms.Probe(ctx, in, row, "ruff", "--version"); bad != nil {
		return *bad
	}
	args := append([]string{"format", "--config", cfg, "--line-length", checks.RuffLineLength, "--check"}, targets...)
	return atoms.SettleTool(ctx, in, row, atoms.Cmd{Dir: in.Root, Name: "ruff", Args: args})
}
