package main

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
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
	register("go:release", goRelease)
	register("go:test", goTest)
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
func (r *run) goModules(dir string) *dagger.Container {
	return goDownload(r.laneCode(checks.ImageGo), dir)
}

// goDownload is goModules on a container an atom has already prepared at the
// tree's root — the dies mounted, the repository made readable. The module's
// directory is entered AFTER that preparation and before anything reads the
// module, because gitReady runs `git init .` in the working directory and a
// module's directory is not the repository's.
func goDownload(ctr *dagger.Container, dir string) *dagger.Container {
	return inModule(ctr, dir).WithExec([]string{"go", "mod", "download"})
}

// inModule enters one module's directory. The root module stays at /src with
// no second withWorkdir, so a single-module repository's chains are the ones
// it had before modules were enumerated.
func inModule(ctr *dagger.Container, dir string) *dagger.Container {
	if dir == "." {
		return ctr
	}
	return ctr.WithWorkdir(path.Join("/src", dir))
}

// goModuleDirs is the tree's Go modules (checks.GoModuleDirs), read once per
// run off the gate's own population.
func (r *run) goModuleDirs(ctx context.Context) ([]string, error) {
	r.goModsOnce.Do(func() {
		files, err := r.population(ctx, "**/go.mod")
		if err != nil {
			r.goModsErr = err
			return
		}
		r.goMods = checks.GoModuleDirs(files)
	})
	return r.goMods, r.goModsErr
}

