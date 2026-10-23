// Package godiffcoverage is the atom go:diff-coverage: every line this pull
// changes in non-test, non-generated Go that holds code ran under the race
// suite. checks/godiffcoverage.go carries why it exists and what "uncovered"
// means; this file asks the questions of the container.
package godiffcoverage

import (
	"context"
	"encoding/json"
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
// and the fork record (atom_env.go). NeedsDies BECAUSE IT CAN RUN THE SUITE
// ITSELF: an atom answers one verdict, and the suite is go:test-race's own run.
//
// IT FOLLOWS go:test-race ON ANY VERDICT (AfterAny). In the executor's pod the
// two share /work: the runner hands this atom its token once go:test-race has
// settled, and this atom reads the profile that run left (raceProfile) instead
// of running the suite a second time. A red go:test-race stands it down as
// excluded, the runner's own verdict or this atom's (raceRed).
//
// IT HOLDS NO SERVERS IN THE POD. The executor binds a service sidecar per
// consuming atom, so `service:*` Needs here were a second set of six servers
// (about 1.1Gi requested) for a suite this atom no longer runs there (proven on
// stellar-core gate 1484: 2.2 s, the profile read). In the pod a missing verdict
// or profile is therefore its could-not-run, never a suite run without servers
// (inPod). Off the executor (the Dagger adapter binds servers from the tree's
// tags, not from Needs; a workstation) it still runs the suite itself.
var Def = atomkit.Def{
	ID: "go:diff-coverage", Stage: checks.StagePrepush, Lane: checks.LaneGo,
	Group:      checks.GroupLanguage,
	Desc:       "Every line this pull changes in non-test, non-generated Go that holds code ran under go:test-race's suite — NOT COVERED, read off its coverage profile.",
	Surface:    "any:**/go.mod",
	Toolchain:  "go",
	Needs:      append(append(append([]string{"dies"}, checks.GravityNeeds()...), checks.WitSnapshotNeeds...), checks.SuiteJqNeeds...),
	NotOnForks: checks.ForkChangeBar,
	AfterAny:   []string{atoms.GoRaceRow.ID},
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

// noBase is the stand-down for a run with no change set.
const noBase = "ABSENT - no usable PR base sha, so there is no change set to hold the suite's coverage against"

// check holds each module's changed lines against its suite's profile. The
// findings are every module's, gathered here: a fold over modules keeps the
// states and the reasons, and a finding from a nested module is still one.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	race, settled := raceVerdict()
	if settled && raceRed(race) {
		return excludedAfter(race)
	}
	var found []checks.Finding
	v := atoms.EachGoModule(in, row, func(dir string) checks.Verdict {
		mv := module(ctx, in, dir, settled)
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
//
// settled is go:test-race having passed in this pod: its profile of the module
// is read where its run left it, and only where it left none does this atom run
// the suite itself.
func module(ctx context.Context, in atomkit.In, dir string, settled bool) checks.Verdict {
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

	var profile []byte
	if settled {
		profile = raceProfile(dir)
	}
	from := "the profile of go:test-race's run in this pod"
	if profile == nil && inPod() {
		// after_any hands this atom its token only once go:test-race has a
		// verdict, and a passing run leaves its profile: either is missing only
		// when something broke, and there are no servers here to measure with.
		return settle(2, "CANNOT RUN - go:test-race left no passing verdict and profile for this module in the pod, and this atom reads that profile rather than running the suite on servers it does not hold")
	}
	if profile == nil {
		suite := atoms.GoRaceSuiteAs(ctx, in, atoms.GoRaceRow, dir, id)
		if suite.State != int(checks.StatePass) {
			return settle(0, "ABSENT - go:test-race did not pass ("+suite.Result+"); a suite that did not finish measures no coverage, and go:test-race reports it")
		}
		var err error
		if profile, err = os.ReadFile(atoms.GoCoverProfile(id, dir)); err != nil {
			return settle(2, "CANNOT RUN - the suite passed and its coverage profile could not be read: "+err.Error())
		}
		from = "this atom's own run of the suite"
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
	v := checks.GoDiffCoverageVerdict(row, len(files), checks.GoUncoveredLines(checks.GoDiffCoverageInput{
		Dir: dir, Root: cwd, Diff: diff, Packages: pkgs, Profile: string(profile),
		Exclude: excluded, Sources: sources,
	}))
	v.Reason = "coverage: " + from + "\n" + v.Reason
	return v
}

// raceVerdict is go:test-race's verdict in this pod, beside this atom's own
// (ATOM_OUT, in the pod's shared /work/out). settled is false where there is
// none to read: no ATOM_OUT (the Dagger adapter, a workstation), no verdict yet
// (go:test-race still running, or a runner that does not order by after_any), or
// one that will not parse. Each of those is "measure it yourself", never a pass.
func raceVerdict() (race checks.Verdict, settled bool) {
	out := os.Getenv("ATOM_OUT")
	if out == "" {
		return checks.Verdict{}, false
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(out), atomkit.EscapeID(atoms.GoRaceRow.ID)+".json"))
	if err != nil {
		return checks.Verdict{}, false
	}
	if err := json.Unmarshal(b, &race); err != nil {
		return checks.Verdict{}, false
	}
	return race, true
}

// inPod is this atom running in the executor's pod, where the tree facts are
// handed over (ATOM_TREE, atomkit.LoadIn) and go:test-race's run is beside it.
// The Dagger adapter hands none.
func inPod() bool { return os.Getenv("ATOM_TREE") != "" }

// raceRed is go:test-race not having passed, as the runner reads it (daedalus
// lead tokens.failed): any state but 0, or a verdict that is itself excluded.
// An absence (state 0, absent) is not red.
func raceRed(race checks.Verdict) bool {
	return race.State != int(checks.StatePass) || race.Result == checks.VerdictExcluded
}

// excludedAfter is this atom stood down because go:test-race did not pass: what
// it reads (the suite's profile) does not exist, and go:test-race already
// carries the red. State 0 with result `excluded` and one `excluded` finding,
// the shape the runner writes itself when it stands an after_any atom down
// (daedalus wire.Excluded), so the lane folds to go:test-race's verdict and this
// line reads excluded, never holds and never could-not-run.
func excludedAfter(race checks.Verdict) checks.Verdict {
	pred := atoms.GoRaceRow.ID
	return checks.Verdict{
		Atom: id, Stage: row.Stage, Lane: string(row.Lane), State: int(checks.StatePass), Result: checks.VerdictExcluded,
		Reason: id + ": EXCLUDED - " + pred + " did not pass (" + race.Result + "), so the profile this reads does not exist; " + pred + " reports why",
		Logs:   []string{},
		Findings: []checks.Finding{{
			Verdict: checks.VerdictExcluded, Subject: id, Cause: "predecessor-red", Detail: pred + " did not pass", Probe: id,
		}},
	}
}

// raceProfile is go:test-race's profile of the module at dir, where its run in
// this pod left it (atoms.GoCoverProfile); nil when there is none or it is
// empty. A run removes the profile of a suite that did not pass, so a profile
// that is there is a passing run's.
func raceProfile(dir string) []byte {
	if p, err := os.ReadFile(atoms.GoCoverProfile(atoms.GoRaceRow.ID, dir)); err == nil && len(p) > 0 {
		return p
	}
	return nil
}

// readFailed words a read that did not answer: a program that would not start
// never ran, one that exited non-zero exited.
func readFailed(what, out string, code int) string {
	if code < 0 {
		return checks.ReadFailed(what, 0, errors.New(out))
	}
	return checks.ReadFailed(what, code, nil)
}
