package main

import (
	"context"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE GO LANE, AS CHAINS. go:vet and go:test-race are the exemplar every other
// lane's port follows; read runtime.go's rules first, then these two, then the
// rest of the lane below them.

func init() {
	register("go:gofmt", goGofmt)
	register("go:vet", goVet)
	register("go:build", goBuild)
	register("go:test-race", goTestRace)
	register("go:staticcheck", goStaticcheck)
	register("go:govulncheck", goGovulncheck)
	register("go:mutation", goMutation)
}

// goModules is the go lane's provisioned base: the lane container with the
// module graph downloaded. IT IS ONE EXEC, ON ITS OWN, so the engine caches it
// as a layer keyed on the tree — and with the module cache a volume, a tree
// whose go.sum did not move downloads nothing.
//
// The download is its own step under the default Expect for the reason the
// old script guarded it: `go vet` and `go build` exit 1 for a module the
// proxy would not serve exactly as they exit 1 for a finding, and an atom
// that reads exit 1 as FINDINGS turns a proxy's bad hour into a terminal red
// the sweep will not re-ask (measured 2026-09-11 17:26Z, foundry-tools#29:
// `proxy.golang.org …: 502 Bad Gateway` filed as a finding). Here a failed
// download is a failed exec — state 2, could not run, re-asked.
func (r *run) goModules() *dagger.Container {
	return r.lane(checks.ImageGo).
		WithExec([]string{"go", "mod", "download"})
}

// go vet ./... reports nothing.
func goVet(ctx context.Context, r *run) checks.Verdict {
	return verdict(ctx, checks.AtomByID("go:vet"),
		r.goModules().WithExec([]string{"go", "vet", "./..."}, anyExit))
}

// go test -race ./... passes, and there is something for it to pass.
//
// NO TESTS IS A FINDING. `go test ./...` prints "[no test files]" per package
// and exits 0, so a module with no test anywhere read green. Rob, 2026-09-11:
// nothing is built without tests. The count is go's own (TestGoFiles +
// XTestGoFiles per package), read in Go rather than in shell; a module where
// every package answers 0 is red before the suite runs.
//
// THE FLEET RECORD TREE RIDES ALONG. hephaestus's internal/slag goldens grade
// every record committed in foundry-dies; a lane checks out one repo, so
// without the mount they resolved nothing and SKIPPED, which `go test` prints
// as ok (#2453, #8118). A repo with no such test reads FOUNDRY_DIES and does
// nothing with it.
//
// AND THE RECORD'S DATABASE RIDES ALONG. A star whose slag record declares
// backends.postgres gets the fleet's test servers bound beside the lane and
// its DB-gated suites compiled (withTestDatabases); `-p 1` with them, because
// every package shares the one database and the suites reset its schema per
// test (chaos's own README says so of its lane).
func goTestRace(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("go:test-race")
	// THE SUITE RUNS IN A REPOSITORY git CAN READ. A test that shells out to
	// git — hephaestus's TestRealResolveHistory runs `git ls-remote` against a
	// fixture — fails on a linked worktree's dangling `.git` file with
	// "fatal: not a git repository: <primary>/.git/worktrees/<name>" before
	// it ever reaches the fixture, and the lane files FINDINGS for a test
	// that never got to look (measured 2026-09-14 on hephaestus, pre-push).
	// gitReady is the fix cargo-test and the mutation atoms already carry.
	mods := r.gitReady(ctx, r.withDies(r.goModules()))

	counts, code, err := output(ctx, mods.WithExec([]string{
		"go", "list", "-f", "{{len .TestGoFiles}}{{len .XTestGoFiles}}", "./...",
	}, anyExit))
	if err != nil {
		return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	if code != 0 {
		// A module that will not even list is a red about the module, and
		// the old atom filed it as FINDINGS on purpose: the tests cannot be
		// counted, so they cannot be shown to exist.
		return checks.VerdictOf(a, 1, "go:test-race: FINDINGS - go list ./... failed, so the tests cannot be counted: "+lastLine(counts))
	}
	if !checks.GoHasTestFiles(counts) {
		return checks.VerdictOf(a, 1, "go:test-race: FINDINGS - no test file in any package; nothing is built without tests")
	}
	mods, dbs, scope := r.withTestDatabases(ctx, mods)
	args := []string{"go", "test", "-race"}
	if len(dbs) > 0 {
		args = append(args, "-tags", checks.BuildTags(dbs), "-p", "1")
	}
	args = append(args, "./...")
	v := verdict(ctx, a, mods.WithExec(args, anyExit))
	v.Reason = scope + "\n" + v.Reason
	return v
}

// withTestDatabases binds the fleet's test servers to a lane container for
// the DB-gated suites the tree carries, when the star's RECORD says it has a
// Postgres — checks/testdb.go carries the reasoning and the vocabulary. It
// answers the container (bound or untouched), the databases bound, and the
// scope line the atom prints either way.
//
// Three reads decide it, none of them a knob: the star's name off the
// answers file (rule 3, in Go off the tree), its record off the mounted
// dies, and the tree's single-tag `//go:build` lines off one recursive grep
// in the lane — an enumeration, not a judgement; the judgement is
// checks.TestDBsFor. A missing name, a missing record or a record without
// the backend all answer "none", said in the scope line with the reason.
//
// THE SERVICE IS THE ENGINE'S, NOT A SCRIPT'S. Dagger starts a bound service
// just in time, health-checks its exposed port before the client runs, and
// stops it when nothing needs it — the fixture the retired star.toml
// provisioned through the Docker Engine API, without the socket.
func (r *run) withTestDatabases(ctx context.Context, ctr *dagger.Container) (*dagger.Container, []checks.TestDB, string) {
	answers, _ := r.src.File(".copier-answers.yml").Contents(ctx)
	star := checks.ServiceName(answers)
	if star == "" {
		return ctr, nil, checks.TestDBScope(nil, nil, "no service_name in .copier-answers.yml, so no record to read")
	}
	slag, err := r.dies.File("fleet/stars/" + star + "/slag.json").Contents(ctx)
	if err != nil {
		return ctr, nil, checks.TestDBScope(nil, nil, "no record at fleet/stars/"+star+"/slag.json")
	}
	if !checks.PostgresBackend(slag) {
		return ctr, nil, checks.TestDBScope(nil, nil, "the record declares no postgres backend")
	}
	// grep exits 1 for no match, which is an answer; 2 and up is grep failing.
	out, code, err := output(ctx, ctr.WithExec([]string{
		"grep", "-rhoE", `^//go:build [A-Za-z0-9_]+$`, "--include=*_test.go", ".",
	}, anyExit))
	if err != nil || code > 1 {
		return ctr, nil, checks.TestDBScope(nil, nil, "the tree's build tags could not be read")
	}
	tags := checks.GoBuildTags(out)
	dbs := checks.TestDBsFor(tags)
	if len(dbs) == 0 {
		return ctr, nil, checks.TestDBScope(nil, nil, "the record declares postgres but no test file sits behind a tag the fleet names")
	}
	for _, d := range dbs {
		svc := dag.Container().From(d.Image).
			WithEnvVariable("POSTGRES_USER", checks.TestDBRole).
			WithEnvVariable("POSTGRES_PASSWORD", checks.TestDBRole).
			WithEnvVariable("POSTGRES_DB", checks.TestDBName).
			WithExposedPort(5432).
			AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})
		ctr = ctr.WithServiceBinding(d.Alias, svc).WithEnvVariable(d.Env, d.DSN())
	}
	return ctr, dbs, checks.TestDBScope(dbs, checks.UncompiledTags(tags, dbs), "")
}

