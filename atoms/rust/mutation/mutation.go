// Package rustmutation is the atom rust:mutation: every viable mutant
// cargo-mutants makes of this pull's changes to the declared critical modules is
// killed by the tests. checks/rustmutation*.go carries the judgement; this
// package asks the questions of the tree and runs the tools.
package rustmutation

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It judges the change, so its environment hands it the base.
// cargo-mutants and the nextest it drives are tools of its own: only this atom
// runs them, so no other rust atom waits on, or pays for, either.
//
// THE DEADLINE IS THE LANE'S, as go:mutation's (75 minutes).
var Def = atomkit.Def{
	ID: "rust:mutation", Stage: checks.StageMutation, Lane: checks.LaneRust,
	Group:      checks.GroupLanguage,
	Desc:       "Every viable mutant cargo-mutants makes of this pull's changes to the declared critical modules is killed by the tests.",
	Surface:    "root:Cargo.toml",
	Toolchain:  "rust",
	Needs:      []string{"tool:cargo-mutants", "tool:cargo-nextest"},
	NotOnForks: checks.ForkChangeBar,
	History:    "full",
	Timeout:    75 * time.Minute,
	Check:      check,
}

const id = "rust:mutation"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StageMutation, Lane: checks.LaneRust}

// noBase is the stand-down for a run with no change set.
const noBase = "no usable PR base sha — the diff-scoped mutation gate did not run"

// check runs cargo-mutants over the critical modules this pull added lines to.
//
// THE HISTORY IS THERE IN THE LANE THAT MATTERS. The mutation Job clones the
// repository whole, so the base resolves and the diff is real. A local pre-push
// hands the engine a linked worktree, which the adapter turns into a throwaway
// repository with no history: the base does not resolve, the atom stands down 0
// saying so, and the door's Job is the one that measures.
//
// THE CHANGE SET IS KNOWN BEFORE ANYTHING IS PROVISIONED. The merge base and the
// diff are git questions; only a pull that adds lines to a critical module gets
// the mutation tools and the dependency fetch. MEASURED 2026-10-09: of 107
// mutation runs, 20 graded nothing and paid ~110 CPU-s each provisioning for it,
// and a cerberus run spent 995 CPU-s building cargo-mutants and cargo-nextest to
// mutate nothing.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	// critical_modules is read in Go off the tree. A missing answers file is an
	// empty declaration.
	answers, _ := atoms.ReadIfPresent(in.Root, ".copier-answers.yml")
	mods := checks.CriticalModules(answers)
	g := &grading{in: in, mods: mods, scope: checks.MutationScope(id, mods)}
	return g.grade(ctx)
}

// workingTree is atoms.WorkingTree, a variable so a test can say the copy fails.
var workingTree = atoms.WorkingTree

// grading is one ask of the atom.
type grading struct {
	in    atomkit.In
	mods  string
	scope string
	work  string
	// tree is where cargo builds and cargo-mutants copies from.
	tree string
}

// settle files a verdict, its scope line first.
func (g *grading) settle(state int, reason string) checks.Verdict {
	v := checks.VerdictOf(row, state, id+": "+reason)
	v.Reason = g.scope + "\n" + v.Reason
	return v
}

// neverRan is the verdict for a program that did not run.
func (g *grading) neverRan(c atoms.Cmd, code int, out string) checks.Verdict {
	return g.settle(2, "CANNOT RUN - "+atoms.NeverRanReason(append([]string{c.Name}, c.Args...), code, out))
}

// unset is what no program of this atom may inherit: the shared target the
// executor points cargo at (see mutate).
var unset = []string{"CARGO_TARGET_DIR"}

// run starts a program in dir through the input's seam.
func (g *grading) run(ctx context.Context, dir, name string, args ...string) (string, int) {
	return atoms.RunCmd(ctx, g.in, atoms.Cmd{Dir: dir, Name: name, Args: args, Unset: unset})
}

