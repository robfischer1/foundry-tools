package atoms

import (
	"context"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/govlaw"
)

// THE LAW LANES (Nomos F5b). Each atom hands the tree to internal/govlaw, which
// owns the judgement and is ABSENT on a tree that is not foundry-stocks. The
// tree is walked from the disk the way fleet:retired-verbs walks it, so a
// checkout git would not read is graded all the same.

// lawTree adapts the atoms' tree to govlaw's. An unwalkable tree is CANNOT RUN,
// never an absence: a lane that could not list the tree has not found it empty.
func lawTree(a checks.AtomDef, in Input) (govlaw.Tree, *checks.Verdict) {
	t := in.tree()
	paths, err := t.files()
	if err != nil {
		v := cannotEnumerate(a, err)
		return govlaw.Tree{}, &v
	}
	return govlaw.Tree{Paths: paths, Read: t.read}, nil
}

func lawVerdict(a checks.AtomDef, r govlaw.Result) checks.Verdict {
	return checks.VerdictOf(a, r.State, r.Report)
}

func runLaw(a checks.AtomDef, in Input, lane func(govlaw.Tree) govlaw.Result) checks.Verdict {
	t, stop := lawTree(a, in)
	if stop != nil {
		return *stop
	}
	return lawVerdict(a, lane(t))
}

// lawLint: blocks, kits and span maps are sound.
func lawLint(_ context.Context, a checks.AtomDef, in Input) checks.Verdict {
	return runLaw(a, in, govlaw.Lint)
}

// lawVerbLiveness: every verb the law names is served and none is retired.
func lawVerbLiveness(_ context.Context, a checks.AtomDef, in Input) checks.Verdict {
	return runLaw(a, in, govlaw.VerbLiveness)
}

// lawBudget: no render's always-on context outgrows its ceiling.
func lawBudget(_ context.Context, a checks.AtomDef, in Input) checks.Verdict {
	return runLaw(a, in, govlaw.Budget)
}
