package main

import (
	"context"
	"path"
	"regexp"
	"sync"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE go:diff-coverage ATOM — EVERY LINE THIS PULL CHANGED RAN UNDER THE SUITE.
// checks/godiffcoverage.go carries why it exists and what "uncovered" means;
// this file is the chain.
//
// ITS OWN ATOM, READING go:test-race's CONTAINER. An atom answers one verdict,
// and the vector runs atoms concurrently, so the profile cannot be handed from
// one atom's evaluation to another's — but both atoms are given the SAME run,
// and the run keeps each module's suite (raceSuite): whichever atom asks first
// runs it, the other waits for that answer, and the suite runs once. A second
// verdict emitted from inside go:test-race was the alternative; it would have
// taught the vector, the registry and every reader that one runner can answer
// two ids, to save nothing the shared run does not already save.
//
// THE BASE NEVER REACHES THE SUITE (runtime.go rule 8). The diff is asked on a
// container of its own, the one the mutation lane asks it on; the suite's cache
// key stays a function of the tree, so go:test-race's caching is unchanged.
//
// A SUITE THAT DID NOT PASS IS go:test-race's TO REPORT. This atom stands down
// ABSENT then — a failing run's profile measures a suite that did not finish,
// and a second red over the same cause is noise. It does so for a could-not-run
// too, rather than re-asking: a re-asked coverage atom would rerun the suite
// beside go:test-race's own re-ask, and on a star with a database the two would
// share one server (bindTestDatabases says why that tears schemas apart).

func init() {
	register("go:diff-coverage", goDiffCoverage)
}

// goCoverageExcluded is the mutation lane's generated-Go exclusion, the same
// pattern over the same module-relative paths, so the two never disagree about
// which files are graded.
var goCoverageExcluded = regexp.MustCompile(goMutationExclude)

// raceRun is one module's go:test-race suite as the run keeps it.
type raceRun struct {
	once sync.Once
	v    checks.Verdict
	ctr  *dagger.Container
}

// raceSuite runs a module's go:test-race suite once per run and answers it to
// every atom that asks.
func (r *run) raceSuite(ctx context.Context, dir string) *raceRun {
	r.racesMu.Lock()
	if r.races == nil {
		r.races = map[string]*raceRun{}
	}
	s, ok := r.races[dir]
	if !ok {
		s = &raceRun{}
		r.races[dir] = s
	}
	r.racesMu.Unlock()
	s.once.Do(func() { s.v, s.ctr = goTestIn(ctx, r, checks.AtomByID("go:test-race"), dir, true) })
	return s
}

// goDiffCoverage holds each module's changed lines against its suite's profile.
// The findings are every module's, gathered here: a fold over modules keeps the
// states and the reasons, and a finding from a nested module is still one.
func goDiffCoverage(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("go:diff-coverage")
	var found []checks.Finding
	v := r.eachModule(ctx, a, func(dir string) checks.Verdict {
		mv := goDiffCoverageIn(ctx, r, a, dir)
		found = append(found, mv.Findings...)
		return mv
	})
	v.Findings = found
	return v
}

// goDiffCoverageNoBase is the stand-down for a run with no change set.
const goDiffCoverageNoBase = "ABSENT - no usable PR base sha, so there is no change set to hold the suite's coverage against"

// goDiffCoverageIn is go:diff-coverage in one module. The change set is asked
// BEFORE the suite: a pull that changed no Go in this module waits for nothing.
func goDiffCoverageIn(ctx context.Context, r *run, a checks.AtomDef, dir string) checks.Verdict {
	settle := func(state int, reason string) checks.Verdict { return checks.VerdictOf(a, state, a.ID+": "+reason) }
	if r.base == "" {
		return settle(0, goDiffCoverageNoBase)
	}
	pre := inModule(r.gitReady(ctx, r.withBase(r.lane(checks.ImageGo))), dir)
	since, err := r.changeBase(ctx, pre)
	if err != nil {
		return settle(2, "CANNOT RUN - "+err.Error())
	}
	if since == "" {
		return settle(r.missingBase(goDiffCoverageNoBase))
	}
	diff, code, err := output(ctx, pre.WithExec([]string{"git", "diff", "--unified=0", "--no-color", "--no-ext-diff", "--relative", since, "--", "*.go"}, anyExit))
	if failed := checks.ReadFailed("git diff", code, err); failed != "" {
		return settle(2, "CANNOT RUN - the pull could not be diffed against its base "+since+": "+failed+"\n"+diff)
	}
	files := checks.GoDiffCoverageFiles(diff, goCoverageExcluded)
	if len(files) == 0 {
		return settle(0, "ABSENT - this pull adds no line to a non-test, non-generated Go file of this module")
	}

	suite := r.raceSuite(ctx, dir)
	if suite.v.State != int(checks.StatePass) {
		return settle(0, "ABSENT - go:test-race did not pass ("+suite.v.Result+"); a suite that did not finish measures no coverage, and go:test-race reports it")
	}
	profile, err := suite.ctr.File(checks.GoCoverProfile).Contents(ctx)
	if err != nil {
		return settle(2, "CANNOT RUN - the suite passed and its coverage profile could not be read: "+err.Error())
	}
	root := path.Join("/src", dir)
	pkgs, code, err := output(ctx, r.goModules(dir).WithExec([]string{"go", "list", "-e", "-f", checks.GoPackagesFormat, "./..."}, anyExit))
	if failed := checks.ReadFailed("go list", code, err); failed != "" {
		return settle(2, "CANNOT RUN - the module's packages could not be listed, so the profile cannot be placed: "+failed)
	}
	sources := map[string]string{}
	for _, f := range files {
		body, ok, _ := fileIfPresent(ctx, r.src, path.Join(dir, f))
		if !ok {
			return settle(2, "CANNOT RUN - a changed file could not be read from the tree: "+path.Join(dir, f))
		}
		sources[f] = body
	}
	return checks.GoDiffCoverageVerdict(a, len(files), checks.GoUncoveredLines(checks.GoDiffCoverageInput{
		Dir: dir, Root: root, Diff: diff, Packages: pkgs, Profile: profile,
		Exclude: goCoverageExcluded, Sources: sources,
	}))
}