// eachModule runs one go atom in every module the tree carries and folds the
// answers (checks.FoldModules). A repository whose only module is its root
// gets that one verdict back untouched, exactly as before modules were
// enumerated. Modules run one after another: a pre-push gate on a laptop
// shares the engine with the other atoms, and the modules of one repository
// are not independent enough of each other's caches to race.
func (r *run) eachModule(ctx context.Context, a checks.AtomDef, one func(dir string) checks.Verdict) checks.Verdict {
	dirs, err := r.goModuleDirs(ctx)
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

// go vet ./... reports nothing.
func goVet(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("go:vet")
	return r.eachModule(ctx, a, func(dir string) checks.Verdict {
		return verdict(ctx, a, r.goModules(dir).WithExec([]string{"go", "vet", "./..."}, anyExit))
	})
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
	return r.eachModule(ctx, a, func(dir string) checks.Verdict { return goTestIn(ctx, r, a, dir, true) })
}

// go test ./... passes: the SAME packages, at the commit's cadence — no race
// detector, no database, no build tags, so the DB-gated suites do not even
// compile in. Rob's four stages put the unit tests at the commit and the race
// + live-DB run at the push; a commit that waits minutes for a suite it will
// wait for again at the push is the loop this plan exists to shorten.
//
// It is SUBSUMED BY go:test-race (checks.AtomDef.SubsumedBy): a caller that
// asks for both stages at once gets the race run and an omission line saying
// so, never the suite twice.
func goTest(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("go:test")
	return r.eachModule(ctx, a, func(dir string) checks.Verdict { return goTestIn(ctx, r, a, dir, false) })
}

// goTestIn runs one module's suite: race + the record's live databases when
// race is set (the push), the plain unit run when it is not (the commit).
func goTestIn(ctx context.Context, r *run, a checks.AtomDef, dir string, race bool) checks.Verdict {
	// THE SUITE RUNS IN A REPOSITORY git CAN READ. A test that shells out to
	// git — hephaestus's TestRealResolveHistory runs `git ls-remote` against a
	// fixture — fails on a linked worktree's dangling `.git` file with
	// "fatal: not a git repository: <primary>/.git/worktrees/<name>" before
	// it ever reaches the fixture, and the lane files FINDINGS for a test
	// that never got to look (measured 2026-09-14 on hephaestus, pre-push).
	// gitReady is the fix cargo-test and the mutation atoms already carry.
	mods := goDownload(r.gitReadyOn(ctx, r.withDies(r.laneCode(checks.ImageGo)), r.code()), dir)

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
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - go list ./... failed, so the tests cannot be counted: "+lastLine(counts))
	}
	if !checks.GoHasTestFiles(counts) {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - no test file in any package; nothing is built without tests")
	}
	args := []string{"go", "test"}
	scope := "unit suite: no race detector and no database — go:test-race runs those at the push"
	if race {
		var dbs []checks.TestDB
		mods, dbs, scope = r.withTestDatabases(ctx, mods)
		args = append(args, "-race")
		if len(dbs) > 0 {
			args = append(args, "-tags", checks.BuildTags(dbs), "-p", "1")
		}
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

	ctr := r.laneCode(checks.ImageGo)
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
	a := checks.AtomByID("go:build")
	return r.eachModule(ctx, a, func(dir string) checks.Verdict {
		return verdict(ctx, a, r.goModules(dir).WithExec([]string{"go", "build", "./..."}, anyExit))
	})
}

// THE RELEASE BUILD, AND IT IS THE ONE THE IMAGE CARRIES. Rob's four stages:
// "Build - Copy the binary from the previous complex run (if green, which is a
// prerequisite for getting here)". The Gate compiles it with the flags the
// image needs, the engine caches that exec by its inputs, and F14's Build asks
// for the same directory and gets the compile it already paid for.
//
// WHAT IT BUILDS IS DERIVED, NOT DECLARED — checks/release.go carries the
// measurement (the fleet's 29 Go Dockerfiles: one flag set, always ./cmd/<X>,
// -mod=vendor exactly where vendor/ is). A record speaks only for the two repos
// that ship more than their own name.
//
// ROOT MODULE ONLY. The convention is the STAR's binary and a star is one
// module; a repo whose root carries no go.mod has no image to build from it.
func goRelease(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("go:release")
	// A REPOSITORY THAT SHIPS NO IMAGE HAS NO RELEASE BUILD, and that is an
	// ABSENCE, not a could-not-run. Measured the honest way, on this module's
	// own gate (foundry-tools #99, 5d3e330): foundry-tools has no
	// .copier-answers.yml because it publishes no image, and the first cut
	// filed CANNOT RUN — a red about a build nobody asked for. The surface is
	// a tracked Dockerfile, the same fact fleet:hadolint reads.
	files, err := r.population(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the tree could not be read: "+err.Error())
	}
	if len(checks.DockerfilePopulation(files)) == 0 {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - this repository tracks no Dockerfile or Containerfile, so it ships no image and has no release build")
	}
	star, err := r.starName(ctx)
	if err != nil {
		// NOT A STAR, NOT A STAR IMAGE, ABSENT. Measured on foundry-stocks the
		// afternoon this atom landed (Wonka17's record, 2026-09-17, nine gate
		// runs on #205): the forge ships images — the CI bases, blade-base,
		// forge-tools — but it is not copier-templated, carries no
		// .copier-answers.yml, and names no star. The first cut read "names
		// no star" as a could-not-run and wedged every landing there. A repo
		// with Dockerfiles and no star identity builds its images through
		// other lanes; the STAR release build has nothing to say about it, and
		// says so the way python:* says it of a missing pyproject.toml.
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - "+err.Error()+", so this is not a star image and there is no star release build")
	}
	plan, why := r.releasePlan(ctx, star)
	if why != "" {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+why)
	}
	ctr, err := r.releaseBuild(ctx, plan)
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+err.Error())
	}
	v := verdict(ctx, a, ctr)
	v.Reason = checks.ReleaseScope(plan) + "\n" + v.Reason
	return v
}

// releasePlan reads the star's name and its record, and derives what the
// release build produces. Every refusal is about the repository, so the atom
// settles it as a could-not-run: an image whose binaries cannot be named is
// not an image anyone should build.
// starName is the star this repository is, as its answers file declares it —
// the key the record and the release convention share. A repo without one is
// not a star: an error here is "not a star", never "could not read".
func (r *run) starName(ctx context.Context) (string, error) {
	answers, err := r.src.File(".copier-answers.yml").Contents(ctx)
	if err != nil {
		return "", errors.New("no .copier-answers.yml, so the repository names no star")
	}
	star := checks.ServiceName(answers)
	if star == "" {
		return "", errors.New("no service_name in .copier-answers.yml, so the repository names no star")
	}
	return star, nil
}