func (g *grading) grade(ctx context.Context) checks.Verdict {
	in := g.in
	if in.Base == "" {
		return g.settle(0, noBase)
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
	// HEAD AS CHECKED OUT: cargo-mutants verifies the diff's `+` lines against the
	// files on disk and hard-errors when they disagree (exit 5), so the only safe
	// head side is the tree this run holds. --relative, because the paths are
	// matched against the tree cargo runs in.
	specs := append([]string{"--"}, checks.RustMutationSpecs(g.mods)...)
	diffCmd := atoms.Cmd{Dir: in.Root, Name: "git", Args: append([]string{"diff", "--relative", since, "HEAD"}, specs...)}
	diff, code := atoms.RunCmd(ctx, in, diffCmd)
	if !atoms.ToolRan(code) {
		return g.neverRan(diffCmd, code, diff)
	}
	if code != 0 {
		return g.settle(2, "CANNOT RUN - git could not diff the pull against its base "+since+": "+diff)
	}
	if diff == "" {
		return g.settle(0, "this pull touched none of the critical modules — nothing to mutate")
	}
	if !checks.DiffAddsLines(diff) {
		return g.settle(0, "this pull only REMOVED lines from the critical modules — nothing to mutate")
	}
	return g.mutate(ctx, since, diff, specs)
}

// mutate is the run once something will be mutated: the dependencies fetched,
// the workspace members the pull touched named, cargo-mutants run.
func (g *grading) mutate(ctx context.Context, since, diff string, specs []string) checks.Verdict {
	in := g.in
	g.work = filepath.Join(atoms.WorkDir(), "rust")
	tree, _, err := workingTree(in.Root, g.work)
	if err != nil {
		return g.settle(2, "CANNOT RUN - the tree could not be copied to build in: "+err.Error())
	}
	g.tree = tree
	// cargo-mutants copies the tree once per job and mutates each copy; every
	// copy and the gate's own cargo-test built into one CARGO_TARGET_DIR share an
	// artifact hash, and a copy whose own source changed reads fresh and runs a
	// sibling's binary (foundry-tools#8869). So the lane compiles into target
	// dirs of its own: the variable the executor sets for its atoms is dropped
	// for every program this atom starts (unset).

	// Provisioning, under the default Expect: a registry outage is the atom's
	// could-not-run, never a finding the committer is told to fix.
	for _, step := range [][]string{{"cargo", "fetch", "--locked"}, {"cargo", "mutants", "--version"}} {
		if out, code := g.run(ctx, g.tree, step[0], step[1:]...); code != 0 {
			return g.settle(2, "CANNOT RUN - preflight `"+strings.Join(step, " ")+"` exited "+strconv.Itoa(code)+": "+atoms.LastLine(out))
		}
	}
	// EVERY WORKSPACE MEMBER THE PULL TOUCHED, each passed as -p
	// (checks.RustTouchedMembers). A metadata read that fails is could-not-run,
	// never a run over the root package alone.
	//
	// --no-renames, and no --diff-filter=d: a file moved from crate A to crate B
	// lists only B's path under default rename detection, and A, which lost it and
	// may no longer compile, would go unmutated. Without renames the move is a
	// deletion in A plus an addition in B, and the deletion must stay listed so A
	// is named (RustTouchedMembers maps by path; the file need not exist).
	filesCmd := atoms.Cmd{Dir: in.Root, Name: "git", Args: append([]string{"diff", "--relative", "--no-renames", "--name-only", "-z", since, "HEAD"}, specs...)}
	files, code := atoms.RunCmd(ctx, in, filesCmd)
	if !atoms.ToolRan(code) {
		return g.neverRan(filesCmd, code, files)
	}
	if code != 0 {
		return g.settle(2, "CANNOT RUN - git could not list the files the pull changed: "+files)
	}
	metaCmd := atoms.Cmd{Dir: g.tree, Name: "cargo", Args: []string{"metadata", "--no-deps", "--format-version", "1"}, Unset: unset}
	meta, code := atoms.RunCmd(ctx, in, metaCmd)
	if !atoms.ToolRan(code) {
		return g.neverRan(metaCmd, code, meta)
	}
	if code != 0 {
		return g.settle(2, "CANNOT RUN - cargo metadata failed, so which workspace members the pull touched is unknown: "+meta)
	}
	packages, err := checks.RustTouchedMembers([]byte(meta), g.tree, strings.Split(files, "\x00"))
	if err != nil {
		return g.settle(2, "CANNOT RUN - could not map the diff onto the workspace members: "+err.Error())
	}
	// THE KEYS of the units the diff touched, for the run's gradings. Reading them
	// cannot change the verdict (keys.go).
	config, _ := atoms.ReadIfPresent(g.tree, ".cargo/mutants.toml")
	keys, owner := g.gradingKeys(ctx, diff, files, meta, config)
	return g.measure(ctx, diff, packages, keys, owner)
}
