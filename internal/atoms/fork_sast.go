package atoms

import (
	"context"
	"os"
	"path/filepath"

	"dagger/foundry-tools/internal/checks"
)

// noRulesSast answers fleet:opengrep-sast for a tree with no rules/sast: a
// fork (Input.Fork) is scanned with the fleet's built-in rulesets, anything
// else gets absentRuleset's answer.
func noRulesSast(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if in.Fork == "" {
		return absentRuleset(in.tree(), a)
	}
	return forkOpengrep(ctx, a, in)
}

// forkOpengrep is the chain's forkOpengrep (atoms_fork_sast.go): the same
// rulesets, the same argv, the same checks.ForkSastVerdict. The rulesets are
// written to a scratch directory outside the tree, so the scan never reads its
// own rules.
func forkOpengrep(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if in.FilesErr != nil {
		return cannotEnumerate(a, in.FilesErr)
	}
	sets := checks.ForkSastRulesets(in.Files)
	if len(sets) == 0 {
		return checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - this fork holds no file in a language the fleet's built-in SAST rulesets cover")
	}
	dir, err := os.MkdirTemp("", "fork-sast-")
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - the fleet's built-in rulesets could not be written for the run: "+err.Error())
	}
	defer func() { _ = os.RemoveAll(dir) }()
	for _, s := range sets {
		if err := os.WriteFile(filepath.Join(dir, s.File), []byte(s.Body), 0o644); err != nil {
			return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - the fleet's built-in rulesets could not be written for the run: "+err.Error())
		}
	}
	out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "opengrep", Args: []string{"scan", "--config", dir, "--error", "."}, Env: opengrepEnv, Both: true})
	if code < 0 {
		return neverRan(a, out)
	}
	return checks.ForkSastVerdict(a, in.Fork, sets, out, code)
}
