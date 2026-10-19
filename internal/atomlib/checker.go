package atomlib

import (
	"context"
	"os"
	"path/filepath"
	"slices"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// THE OPS ATOMS THAT RUN THE TREE'S OWN CHECKER: ops:dup (infra's
// tools/dup-check), ops:declaration (infra's tools/declaration-integrity),
// ops:specs (infra's tools/console-specs), ops:metrics (flux's
// tools/metric-allowlist), ops:star-sa (flux's tools/star-sa-list) and
// ops:world-patches (flux's tools/world-patches). They share the ops atoms'
// shape: the ops surface first, the checker's presence second (ABSENT before
// python is asked anything), the version probe third, the checker last.
//
// NONE OF THEM WRITES TO THE TREE. dup-check's one write is its ratchet baseline
// under --update-baseline, which the atom never passes (--blocking only reads);
// the others read YAML. They run on the tree, with python's bytecode off.

// HasChecker answers whether the tree tracks one of its own checkers at
// tools/<name>, by the rule the chain applied (checks.OpsHasTool). A checker that
// runs as a program has to be tracked EXECUTABLE, which git records as mode
// 100755: the owner's execute bit, the one git itself reads.
func HasChecker(in atoms.Input, name string, executable bool) bool {
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

// Checker is one ops atom's checker: where it is, how python is named for it and
// what its phase is called in the report.
type Checker struct {
	Phase string
	// Tool is the file under tools/; Executable says it must be tracked so.
	Tool       string
	Executable bool
	// Python is the program that runs it, as the chain's argv named it.
	Python string
	Args   []string
	// Two, when set, is the line appended when the tool exits 2: the tool grades
	// itself, and 2 is "could not read", could-not-run whatever its words were.
	Two string
}

// RunChecker is the shared body: surface, presence, probe, run, settle.
func RunChecker(ctx context.Context, a checks.AtomDef, in atoms.Input, c Checker) checks.Verdict {
	if stop := OpsSurface(a, in); stop != nil {
		return *stop
	}
	if !HasChecker(in, c.Tool, c.Executable) {
		return PhaseAbsent(a, "no tools/"+c.Tool+" in this tree")
	}
	if stop := ProbeTool(ctx, a, in, c.Python, "--version"); stop != nil {
		return *stop
	}
	cmd := atoms.Cmd{Dir: in.Root, Name: c.Python, Args: append([]string{"tools/" + c.Tool}, c.Args...), Env: atoms.PythonEnv(), Both: true}
	run := func() (string, int) { return in.Run(ctx, cmd) }
	// Two is the mark of a checker that reads a source over the network.
	runIt := run
	if c.Two != "" {
		runIt = func() (string, int) { return runNet(run) }
	}
	out, code := runIt()
	if code < 0 {
		return NeverRan(a, out)
	}
	if code == 2 && c.Two != "" {
		return checks.VerdictOf(a, int(checks.StateCannotRun), out+c.Two)
	}
	state, report := OpsSettledPython(c.Phase, code, out)
	return checks.VerdictOf(a, state, report)
}
