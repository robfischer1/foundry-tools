// Package tsmut is the atom ts:mutation: every mutant StrykerJS makes of
// this pull's changes to the declared critical modules is killed by the tests.
// checks/tsmutation.go carries the judgement; this package installs the tree and
// runs Stryker.
//
// THE MEASUREMENT IS PLAIN EXECS, SETTLED IN GO. foundry-stocks'
// ci/lib/mutation/ts.sh ran as five bash phases with four node helpers beside it;
// git, bun, curl and stryker run as their own programs and checks.TSMutationVerdict
// reads what they left.
//
// STRYKER RUNS UNDER NODE, NOT BUN. The package's own node_modules/.bin/stryker is
// exec'd, and its shebang picks node: `bunx --bun stryker` forces bun as the
// runtime, and Stryker's plugin loader does not survive it (the vitest runner
// fails to register after instrumenting every mutant). bun installs; node runs.
//
// THE LOCKFILE IS THE PIN. `bun install --frozen-lockfile`, never retried without
// the flag: an install that quietly resolved something else is the failure this
// gate exists to prevent. A failed install is diagnosed against the registry the
// lockfile names (checks.DiagnoseInstall).
package tsmut

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

const id = "ts:mutation"

// ID is the atom's identity, for the declaration in atoms/ts/mutation.
const ID = id

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StageMutation, Lane: checks.LaneTS}

// noBase is the stand-down for a run with no change set.
const noBase = "no usable PR base sha — the diff-scoped mutation gate did not run"

// The disk, as the atom touches it: variables so a test can say a write fails or
// a walk breaks.
var (
	walk       = filepath.WalkDir
	removeFile = os.Remove
	writeFile  = os.WriteFile
	readFile   = os.ReadFile
	linkHelper = atoms.LinkHelper
)

// grading is one ask of the atom.
type grading struct {
	in    atomkit.In
	mods  string
	scope string
	// runner is what the vitest-runner patch did, which rides on every verdict
	// this atom files from then on, beside the scope, for the reason
	// checks.ReportVitestRunners gives: a score is worth what the runner under it
	// is. Empty until the patcher has run, so a stand-down before the install
	// reads exactly as it did.
	runner string
	// tree is where bun installs and stryker runs.
	tree string
}

// settle files a verdict: the scope, the runner note, then the reason.
func (g *grading) settle(state int, reason string) checks.Verdict {
	v := checks.VerdictOf(row, state, id+": "+reason)
	v.Reason = g.scope + g.runner + "\n" + v.Reason
	return v
}

// step is one program's answer.
type step struct {
	out  string
	code int
	cmd  atoms.Cmd
}

// do runs a program, stdout then stderr, with the tool's own exit code.
func (g *grading) do(ctx context.Context, dir, name string, args ...string) step {
	return g.doWith(ctx, dir, nil, name, args...)
}

// doWith is do with environment added to the program's.
func (g *grading) doWith(ctx context.Context, dir string, env []string, name string, args ...string) step {
	c := atoms.Cmd{Dir: dir, Name: name, Args: args, Env: env, Both: true}
	out, code := atoms.RunCmd(ctx, g.in, c)
	return step{out: out, code: code, cmd: c}
}

// neverRan is the verdict for a program that did not run: it would not start, or
// it ended in the signal range.
func (g *grading) neverRan(s step) checks.Verdict {
	return g.settle(2, "CANNOT RUN - "+atoms.NeverRanReason(append([]string{s.cmd.Name}, s.cmd.Args...), s.code, s.out))
}

// Check mutates the declared critical modules.
func Check(ctx context.Context, in atomkit.In) checks.Verdict {
	// critical_modules is read in Go off the tree, before anything runs.
	answers, _ := atoms.ReadIfPresent(in.Root, ".copier-answers.yml")
	mods := checks.CriticalModules(answers)
	g := &grading{in: in, mods: mods, scope: checks.MutationScope(id, mods)}
	return g.grade(ctx)
}

func (g *grading) grade(ctx context.Context) checks.Verdict {
	in := g.in
	if in.Base == "" {
		return g.settle(0, noBase)
	}
	// Provisioning, under the default Expect: an image without bun is the atom's
	// could-not-run.
	if s := g.do(ctx, in.Root, "bun", "--version"); s.code != 0 {
		return g.neverRan(s)
	}
	// The change set starts at the merge base, not at the base the door named:
	// main's tip moves under an open pull.
	since, err := atoms.ChangeBase(ctx, in, in.Root)
	if err != nil {
		return g.settle(2, "CANNOT RUN - "+err.Error())
	}
	if since == "" {
		return g.settle(atoms.MissingBase(in, noBase))
	}
	// The diff is taken at the root and each range handed to the package that owns
	// it (checks.PlanStryker).
	s := g.do(ctx, in.Root, "git", append([]string{"diff", "--unified=0", since, "HEAD", "--"}, checks.TSMutationSpecs(g.mods)...)...)
	if !atoms.ToolRan(s.code) {
		return g.neverRan(s)
	}
	if s.code != 0 {
		return g.settle(2, "CANNOT RUN - git could not diff the pull against its base "+since+": "+strings.TrimSpace(s.out))
	}
	diff := strings.TrimSpace(s.out)
	if diff == "" {
		return g.settle(0, "this pull touched none of the critical modules — nothing to mutate")
	}
	ranges := checks.StrykerRanges(diff)
	if len(ranges) == 0 {
		return g.settle(0, "this pull only REMOVED lines from the critical modules — nothing to mutate")
	}
	if in.FilesErr != nil {
		return g.settle(2, "CANNOT RUN - could not enumerate the tree's Stryker configs: "+in.FilesErr.Error())
	}
	configs := atoms.Matching(in.Files, "**/*stryker.con*")
	plan, orphans := checks.PlanStryker(ranges, checks.StrykerConfigDirs(configs))
	// A range no config owns is a FINDING, not a could-not-run: a package this
	// pull changes that declares no mutation run is the committer's to set up, and
	// nothing with tests goes un-mutation-tested (Rob, 2026-09-11).
	if len(orphans) > 0 {
		return g.settle(1, "no Stryker config owns "+strings.Join(orphans[:min(len(orphans), 5)], ",")+
			" — every package this pull changes must carry its own stryker config and devDependencies; nothing with tests goes un-mutation-tested")
	}
	return g.install(ctx, plan, checks.StrykerConfigFiles(configs))
}

// work is the atom's directory.
func work() string { return filepath.Join(atoms.WorkDir(), "ts") }