// Every Go file is gofmt-clean.
//
// THE POPULATION IS THE GATE'S, NOT A find(1) WALK. The old script listed
// `find . -path ./vendor -prune -o -name '*.go' -print`, which sweeps in every
// generated and gitignored .go a developer happens to have on disk — the same
// shape that turned fleet:check-added-large-files into 40+ findings under
// target/ on tongs (2026-09-09). r.population is the engine's own gitignore
// filter with the fleet exclude applied in Go, and that exclude already carries
// `(^|/)vendor/`, so the prune this script spelled by hand is the FLEET's rule
// here rather than this atom's private one.
//
// NO `go mod download`. gofmt parses and prints; it never resolves an import.
// So this atom starts from the bare lane rather than from goModules(), and a
// proxy's bad hour cannot reach it at all.
//
// GOFMT'S EXIT CODE IS NOT THE VERDICT — ITS STDOUT IS. `gofmt -l` exits 0
// whether it listed every file in the tree or none of them, so the answer has
// to be read off the listing. DELIBERATELY CHANGED: the old script read the
// listing and threw away BOTH the exit code and stderr (`2>/dev/null`), so a
// file gofmt could not parse listed nothing, said nothing, and rendered as
// "go:gofmt: clean". A file that was not examined is not a file that is
// formatted, so a non-zero gofmt is a 2 here.
func goGofmt(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("go:gofmt")

	files, err := r.population(ctx, "**/*.go")
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+err.Error())
	}
	if len(files) == 0 {
		return checks.VerdictOf(a, 0, "go:gofmt: no Go files")
	}

	ctr := r.lane(checks.ImageGo)
	args := append([]string{"gofmt", "-l"}, files...)
	if checks.NeedsArgFile(files) {
		// A list longer than one argv may carry goes in as a NUL-joined file
		// and xargs re-splits it. That is still one typed exec of one program;
		// it is not a shell, and rule 7 holds.
		ctr = ctr.WithNewFile(gofmtArgFile, strings.Join(files, "\x00"))
		args = []string{"xargs", "-0", "-a", gofmtArgFile, "gofmt", "-l"}
	}

	listed, code, err := output(ctx, ctr.WithExec(args, anyExit))
	if err != nil {
		return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	if code != 0 {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - gofmt exited "+strconv.Itoa(code)+
			"; a file it could not parse is a file it did not check.\n"+listed)
	}
	if listed != "" {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - gofmt needed on:\n"+listed)
	}
	return checks.VerdictOf(a, 0, a.ID+": clean")
}

