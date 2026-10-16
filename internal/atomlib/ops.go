// Package atomlib is what the ported ops, orbit, compose and template atoms
// (atoms/<ns>/<name>) share: the stand-downs, the tool probe, the settle rules
// and the private tree. A helper two atoms use lives here, once; an atom's own
// question is in its own file.
package atomlib

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// THE OPS ATOMS stand down ABSENT on a tree that is not an ops tree (flux/,
// ansible/, a chezmoi source, a compose spec, a rego policy, one of infra's
// tools): running them over every star would be a new gate turned on across the
// fleet at once. A tree the atom has no question about is ABSENT before any tool
// is touched, so an absent atom runs no program at all.

// OpsSurface is the stand-down every ops atom shares, or nil when the tree is an
// ops tree. A scan that failed is a 2, not an absence the repository never
// declared.
func OpsSurface(a checks.AtomDef, in atoms.Input) *checks.Verdict {
	var v checks.Verdict
	switch {
	case in.FilesErr != nil:
		v = checks.CannotEnumerate(a, in.FilesErr)
	case checks.IsOpsTree(in.Files):
		return nil
	default:
		v = checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - this repository has no ops shape (no flux/, ansible/, chezmoi source, compose spec, rego policy or infra tool), so the ops lane does not gate it. A star is gated by its language lane.")
	}
	return &v
}

// PhaseAbsent is a phase that found its facet missing, in the chain's words.
func PhaseAbsent(a checks.AtomDef, why string) checks.Verdict {
	return checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - "+why)
}

// NeverRan is a program that would not start, or an engine-side failure the
// chains filed as "the atom never ran": a 2, with the reason.
func NeverRan(a checks.AtomDef, why string) checks.Verdict {
	return checks.VerdictOf(a, int(checks.StateCannotRun), "the atom never ran: "+why)
}

// Unprovisioned is the verdict for a tool that did not pass its version probe.
func Unprovisioned(a checks.AtomDef, why string) checks.Verdict {
	return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the phase's tool could not be provisioned: %s", a.ID, why))
}

// ProbeTool runs the tool's version probe and answers a verdict when it did not
// pass: the chain's provisioning exec, which carried the default Expect.
func ProbeTool(ctx context.Context, a checks.AtomDef, in atoms.Input, name string, args ...string) *checks.Verdict {
	out, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: name, Args: args, Both: true})
	if code == 0 {
		return nil
	}
	v := Unprovisioned(a, fmt.Sprintf("%s %s exited %d: %s", name, strings.Join(args, " "), code, strings.TrimSpace(out)))
	return &v
}

// OpsSettled is a phase's state and report from its tools' exit and output.
func OpsSettled(phase string, rc int, out string) (int, string) {
	state, line := checks.OpsSettle(phase, rc, out)
	if line != "" {
		out += "\n" + line
	}
	return state, out
}

// OpsSettledPython is OpsSettled for a python checker: an uncaught exception (a
// missing dependency, a crash) is could-not-run, not a finding.
func OpsSettledPython(phase string, rc int, out string) (int, string) {
	state, line := checks.OpsSettlePython(phase, rc, out)
	if line != "" {
		out += "\n" + line
	}
	return state, out
}
