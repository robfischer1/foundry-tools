// Package godiffcoverage is the atom go:diff-coverage: every line this pull
// changes in non-test, non-generated Go that holds code ran under the race
// suite. checks/godiffcoverage.go carries why it exists and what "uncovered"
// means; this file asks the questions of the container.
package godiffcoverage

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It judges the change, so its environment hands it the base
// and the fork record (atom_env.go). NeedsDies BECAUSE IT RUNS THE SUITE ITSELF:
// an atom answers one verdict, and the suite is go:test-race's own run in
// another container, so this atom runs the same `go test` on a server of its
// own (the profile cannot be handed from one atom's evaluation to another's).
var Def = atomkit.Def{
	ID: "go:diff-coverage", Stage: checks.StagePrepush, Lane: checks.LaneGo,
	Group:      checks.GroupLanguage,
	Desc:       "Every line this pull changes in non-test, non-generated Go that holds code ran under go:test-race's suite — NOT COVERED, read off its coverage profile.",
	Surface:    "any:**/go.mod",
	Toolchain:  "go",
	Needs:      []string{"dies", "service:postgres", "service:pgvector", "service:redpanda", "tool:gravity"},
	NotOnForks: checks.ForkChangeBar,
	Timeout:    45 * time.Minute,
	Check:      check,
}

const id = "go:diff-coverage"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneGo}

// excluded is the mutation lane's generated-Go exclusion, the same pattern over
// the same module-relative paths, so the two never disagree about which files
// are graded.
var excluded = regexp.MustCompile(checks.GoMutationExclude)

// profilePath is where the suite leaves its profile; a variable so a test can
// say where it is.
var profilePath = checks.GoCoverProfile

// noBase is the stand-down for a run with no change set.
const noBase = "ABSENT - no usable PR base sha, so there is no change set to hold the suite's coverage against"

// check holds each module's changed lines against its suite's profile. The
// findings are every module's, gathered here: a fold over modules keeps the
// states and the reasons, and a finding from a nested module is still one.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	var found []checks.Finding
	v := atoms.EachGoModule(in, row, func(dir string) checks.Verdict {
		mv := module(ctx, in, dir)
		found = append(found, mv.Findings...)
		return mv
	})
	v.Findings = found
	return v
}

// module is go:diff-coverage in one module. The change set is asked BEFORE the
// suite: a pull that changed no Go in this module waits for nothing.
//
// A SUITE THAT DID NOT PASS IS go:test-race's TO REPORT. This atom stands down
// ABSENT then — a failing run's profile measures a suite that did not finish,
// and a second red over the same cause is noise. It does so for a could-not-run
// too, rather than re-asking.
func module(ctx context.Context, in atomkit.In, dir string) checks.Verdict {
	settle := func(state int, reason string) checks.Verdict { return checks.VerdictOf(row, state, id+": "+reason) }
	if in.Base == "" {
		return settle(0, noBase)
	}
	cwd := atoms.GoModuleRoot(in, dir)
	since, err := atoms.ChangeBase(ctx, in, cwd)
	if err != nil {
		return settle(2, "CANNOT RUN - "+err.Error())
	}
	if since == "" {
		return settle(atoms.MissingBase(in, noBase))
	}
	run := func(ctx context.Context, c atoms.Cmd) (string, int) { return atoms.RunCmd(ctx, in, c) }
	diff, code := run(ctx, atoms.Cmd{Dir: cwd, Name: "git", Args: []string{"diff", "--unified=0", "--no-color", "--no-ext-diff", "--relative", since, "--", "*.go"}})
	if code != 0 {
		return settle(2, "CANNOT RUN - the pull could not be diffed against its base "+since+": "+readFailed("git diff", diff, code)+"\n"+diff)
	}
	files := checks.GoDiffCoverageFiles(diff, excluded)
	if len(files) == 0 {
		return settle(0, "ABSENT - this pull adds no line to a non-test, non-generated Go file of this module")
	}

	suite := atoms.GoSuite(ctx, in, atoms.GoRaceRow, dir, true)
	if suite.State != int(checks.StatePass) {
		return settle(0, "ABSENT - go:test-race did not pass ("+suite.Result+"); a suite that did not finish measures no coverage, and go:test-race reports it")
	}
	profile, err := os.ReadFile(profilePath)
	if err != nil {
		return settle(2, "CANNOT RUN - the suite passed and its coverage profile could not be read: "+err.Error())
	}
	pkgs, code := run(ctx, atoms.Cmd{Dir: cwd, Name: "go", Args: []string{"list", "-e", "-f", checks.GoPackagesFormat, "./..."}})
	if code != 0 {
		return settle(2, "CANNOT RUN - the module's packages could not be listed, so the profile cannot be placed: "+readFailed("go list", pkgs, code))
	}
	sources := map[string]string{}
	for _, f := range files {
		body, err := os.ReadFile(filepath.Join(in.Root, dir, f))
		if err != nil {
			return settle(2, "CANNOT RUN - a changed file could not be read from the tree: "+path.Join(dir, f))
		}
		sources[f] = string(body)
	}
	return checks.GoDiffCoverageVerdict(row, len(files), checks.GoUncoveredLines(checks.GoDiffCoverageInput{
		Dir: dir, Root: cwd, Diff: diff, Packages: pkgs, Profile: string(profile),
		Exclude: excluded, Sources: sources,
	}))
}

// readFailed words a read that did not answer: a program that would not start
// never ran, one that exited non-zero exited.
func readFailed(what, out string, code int) string {
	if code < 0 {
		return checks.ReadFailed(what, 0, errors.New(out))
	}
	return checks.ReadFailed(what, code, nil)
}