// releasePlan derives what the star's release build produces: its record's
// binaries or its own name, and whether the module vendors. Every refusal is
// about the repository and settles as a could-not-run.
func (r *run) releasePlan(ctx context.Context, star string) (checks.ReleasePlan, string) {
	var declared []string
	if slag, err := r.dies.File("fleet/stars/" + star + "/slag.json").Contents(ctx); err == nil {
		declared = checks.ReleaseBinaries(slag)
	}
	vendored := false
	if entries, err := r.src.Entries(ctx); err == nil {
		vendored = slices.Contains(entries, "vendor/")
	}
	plan, err := checks.GoReleasePlan(star, declared, vendored)
	if err != nil {
		return checks.ReleasePlan{}, err.Error()
	}
	return plan, ""
}

// releaseBuild is the compile itself: one exec per binary, in the go lane, off
// the root module. The container it answers carries /out, which is what F14
// copies onto the base image.
func (r *run) releaseBuild(ctx context.Context, plan checks.ReleasePlan) (*dagger.Container, error) {
	dirs, err := r.goModuleDirs(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not enumerate the tree's Go modules: %w", err)
	}
	if !slices.Contains(dirs, ".") {
		return nil, fmt.Errorf("no go.mod at the repository root, so there is no star binary to build")
	}
	// A VENDORED MODULE IS PROVISIONED BY ITS vendor/, AND NOTHING IS
	// DOWNLOADED. `go mod download` fills the module cache from the proxy
	// whether or not vendor/ exists, and -mod=vendor then ignores the cache —
	// so for a vendored module the download is pure network reach, and for
	// ourea it is the reach the vendoring exists to remove: ourea IS the door,
	// GONOPROXY routes its first-party module past Nexus to the door, and a
	// release build that downloads needs a running ourea to build ourea
	// (ourea internal/discipline TestImageBuildsWithoutReachingTheDoor, the
	// guard that used to read this off the Dockerfile's build stage and now
	// reads it off the release path). The compile is the image's; the gate's
	// other atoms keep their download, which is F16's to move.
	base := r.laneCode(checks.ImageGo)
	if plan.Vendored {
		base = inModule(base, ".")
	} else {
		base = goDownload(base, ".")
	}
	ctr := base.WithEnvVariable("CGO_ENABLED", "0")
	for _, b := range plan.Binaries {
		ctr = ctr.WithExec(checks.GoReleaseArgs(b, plan.Vendored), anyExit)
	}
	return ctr, nil
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
	a := checks.AtomByID("go:staticcheck")
	return r.eachModule(ctx, a, func(dir string) checks.Verdict {
		return verdict(ctx, a, r.goModules(dir).
			WithExec([]string{"staticcheck", "-version"}).
			WithExec([]string{"staticcheck", "-checks", staticcheckChecks, "./..."}, anyExit))
	})
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
	return r.eachModule(ctx, a, func(dir string) checks.Verdict {
		out, code, err := output(ctx, r.goModules(dir).
			WithExec([]string{"govulncheck", "-version"}).
			WithExec([]string{"govulncheck", "./..."}, anyExit))
		if err != nil {
			return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
		}
		// govulncheck's 3 is "vulnerabilities found" and reads as 1; any
		// other non-zero with a network fault in it is the database not
		// answering, which AuditVerdict reads as could-not-run.
		return checks.AuditVerdict(a, checks.GovulncheckExit(code), out)
	})
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
// THE GATE OWNS ITS KNOBS, AND THEY HAVE ONE HOME. gremlins auto-loads a
// .gremlins.yaml from the tree, and that file can set thresholds that turn a
// healthy run into exit 10, so every run here reads a config the tree cannot
// touch. That config is now forge-testkit-go's generated one, read at its own
// repo through the door (checks.TestkitRepo) — A REPO STILL HAS NO SAY, because
// the file does not come from the tree under test.
//
// What it replaces is a comment-only file this atom wrote itself. "Neutral"
// meant "state nothing and inherit gremlins' defaults", so the fleet's gate was
// whatever the tool happened to default to, written down in two places that
// agreed by luck — and the mutator set nobody had stated was invisible to
// anyone reading either one.
//
// THE COEFFICIENT COMES FROM THAT FILE NOW, not from a flag here. It is the knob
// that decides whether the run is CORRECT: parallel mutants slow each other
// down, trip the timeout computed from the unmutated suite, and are recorded
// TIMED OUT — which counts as neither killed nor survived and silently shrinks
// the population. MEASURED on a 30-mutant module, 16 cores, five runs each on an
// unchanged tree: --workers 4 alone gave 15/12, 15/13, 14/14, 15/13, 14/9 — five
// different answers — while --workers 4 with coefficient 10 gave 16/14 three
// times. --workers stays a flag here because 4 is this LANE's deliberate
// override of a config default of 1, which is what a repo with a loopback-bound
// suite needs and a CI runner does not.
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
	return r.eachModule(ctx, a, func(dir string) checks.Verdict { return goMutationIn(ctx, r, a, dir) })
}