// gofmtArgFile is where the population goes when it will not fit in an argv.
const gofmtArgFile = "/tmp/gofmt-files0"

// go build ./... succeeds.
//
// THE MODULES ARE PROVISIONED BEFORE THE TOOLCHAIN SPEAKS, for the reason
// goModules states: `go build` exits 1 for a module the proxy would not serve
// exactly as it exits 1 for a compile error, and an atom that reads exit 1 as
// FINDINGS turns a proxy's bad hour into a terminal red the sweep will not
// re-ask (measured 2026-09-11 17:26Z, foundry-tools#29).
func goBuild(ctx context.Context, r *run) checks.Verdict {
	return verdict(ctx, checks.AtomByID("go:build"),
		r.goModules().WithExec([]string{"go", "build", "./..."}, anyExit))
}

// staticcheck ./... reports nothing.
//
// THE BINARY IS BAKED INTO go-ci, at /go/bin and on PATH (measured inside the
// engine 2026-09-09, with opengrep and govulncheck beside it). The old script
// ran `go install honnef.co/go/tools/cmd/staticcheck@latest` first — a compile
// of the whole analysis suite, on every atom, on every gate, for a binary the
// image already carries — and `@latest` meant the gate's check set was whatever
// honnef published that morning rather than what the pin declared. The version
// probe below replaces it: its own exec under the DEFAULT Expect, so an image
// that lost the binary is state 2. The engine ends the "CANNOT RUN when not
// installed locally" branch this check has carried since it was a pre-push
// hook — the toolchain is the module's now — but it does not end it by letting
// an absent tool answer "fine".
//
// THE CHECK SET IS NAMED ON THE COMMAND LINE. staticcheck reads a
// staticcheck.conf from the tree when there is one, and -checks overrides it;
// naming the fleet's set here means a conf a repo adds later changes nothing in
// the gate. Measured 2026-09-11: no repo carries one today.
//
// THE DEFAULT IS EIGHT EXCLUSIONS, NOT SEVEN. The first cut listed -ST1000 …
// -ST1022 and left out -ST1023 (redundant type in a declaration), which
// staticcheck's own default config also disables — so the first Go pull through
// it (ourea #127) went red on a pre-existing `var fs billy.Filesystem = …` in a
// test file. What is spelled below is staticcheck's shipped default, verbatim;
// a stricter fleet set is a decision to make on purpose, not by omission.
func goStaticcheck(ctx context.Context, r *run) checks.Verdict {
	return verdict(ctx, checks.AtomByID("go:staticcheck"),
		r.goModules().
			WithExec([]string{"staticcheck", "-version"}).
			WithExec([]string{"staticcheck", "-checks", staticcheckChecks, "./..."}, anyExit))
}

