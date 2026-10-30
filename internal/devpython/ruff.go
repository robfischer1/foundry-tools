package devpython

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/rulesets"
)

// Ruff runs `ruff <sub>` (check or format) over the tree under THE FLEET'S
// RULESET, as the gate twins do (python:ruff-check, python:ruff-format): the
// embedded internal/checks/rulesets/ruff.toml is written under ATOM_WORK and
// handed over with --config, which makes ruff ignore the tree's own
// pyproject.toml and ruff.toml. A dev answer is then the gate's answer for the
// same files, instead of the answer under whatever a star's copy of the ruleset
// has drifted to. The width is the fleet's one, on the command line as well as
// in the ruleset, so the argv names it.
//
// What stays the verb's own: the scope. With no path ruff works on the working
// directory and a caller's paths replace it (the gate twins pick their own: `.`
// and, for format, src and tests); the caller's other options follow the head.
//
// cache, when set, is where ruff keeps its cache: it writes it beside the
// sources, and /src is read-only for a verb that does not write the tree.
func Ruff(ctx context.Context, in atomkit.In, row checks.AtomDef, sub, cache string) checks.Verdict {
	cfg, err := atoms.WriteRuleset("ruff.toml", rulesets.Ruff)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), row.ID+": CANNOT RUN - the fleet's ruleset could not be written: "+err.Error())
	}
	if bad := atoms.Probe(ctx, in, row, "ruff", "--version"); bad != nil {
		return *bad
	}
	args := append([]string{sub, "--config", cfg, "--line-length", checks.RuffLineLength}, in.Args...)
	var env []string
	if cache != "" {
		env = []string{"RUFF_CACHE_DIR=" + cache}
	}
	return atoms.SettleTool(ctx, in, row, atoms.Cmd{Dir: in.Root, Name: "ruff", Args: args, Env: env})
}