// goMutationIn is go:mutation in one module.
//
// A NESTED MODULE NEEDS diff.relative OR IT MEASURES NOTHING. gremlins scopes
// to `git diff --merge-base <base>`, whose paths are relative to the
// repository, and matches them against paths relative to the module. In
// tools/forge nothing matched, and the run scored zero mutants on a pull that
// changed three of its files. MEASURED 2026-09-16 on foundry-stocks #199
// against its base: 0 killed, 0 lived without it; 15 killed, 0 lived with
// diff.relative=true, which makes that diff print paths relative to the
// working directory. At a repository's root the two are the same paths, so
// every module carries it.
func goMutationIn(ctx context.Context, r *run, a checks.AtomDef, dir string) checks.Verdict {
	ctr := goDownload(r.gitReady(ctx, r.withBase(r.withDies(r.lane(checks.ImageGo)))), dir)
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
	// The change set starts at the merge base, not at the base the door named
	// (run.changeBase): main's tip moves under an open pull. This lane asked
	// git for --merge-base already; gremlins' own --diff below did not.
	since, err := r.changeBase(ctx, ctr)
	if err != nil {
		return settle(2, "CANNOT RUN - "+err.Error())
	}
	if since == "" {
		return settle(0, noBase)
	}
	// AN EMPTY DIFF MEANS "MUTATE EVERYTHING" TO GREMLINS, written for "no
	// --diff was given". A pull that changes no Go produces exactly that, so it
	// stands down here instead of mutating the whole module.
	changed, code, err := output(ctx, ctr.WithExec([]string{"git", "diff", "--relative", since, "--name-only", "--", "*.go"}, anyExit))
	if err != nil {
		return neverRan(err)
	}
	if code != 0 {
		return settle(2, "CANNOT RUN - git could not diff the pull against its base "+since+": "+changed)
	}
	if strings.TrimSpace(changed) == "" {
		return settle(0, "this pull changes no Go file — nothing to mutate")
	}

	var tags []string
	if len(dbs) > 0 {
		tags = []string{"--tags", checks.BuildTags(dbs)}
	}
	// THE CANONICAL CONFIG, at its one home. A read that fails is a lane that
	// could not measure, never a gate that passes: without this file gremlins
	// would fall back to its own defaults and score a different population.
	cfg, err := r.testkit.File(checks.TestkitGremlinsConfig).Contents(ctx)
	if err != nil {
		return neverRan(fmt.Errorf("reading the canonical gremlins config from %s: %w", checks.TestkitRepo, err))
	}
	canonical := ctr.WithNewFile(goMutationConfig, cfg)

	// COVER: the profile the scorer reads to tell a misjudged NOT COVERED from
	// a real one. Never fatal — gremlins gathers its own; this one only corrects
	// the switch-case misread.
	//
	// SCOPED TO THE PACKAGES THE DIFF TOUCHES. The profile only ever answers
	// for a mutated line, and every mutant is in a changed file, so covering
	// the whole module was a full suite run bought for nothing: MEASURED
	// 2026-09-18 on ourea aeb9cd9, `go test -cover ./...` with -p 1 took
	// 4m44s of an 11m44s lane for a pull that touched one package whose
	// suite runs in ~100s. goMutationCoverPackages names the packages.
	coverArgs := []string{"go", "test", "-cover", "-coverprofile", goMutationProfile}
	if len(dbs) > 0 {
		coverArgs = append(coverArgs, "-tags", checks.BuildTags(dbs), "-p", "1")
	}
	covered := canonical.WithExec(append(coverArgs, goMutationCoverPackages(changed)...), anyExit)
	if _, err := covered.ExitCode(ctx); err != nil {
		return neverRan(err)
	}
	profile, _ := covered.File(path.Join("/src", dir, goMutationProfile)).Contents(ctx)

	// THE CANARY, in a module of its own.
	canary := checks.CanaryUnknown
	control := canonical.
		WithNewFile(mutationDir+"/canary/go.mod", checks.GoMutationCanaryMod).
		WithNewFile(mutationDir+"/canary/canary.go", checks.GoMutationCanaryCode).
		WithNewFile(mutationDir+"/canary/canary_test.go", checks.GoMutationCanaryTest).
		WithWorkdir(mutationDir+"/canary").
		WithExec([]string{"go", "test", "-cover", "-coverprofile", goMutationProfile, "./..."}, anyExit)
	if code, err := control.ExitCode(ctx); err == nil && code == 0 {
		out, _, err := outputBoth(ctx, control.WithExec([]string{"gremlins", "unleash", "--config", goMutationConfig,
			"--workers", "1", "."}, anyExit))
		if err == nil {
			canary = checks.GoMutationCanary(out)
		}
	}

	// MUTATE.
	args := append([]string{"gremlins", "unleash", "--config", goMutationConfig, "--output", goMutationReport,
		"--workers", strconv.Itoa(goMutationWorkers)}, tags...)
	args = append(args, "--exclude-files", goMutationExclude, "--diff", since, ".")
	// THE SCOPE IS THE ADDED LINES, NOT THE HUNK. gremlins reads its own
	// `git diff --merge-base` and takes each fragment as one contiguous range
	// of LinesAdded lines from its first addition — so with git's default
	// three lines of context, two insertions six lines apart merge into one
	// fragment and the UNCHANGED lines between them come into scope. Measured
	// on kairos e138f50 (2026-09-18): a three-line insertion into main()
	// dragged main's own `err != nil`, a line the pull never touched and no
	// test can reach through a process boundary, in as NOT COVERED — a red
	// on code the pull did not write. diff.context=0 makes every fragment
	// exactly its additions (the same dry run: that mutant SKIPPED, the real
	// one still RUNNABLE).
	// THE BASELINE IS NEVER CACHED. gremlins times each mutant against the
	// unmutated suite's runtime, which it measures in its own coverage gather
	// — a `go test -cover ./...` in this container, on the tree the COVER
	// step above just tested with the same flags. Go's test cache answered
	// it: MEASURED 2026-09-18 on ourea aeb9cd9 (mutation-ourea-aeb9cd9-7kj9n),
	// "Gathering coverage... done in 1.09s" for a module whose gatejob
	// package alone tests in ~100s, so the per-mutant timeout was ten times
	// a cached read — about eight seconds — and the run scored killed 72,
	// TIMED OUT 79, honest 1: half the population unanswered and the gate
	// green over it. -count=1 makes every run under gremlins a real one, the
	// baseline included; the profile above may stay cached — it is the same
	// tree either way.
	mutated := covered.
		WithEnvVariable("GOMAXPROCS", "1").
		WithEnvVariable("GOFLAGS", "-p=1 -count=1").
		WithEnvVariable("GIT_CONFIG_COUNT", "2").
		WithEnvVariable("GIT_CONFIG_KEY_0", "diff.relative").
		WithEnvVariable("GIT_CONFIG_VALUE_0", "true").
		WithEnvVariable("GIT_CONFIG_KEY_1", "diff.context").
		WithEnvVariable("GIT_CONFIG_VALUE_1", "0").
		WithExec(args, anyExit)
	status, err := mutated.ExitCode(ctx)
	if err != nil {
		return neverRan(err)
	}
	// gremlins writes no report when it has nothing to report; a read that
	// fails is that absence, and the verdict decides what it means.
	report, _ := mutated.File(path.Join("/src", dir, goMutationReport)).Contents(ctx)

	// CLASSIFY, IN THE LANE, WHERE THE SOURCE IS. forge-testkit-go's gate
	// parses the tree to say which survivors no test could ever kill — a
	// mutant in a top-level const or var has no coverage block — and answers
	// them under -json for the scorer on the host to fold in. Its own exit
	// code is not the verdict here (this atom holds the canary and the timeout
	// budget); its stdout is. Nothing to write is nothing to classify.
	var classified, classifyErr string
	if report != "" {
		classified, classifyErr, _ = classify(ctx, mutated.WithExec([]string{"mutation-gate", "-report", goMutationReport, "-C", ".", "-json"}, anyExit))
	}

	return settle(checks.GoMutationVerdict(checks.GoMutationRun{
		Status: status, Report: []byte(report), Profile: profile, Canary: canary, Workers: goMutationWorkers,
		Classified: []byte(classified), ClassifyErr: classifyErr,
	}))
}