// staticcheckChecks is staticcheck's shipped default set, spelled out so the
// gate's answer does not depend on a file any repository could add. Eight
// exclusions — see goStaticcheck for why -ST1023 is the eighth.
const staticcheckChecks = "all,-ST1000,-ST1003,-ST1016,-ST1020,-ST1021,-ST1022,-ST1023"

// govulncheck ./... reports no known vulnerability.
//
// BAKED, LIKE staticcheck. The old script `go install`ed
// golang.org/x/vuln/cmd/govulncheck@latest on every run for a binary go-ci
// already carries in /go/bin; the version probe is the provisioning step in its
// place, its own exec under the default Expect.
//
// THE MODULES ARE DOWNLOADED FIRST, which the old script did NOT do — it
// guarded the install and nothing else. govulncheck loads the package graph
// before it scans, so a proxy that is briefly gone surfaces as a govulncheck
// failure, and at exit 1 that reads as a vulnerability finding. MEASURED
// 2026-09-10T02:58Z, gate-hades-c099a35: govulncheck red at "loading packages"
// while the door rolled onto 9a5313fb. A door that is briefly gone is not a
// verdict on the code, so the fetch is its own step and its failure is a 2.
func goGovulncheck(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("go:govulncheck")
	out, code, err := output(ctx, r.goModules().
		WithExec([]string{"govulncheck", "-version"}).
		WithExec([]string{"govulncheck", "./..."}, anyExit))
	if err != nil {
		return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	return checks.VerdictOf(a, checks.GovulncheckExit(code), out)
}

// Every mutant gremlins makes of this pull's changed Go is killed by the tests.
//
// THE GATE IS GO, NOT A SCRIPT. It was foundry-stocks' ci/lib/mutation/go.sh,
// run phase by phase, scored by go_score.py. The measurement is now plain execs
// here — resolve, cover, the canary, gremlins — and the decision is
// checks.GoMutationVerdict, in DIFF mode against GATE_BASE, the pull's merge
// base as the door names it: 0 clean, 1 survivors, 2 did not measure. The
// script's setup and teardown phases are gone with it: they ran MUT_SETUP and
// MUT_TEARDOWN, which nothing had set since a repo stopped having a say.
//
// THE HISTORY IS THERE IN THE LANE THAT MATTERS. The mutation Job fetches the
// repository and the base the door names, so `git cat-file -e <base>` answers
// and the diff is real. A local pre-push run hands the engine a linked
// worktree, which gitReady turns into a throwaway repository with no history:
// resolve then stands down 0 with "no usable PR base sha", and the door's Job
// is the one that measures.
//
// THE GATE OWNS ITS KNOBS. gremlins auto-loads a .gremlins.yaml from the tree,
// and that file can set thresholds that turn a healthy run into exit 10; every
// gremlins run here reads an explicit neutral config instead.
//
// THE CANARY RUNS FIRST AND DECIDES WHETHER THE REPORT IS A MEASUREMENT. A
// harness that cannot start the test child scores every mutant KILLED; the
// control is a module whose one mutant must LIVE (checks.GoMutationCanary). The
// bound it existed for — gremlins v0.6.0 mangled --test-cpu into one argv word,
// #7649 — is kept through the environment the child inherits: GOMAXPROCS caps
// the test binary, GOFLAGS=-p caps concurrent builds.
//
// GO NEEDS NO critical_modules. The python, rust and ts mutation atoms read
// that declaration off .copier-answers.yml; gremlins scopes to the diff itself,
// exactly as the retired mutation-go did.
//
// A REPO HAS NO SAY. The first cut of these atoms sourced a repo-root
// ci/mutation.env — MUT_* knobs standing in for the retired workflow's inputs —
// and Rob asked why a repo should have a say in anything (2026-09-11). It
// should not: the scripts honour MUT_GATE=false, so that file was a one-line
// switch to turn a fleet gate off, the exact shape stop-justifications exists
// to refuse (Rule #2: one canonical gate, always the latest, a red is the
// committer's to fix). It is gone, and nothing below reads the tree for a knob.
//
// GENERATED GO IS EXCLUDED BY DEFAULT, FLEET-WIDE, NOT PER REPO. MEASURED on
// this stage's first live run (2026-09-11, foundry-tools' own diff): of 14
// survivors, one was in dagger.gen.go — dagger's codegen, which no test of ours
// covers and none should. The retired mutation-go.yml carried the same
// exclusion per repo; here it is the atom's default, and it is the same for
// every repo.
//
// NeedsDies FOR THE SAME REASON go:test-race CARRIES IT: gremlins gathers
// coverage by running `go test`, and hephaestus's internal/slag goldens are
// armed on CI=true — in a container with no /dies they refuse (FATAL, exit 1),
// gremlins' coverage run dies, and the lane answers could-not-run on every
// hephaestus pull. MEASURED 2026-09-11, hephaestus #53: "failed to gather
// coverage: impossible to executeCoverage coverage: exit status 1". Any atom
// that runs a repo's `go test` runs its armed goldens, and needs the tree they
// read.
func goMutation(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("go:mutation")
	ctr := r.gitReady(ctx, r.withBase(r.withDies(r.goModules())))
	// THE RECORD'S DATABASE, as go:test-race brings it: the DB-gated suites are
	// compiled for the coverage run and gremlins' (and serialised on the one
	// database), and the servers are bound. Without this every DB-touching line
	// read NOT COVERED by construction (foundry-tools#8608).
	ctr, dbs, scope := r.withTestDatabases(ctx, ctr)
	// The scope line is set on the verdict, not folded into the output: a pass
	// keeps no output (checks.VerdictOf), and the line is printed either way.
	settle := func(state int, reason string) checks.Verdict {
		v := checks.VerdictOf(a, state, a.ID+": "+reason)
		v.Reason = scope + "\n" + v.Reason
		return v
	}
	neverRan := func(err error) checks.Verdict { return settle(2, "CANNOT RUN - the atom never ran: "+err.Error()) }

	// RESOLVE: without a base the history reaches, there is no diff to scope
	// to, and a full run is the nightly's, never a pull's.
	const noBase = "no usable PR base sha — the diff-scoped gate did not run"
	if r.base == "" {
		return settle(0, noBase)
	}
	// rev-parse --verify --quiet, not cat-file -e: a missing object is exit 128
	// for cat-file, and the engine answers 128-191 as its own error even under
	// Expect ANY (foundry-tools#63) — a pull whose base the history lacks would
	// read could-not-run instead of standing down. rev-parse answers 1.
	_, code, err := output(ctx, ctr.WithExec([]string{"git", "rev-parse", "--verify", "--quiet", r.base + "^{commit}"}, anyExit))
	if err != nil {
		return neverRan(err)
	}
	if code != 0 {
		return settle(0, noBase)
	}
	// AN EMPTY DIFF MEANS "MUTATE EVERYTHING" TO GREMLINS, written for "no
	// --diff was given". A pull that changes no Go produces exactly that, so it
	// stands down here instead of mutating the whole module.
	changed, code, err := output(ctx, ctr.WithExec([]string{"git", "diff", "--relative", "--merge-base", r.base, "--name-only", "--", "*.go"}, anyExit))
	if err != nil {
		return neverRan(err)
	}
	if code != 0 {
		return settle(2, "CANNOT RUN - git could not diff the pull against its base "+r.base+": "+changed)
	}
	if strings.TrimSpace(changed) == "" {
		return settle(0, "this pull changes no Go file — nothing to mutate")
	}

	var tags []string
	if len(dbs) > 0 {
		tags = []string{"--tags", checks.BuildTags(dbs)}
	}
	neutral := ctr.WithNewFile(goMutationConfig, "# neutral — the mutation gate owns every knob it cares about.\n")

	// COVER: the profile the scorer reads to tell a misjudged NOT COVERED from
	// a real one. Never fatal — gremlins gathers its own; this one only corrects
	// the switch-case misread.
	coverArgs := []string{"go", "test", "-cover", "-coverprofile", goMutationProfile}
	if len(dbs) > 0 {
		coverArgs = append(coverArgs, "-tags", checks.BuildTags(dbs), "-p", "1")
	}
	covered := neutral.WithExec(append(coverArgs, "./..."), anyExit)
	if _, err := covered.ExitCode(ctx); err != nil {
		return neverRan(err)
	}
	profile, _ := covered.File("/src/" + goMutationProfile).Contents(ctx)

	// THE CANARY, in a module of its own.
	canary := checks.CanaryUnknown
	control := neutral.
		WithNewFile(mutationDir+"/canary/go.mod", checks.GoMutationCanaryMod).
		WithNewFile(mutationDir+"/canary/canary.go", checks.GoMutationCanaryCode).
		WithNewFile(mutationDir+"/canary/canary_test.go", checks.GoMutationCanaryTest).
		WithWorkdir(mutationDir+"/canary").
		WithExec([]string{"go", "test", "-cover", "-coverprofile", goMutationProfile, "./..."}, anyExit)
	if code, err := control.ExitCode(ctx); err == nil && code == 0 {
		out, _, err := outputBoth(ctx, control.WithExec([]string{"gremlins", "unleash", "--config", goMutationConfig,
			"--timeout-coefficient", "10", "--workers", "1", "."}, anyExit))
		if err == nil {
			canary = checks.GoMutationCanary(out)
		}
	}

	// MUTATE.
	args := append([]string{"gremlins", "unleash", "--config", goMutationConfig, "--output", goMutationReport,
		"--timeout-coefficient", "10", "--workers", strconv.Itoa(goMutationWorkers)}, tags...)
	args = append(args, "--exclude-files", goMutationExclude, "--diff", r.base, ".")
	mutated := covered.
		WithEnvVariable("GOMAXPROCS", "1").
		WithEnvVariable("GOFLAGS", "-p=1").
		WithExec(args, anyExit)
	status, err := mutated.ExitCode(ctx)
	if err != nil {
		return neverRan(err)
	}
	// gremlins writes no report when it has nothing to report; a read that
	// fails is that absence, and the verdict decides what it means.
	report, _ := mutated.File("/src/" + goMutationReport).Contents(ctx)

	return settle(checks.GoMutationVerdict(checks.GoMutationRun{
		Status: status, Report: []byte(report), Profile: profile, Canary: canary, Workers: goMutationWorkers,
	}))
}

const (
	// mutationDir is where the canary module is written.
	mutationDir = "/tmp/mutation"
	// goMutationConfig is the neutral gremlins config every run reads.
	goMutationConfig = mutationDir + "/gremlins-neutral.yaml"
	// goMutationReport and goMutationProfile are what the run leaves in /src.
	goMutationReport  = "mutation-go.json"
	goMutationProfile = "mutation-cover.out"
	// goMutationWorkers is gremlins' --workers.
	goMutationWorkers = 4
	// goMutationExclude keeps generated Go out of the mutant population —
	// vendored code, dagger's codegen, protobuf stubs and kubebuilder's
	// zz_generated. See goMutation for the measurement.
	goMutationExclude = `^vendor/|(^|/)dagger\.gen\.go$|\.pb\.go$|(^|/)zz_generated`
)

// lastLine is the tail of a tool's output — the line a human reads first.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
