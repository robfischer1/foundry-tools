package atoms

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// THE GO LANE'S SHARED PARTS: what go:test, go:test-race, go:govulncheck and
// go:diff-coverage each need and no one of them owns — the module walk, the
// provisioning step in front of a module, the way a program that would not
// start is worded. They sit here, with the other helpers two or more atoms use
// (atoms/<ns>/<name> holds exactly one Def and what only that atom uses).

// GoRaceRow is go:test-race's identity. go:diff-coverage runs the same suite and
// names the atom whose suite it is, and an atom cannot look another's row up
// (the catalogue is not linked into its binary).
var GoRaceRow = checks.AtomDef{ID: "go:test-race", Stage: checks.StagePrepush, Lane: checks.LaneGo}

// goModuleDirs is the tree's Go modules off the gate's own population.
func goModuleDirs(in Input) ([]string, error) {
	if in.FilesErr != nil {
		return nil, in.FilesErr
	}
	return checks.GoModuleDirs(in.Files), nil
}

// EachGoModule runs one go atom in every module the tree carries and folds the
// answers (checks.FoldModules). A repository whose only module is its root gets
// that one verdict back untouched. Modules run one after another: they are not
// independent enough of each other's caches to race.
func EachGoModule(in Input, a checks.AtomDef, one func(dir string) checks.Verdict) checks.Verdict {
	dirs, err := goModuleDirs(in)
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - could not enumerate the tree's Go modules: "+err.Error())
	}
	if len(dirs) == 0 {
		return checks.AbsentVerdict(a)
	}
	if len(dirs) == 1 && dirs[0] == "." {
		return one(".")
	}
	mods := make([]checks.ModuleVerdict, 0, len(dirs))
	for _, dir := range dirs {
		mods = append(mods, checks.ModuleVerdict{Dir: dir, Verdict: one(dir)})
	}
	return checks.FoldModules(a, mods)
}

// GoModuleRoot is a module's directory on disk.
func GoModuleRoot(in Input, dir string) string { return filepath.Join(in.Root, dir) }

// GoNeverRan is the verdict for a program that would not start: the engine's
// "the atom never ran", which is the atom's could-not-run and never a pass.
func GoNeverRan(a checks.AtomDef, why string) checks.Verdict {
	return checks.VerdictOf(a, 2, "the atom never ran: "+why)
}

// GoPreflight runs one provisioning step in a module — `go mod download`, a
// tool's version probe — under the default Expect: a step that fails is the
// atom's could-not-run, never a finding about the tree. `go vet` and `go build`
// exit 1 for a module the proxy would not serve exactly as they exit 1 for a
// finding, and an atom that reads exit 1 as FINDINGS turns a proxy's bad hour
// into a terminal red the sweep will not re-ask (foundry-tools#29). ok is false
// with the verdict to return when the step failed.
func GoPreflight(ctx context.Context, in Input, a checks.AtomDef, cwd string, argv ...string) (v checks.Verdict, ok bool) {
	out, code := in.run(ctx, Cmd{Dir: cwd, Name: argv[0], Args: argv[1:]})
	if code == 0 {
		return checks.Verdict{}, true
	}
	return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - preflight `%s` exited %d: %s",
		a.ID, strings.Join(argv, " "), code, GoLastLine(out))), false
}

// GoLastLine is the tail of a tool's output: the line a human reads first.
func GoLastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
