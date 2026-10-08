package atoms

import (
	"context"
	"os"
	"path/filepath"
	"slices"

	"dagger/foundry-tools/internal/checks"
)

// THE OPS ATOMS THAT RUN THE TREE'S OWN CHECKER: ops:dup (infra's
// tools/dup-check), ops:declaration (infra's tools/declaration-integrity) and
// ops:metrics (flux's tools/metric-allowlist). They share the ops atoms' shape:
// the ops surface first, the checker's presence second (ABSENT before python is
// asked anything), the version probe third, the checker last.
//
// NONE OF THEM WRITES TO THE TREE. dup-check's one write is its ratchet baseline
// under --update-baseline, which the atom never passes (--blocking only reads);
// the other two read YAML. They run on the tree, with python's bytecode off.

// opsHasChecker answers whether the tree tracks one of its own checkers at
// tools/<name>, by the rule the chain applied (checks.OpsHasTool). A checker that
// runs as a program has to be tracked EXECUTABLE, which git records as mode
// 100755: the owner's execute bit, the one git itself reads.
func opsHasChecker(in Input, name string, executable bool) bool {
	rel := "tools/" + name
	if !slices.Contains(in.Committable, rel) {
		return false
	}
	mode := "100644"
	if fi, err := os.Lstat(filepath.Join(in.Root, rel)); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o100 != 0 {
		mode = "100755"
	}
	return checks.OpsHasTool([]checks.OpsFile{{Path: rel, Mode: mode}}, name, executable)
}

// opsChecker is one ops atom's checker: where it is, how python is named for it
// and what its phase is called in the report.
type opsChecker struct {
	phase string
	// tool is the file under tools/; executable says it must be tracked so.
	tool       string
	executable bool
	// python is the program that runs it, as the chain's argv named it.
	python string
	args   []string
	// two, when set, is the line appended when the tool exits 2: the tool grades
	// itself, and 2 is "could not read", could-not-run whatever its words were.
	two string
}

// opsRunChecker is the shared body: surface, presence, probe, run, settle.
func opsRunChecker(ctx context.Context, a checks.AtomDef, in Input, c opsChecker) checks.Verdict {
	if stop := opsSurface(a, in); stop != nil {
		return *stop
	}
	if !opsHasChecker(in, c.tool, c.executable) {
		return opsPhaseAbsent(a, "no tools/"+c.tool+" in this tree")
	}
	if stop := probeTool(ctx, a, in, c.python, "--version"); stop != nil {
		return *stop
	}
	out, code := in.run(ctx, Cmd{Dir: in.Root, Name: c.python, Args: append([]string{"tools/" + c.tool}, c.args...), Env: pythonEnv, Both: true})
	if code < 0 {
		return neverRan(a, out)
	}
	if code == 2 && c.two != "" {
		return checks.VerdictOf(a, int(checks.StateCannotRun), out+c.two)
	}
	state, report := opsSettled(c.phase, code, out)
	return checks.VerdictOf(a, state, report)
}

// opsDup: the repo's own tools/dup-check finds no blocking duplicate.
func opsDup(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	return opsRunChecker(ctx, a, in, opsChecker{phase: "dup", tool: "dup-check", executable: true, python: "python3", args: []string{"--blocking"}})
}

// opsDeclaration: the repo's own tools/declaration-integrity holds. Run through
// `python` and not `python3`, as the chain's argv named it; the venv has both.
func opsDeclaration(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	return opsRunChecker(ctx, a, in, opsChecker{phase: "declaration", tool: "declaration-integrity", python: "python"})
}

// opsMetrics: every metric a rule or dashboard reads is on a keep-list. Ingest is
// keep-lists, and a keep-list fails silently: a query against an omitted family
// reads "no data" forever. The tool grades itself: 2 is "could not read a
// keep-list or a source", could-not-run whatever its words were.
func opsMetrics(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	return opsRunChecker(ctx, a, in, opsChecker{
		phase: "metrics", tool: "metric-allowlist", python: "python",
		two: "\nmetrics: could not read a keep-list or a source — did not look",
	})
}