// goMutationCoverPackages names the packages the COVER step runs, from the
// diff's changed Go files: one `./<dir>` per directory that holds one, the
// module root as ".", in path order and without repeats. An empty diff is
// the whole module — the caller has already stood down on that case, so this
// is only the shape the fallback takes.
func goMutationCoverPackages(changed string) []string {
	seen := map[string]bool{}
	var pkgs []string
	for _, f := range strings.Split(changed, "\n") {
		f = strings.TrimSpace(f)
		if f == "" || !strings.HasSuffix(f, ".go") {
			continue
		}
		dir := path.Dir(f)
		pkg := "."
		if dir != "." && dir != "" {
			pkg = "./" + dir
		}
		if !seen[pkg] {
			seen[pkg] = true
			pkgs = append(pkgs, pkg)
		}
	}
	if len(pkgs) == 0 {
		return []string{"./..."}
	}
	slices.Sort(pkgs)
	return pkgs
}

// classify answers mutation-gate's stdout and stderr separately: the JSON is
// the answer, and what it said when it had none is the reason the verdict
// carries. An engine error is a reason too, never a silent empty answer.
func classify(ctx context.Context, ctr *dagger.Container) (out, errOut string, code int) {
	code, err := ctr.ExitCode(ctx)
	if err != nil {
		return "", fmt.Sprintf("the engine could not run mutation-gate: %v", err), 0
	}
	out, _ = ctr.Stdout(ctx)
	errOut, _ = ctr.Stderr(ctx)
	return out, errOut, code
}

const (
	// mutationDir is where the canary module is written.
	mutationDir = "/tmp/mutation"
	// goMutationConfig is where the canonical config is written in the lane.
	//
	// SPELLED WHOLE, not mutationDir + "/…", and the reason is this gate's own.
	// A `+` in a top-level const has NO COVERAGE BLOCK — Go's cover tool
	// instruments function bodies — so gremlins reports its mutant NOT COVERED
	// forever and no test that could ever be written would change that. This
	// pull's first run proved it on this very line: 1 killed, 1 not covered,
	// the uncovered one being the concatenation that used to be here.
	//
	// The concatenation bought nothing, so it is gone rather than forgiven.
	// That is the same answer this gate gives every star: remove the operator
	// when removing it costs nothing. (mutationDir is still shared by the
	// canary and the python lane, where every use is inside a function body and
	// its mutants are killable.)
	goMutationConfig = "/tmp/mutation/gremlins-canonical.yaml"
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
