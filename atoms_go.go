package main

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"dagger/foundry-tools/internal/buildlane"
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

// goBuilt is goModules with the module compiled once: `go build ./...` under
// anyExit, so the shared GOCACHE volume holds every package the tree and its
// dependencies need before the analysers read them.
//
// ONE COMPILE, SHARED BY CONTENT. go:build, go:vet, go:staticcheck and
// go:govulncheck all branch from this container; the engine keys the exec on
// its inputs, so the four atoms (and the warm-up they would each have paid
// for) cost one build, not four concurrent ones racing to fill the same cache.
// vet, staticcheck and govulncheck then run side by side against a warm cache.
// go:test-race stays beside them rather than behind: it compiles with -race,
// which a plain build does not warm.
//
// THE WARM-UP CANNOT MOVE A VERDICT. It runs under anyExit, so a build that
// fails (a compile error, a finding to go:build) lets the chain carry on, and
// vet still reports the compile error it always did. Only go:build reads this
// exec's exit code.
func (r *run) goBuilt(dir string) *dagger.Container {
	return r.goModules(dir).WithExec([]string{"go", "build", "./..."}, anyExit)
}

// go vet ./... reports nothing.
func goVet(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("go:vet")
	return r.eachModule(ctx, a, func(dir string) checks.Verdict {
		return verdict(ctx, a, r.goBuilt(dir).WithExec([]string{"go", "vet", "./..."}, anyExit))
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
	// THE REGEN TESTS RUN INSTEAD OF SKIPPING where the tree has packages
	// gravity generated (atoms_go_regen.go): a stale regeneration is a finding,
	// not just a hand edit.
	mods, regen, err := r.withGravityRegen(ctx, mods)
	if errors.Is(err, errGravityRead) {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+err.Error())
	}
	if err != nil {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - "+err.Error())
	}
	args := []string{"go", "test"}
	scope := "unit suite: no race detector and no database — go:test-race runs those at the push"
	if race {
		var dbs []checks.TestDB
		var brokers []checks.TestBroker
		var bscope string
		// The shared binding: the complex checks run in sequence, so they do
		// not contend with each other. Only the mutation gate runs beside them.
		mods, dbs, scope = r.withTestDatabases(ctx, mods, "")
		mods, brokers, bscope = r.withTestBrokers(ctx, mods, "")
		scope = scope + "\n" + bscope
		args = append(args, goRaceCoverArgs()...)
		// `-p 1` for EITHER kind. The databases share one server and the suites
		// reset its schema; one broker serves every package and its isolation
		// unit is the topic. Parallel packages break both the same way.
		if len(dbs) > 0 || len(brokers) > 0 {
			args = append(args, "-tags", checks.BuildTags(dbs, brokers), "-p", "1")
		}
	}
	args = append(args, "./...")
	v := verdict(ctx, a, mods.WithExec(args, anyExit))
	v.Reason = scope + "\n" + regen + "\n" + v.Reason
	return v
}

// goRaceProfile is where go:test-race leaves its coverage profile: outside the
// tree, so the run writes nothing a later atom could read as a change.
const goRaceProfile = "/tmp/race-cover.out"

// goRaceCoverArgs is what turns the push's suite into a race run that also
// records which blocks it executed. -race implies atomic mode already; naming
// it keeps the profile's mode a decision here rather than a default.
//
// NOTHING READS THE PROFILE YET. gomutants (v0.6.1) takes no profile — it
// builds its own per-test map — and go:mutation's cover step is scoped to the
// packages the diff touches, on a database of its own; see goMutationIn.
func goRaceCoverArgs() []string {
	return []string{"-race", "-covermode", "atomic", "-coverprofile", goRaceProfile}
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
func (r *run) withTestDatabases(ctx context.Context, ctr *dagger.Container, scope string) (*dagger.Container, []checks.TestDB, string) {
	answers, _, _ := fileIfPresent(ctx, r.src, ".copier-answers.yml")
	star := checks.ServiceName(answers)
	if star == "" {
		return ctr, nil, checks.TestDBScope(nil, nil, "no service_name in .copier-answers.yml, so no record to read")
	}
	slag, err := r.starRecord(ctx, star)
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
		base := dag.Container().From(d.Image).
			WithEnvVariable("POSTGRES_USER", checks.TestDBRole).
			WithEnvVariable("POSTGRES_PASSWORD", checks.TestDBRole).
			WithEnvVariable("POSTGRES_DB", checks.TestDBName)
		if scope != "" {
			// THE DEFINITION HAS TO DIFFER, not just the alias. dagger
			// content-addresses services: the mutation lane and the complex
			// checks ran concurrently and built byte-identical definitions, so
			// they were handed the SAME Postgres and tore each other's schema
			// apart (checks.TestDB.AliasFor carries the measurements). Binding
			// a second alias to one service would not have separated them —
			// this env var is what makes it a second server.
			base = base.WithEnvVariable("FOUNDRY_TEST_DB_LANE", scope)
		}
		svc := base.WithExposedPort(5432).
			AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})
		ctr = ctr.WithServiceBinding(d.AliasFor(scope), svc).WithEnvVariable(d.Env, d.DSNFor(scope))
	}
	return ctr, dbs, checks.TestDBScope(dbs, checks.UncompiledTags(tags, dbs, checks.TestBrokers), "")
}

// withTestBrokers binds the fleet's test broker to a lane container for the
// broker-gated suites the tree carries, when the star's RECORD names topics —
// checks/testdb.go carries the reasoning and the vocabulary.
//
// THE SAME THREE READS as withTestDatabases, and none of them a knob: the star's
// name off the answers file, its record off the mounted dies, and the tree's
// single-tag `//go:build` lines off one recursive grep. The grep is the same exec
// the database path runs, so the engine serves it from cache rather than twice.
//
// NO ISOLATION HELPER, unlike the database path, and that is the broker's own
// contract rather than a shortcut: kafka's isolation unit is the topic plus the
// consumer group, so one bound broker has exactly the semantics
// forge-testkit-go's fixture documented. What the lane CANNOT do is mint the
// names — a suite that hardcodes a topic reads another package's records, which
// is why TestBrokerScope prints the caveat on every run.
func (r *run) withTestBrokers(ctx context.Context, ctr *dagger.Container, scope string) (*dagger.Container, []checks.TestBroker, string) {
	// NO BRANCH LIVES HERE. This makes the three reads and hands them over as
	// facts; every decision is checks.SelectBrokers', where a unit test can reach
	// it. The first cut guarded in place and the mutation lane graded those guards
	// NOT COVERED and LIVED on the pull that added them (#196, six survivors) — a
	// guard no test can execute is a guard that is not there.
	answers, _, _ := fileIfPresent(ctx, r.src, ".copier-answers.yml")
	slag, slagErr := r.starRecord(ctx, checks.ServiceName(answers))
	out, code, tagsErr := output(ctx, ctr.WithExec([]string{
		"grep", "-rhoE", `^//go:build [A-Za-z0-9_]+$`, "--include=*_test.go", ".",
	}, anyExit))
	brokers, scopeLine := checks.SelectBrokers(checks.BrokerReads{
		Answers: answers,
		Slag:    slag, SlagErr: slagErr,
		Tags: out, TagsErr: tagsErr, TagsCode: code,
	})
	for _, b := range brokers {
		// THE ADVERTISED ADDRESS IS THE ALIAS, and it is what makes this work
		// through a binding at all. It also makes the service DEFINITION differ
		// per lane for free — the database path needs FOUNDRY_TEST_DB_LANE to
		// achieve the same thing, because dagger content-addresses services and
		// two lanes with byte-identical definitions were handed one server.
		// NOT WithExec. A WithExec is an op with a snapshot to commit and a
		// service command never exits, so the commit never lands, the service
		// never reads ready, and the client exec that binds it never starts —
		// the lane then emits nothing and dies on the silence limit while the
		// broker's own log shows it healthy. checks.TestBroker.StartArgs
		// carries the measurements; the Postgres service above is built the
		// same way for the same reason.
		svc := dag.Container().From(b.Image).
			WithExposedPort(b.Port).
			WithDefaultArgs(b.StartArgs(scope)).
			AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})
		ctr = ctr.WithServiceBinding(b.AliasFor(scope), svc).WithEnvVariable(b.Env, b.AddrFor(scope))
	}
	return ctr, brokers, scopeLine
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
		return verdict(ctx, a, r.goBuilt(dir))
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
	if err != nil && !isNotAStar(err) {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+err.Error())
	}
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
	// THE COMPILE IS ASKED FOR BY THE DOCKERFILE, and a Dockerfile that does
	// not ask is an ABSENCE. The build lane reads one contract off the
	// Dockerfile (buildlane.CopiesRelease; build.go stageRelease): a COPY from
	// release/ asks for the Gate's artifact, and a Dockerfile that carries its
	// own build stage copies nothing from release/ and is built exactly as
	// before. This atom reads the same contract, because a release build the
	// image never copies is a compile nobody asked for — and it is the compile
	// that reds a cgo star. Measured on narcissus (foundry-tools #10307,
	// narrowed 2026-09-18): its analyzers are tree-sitter through cgo, built
	// statically in its own build stage, and CGO_ENABLED=0 here fails on a
	// binary its image never carries. Keyed on the fact the build lane keys
	// on, so the two lanes cannot disagree about whose compile it is; and
	// read AFTER the star's name, so a repo that is not a star still says so.
	asking, dockerfile, err := r.releaseDockerfile(ctx, checks.DockerfilePopulation(files))
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+err.Error())
	}
	if asking == "" {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - no tracked Dockerfile copies from "+buildlane.ReleaseDir+"/, so this image compiles itself: its build is the build lane's, and there is no release build to make here")
	}
	plan, why := r.releasePlan(ctx, star, dockerfile)
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

// starName is the star this repository is — the key the record and the
// release convention share. The answers file says it first; a repository
// with none is named by ITS RECORD, the one in foundry-dies whose meta.repo
// is this clone's custody key. hephaestus is the case (CA F17, Rob
// 2026-09-19): not copier-templated, no .copier-answers.yml, and a record at
// fleet/stars/hephaestus/slag.json saying repo rob/hephaestus — the door
// already routes its lanes by that record, so the release build reads the
// same name. The clone URL is the door's (--repo) or the hook's (--origin);
// a run given neither, and no answers file, is not a star: an error here is
// "not a star", never "could not read".
//
// AN ANSWERS FILE WITH NO service_name FALLS BACK TO THE RECORD TOO. Only
// the go, rust and python templates ask for one; the frontend template asks
// for repo_name, which is not the star (demeter's is demeter-mcp, its record
// fleet/stars/demeter). Measured on calliope #46, 2026-09-19: the push's
// ts:release stood down ABSENT as "not a star image" and the build lane's
// Release() failed on the same sentence — a star with a record, read as none.
func (r *run) starName(ctx context.Context) (string, error) {
	lacks := "no .copier-answers.yml"
	if answers, present, _ := fileIfPresent(ctx, r.src, ".copier-answers.yml"); present {
		if star := checks.ServiceName(answers); star != "" {
			return star, nil
		}
		lacks = "no service_name in .copier-answers.yml"
	}
	key := checks.RepoKey(r.repo)
	if key == "" {
		key = checks.RepoKey(r.origin)
	}
	if key == "" {
		return "", notAStar(lacks + " and no clone URL to find a record by, so the repository names no star")
	}
	star, err := r.starOfRepo(ctx, key)
	if err != nil {
		return "", err
	}
	if star == "" {
		return "", notAStar(fmt.Sprintf("%s, and no record in foundry-dies names repo %s, so the repository names no star", lacks, key))
	}
	return star, nil
}

// notAStarError is starName's answer when the repository is not a star — an
// ABSENCE to the release atoms, where a read fault (the dies unreadable) is a
// could-not-run. errors.As tells them apart.
type notAStarError struct{ why string }

func notAStar(why string) error        { return &notAStarError{why: why} }
func (e *notAStarError) Error() string { return e.why }

// isNotAStar reports whether err is starName's "not a star".
func isNotAStar(err error) bool {
	var ns *notAStarError
	return errors.As(err, &ns)
}

// starOfRepo is the star whose record says meta.repo == key, or "" when no
// record does. The records are read at their one home (r.dies); a tree that
// cannot be listed is an error, not "no star".
func (r *run) starOfRepo(ctx context.Context, key string) (string, error) {
	paths, err := r.dies.Glob(ctx, "fleet/stars/*/slag.json")
	if err != nil {
		return "", fmt.Errorf("the fleet's records could not be listed: %w", err)
	}
	for _, p := range paths {
		slag, err := r.dies.File(p).Contents(ctx)
		if err != nil {
			return "", fmt.Errorf("record %s could not be read: %w", p, err)
		}
		if checks.RecordRepo(slag) == key {
			return checks.StarOfRecordPath(p), nil
		}
	}
	return "", nil
}

// releasePlan derives what the star's release build produces: the binaries
// its Dockerfile copies out of release/, each from a ./cmd/<name> the tree
// must carry, and whether the module vendors. Every refusal is about the
// repository and settles as a could-not-run.
func (r *run) releasePlan(ctx context.Context, star, dockerfile string) (checks.ReleasePlan, string) {
	copies := buildlane.ReleaseCopies(dockerfile)
	vendored := false
	if entries, err := r.src.Entries(ctx); err == nil {
		vendored = slices.Contains(entries, "vendor/")
	}
	plan, err := checks.GoReleasePlan(star, copies, vendored)
	if err != nil {
		return checks.ReleasePlan{}, err.Error()
	}
	// A COPY with no main package behind it would ship a half image: say so
	// here, by name, instead of as the compiler's "directory not found".
	for _, b := range plan.Binaries {
		ok, err := r.src.Exists(ctx, "cmd/"+b.Name, dagger.DirectoryExistsOpts{ExpectedType: dagger.ExistsTypeDirectoryType})
		if err != nil {
			return checks.ReleasePlan{}, fmt.Sprintf("the tree could not be read for cmd/%s: %v", b.Name, err)
		}
		if !ok {
			return checks.ReleasePlan{}, fmt.Sprintf("the Dockerfile copies release/%s and the tree carries no cmd/%s to build it from", b.Name, b.Name)
		}
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
// THE BINARY IS A LAYER OF THE LANE (provision: `go install` at the pin, then
// on PATH). The old script ran `go install honnef.co/go/tools/cmd/staticcheck@latest`
// first — a compile of the whole analysis suite, on every atom, on every
// gate — and `@latest` meant the gate's check set was whatever honnef
// published that morning rather than what the pin declared. The version
// probe below checks the layer: its own exec under the DEFAULT Expect, so a
// lane that lost the binary is state 2. The engine ends the "CANNOT RUN when not
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
		return verdict(ctx, a, r.goBuilt(dir).
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
// A LAYER, LIKE staticcheck. The old script `go install`ed
// golang.org/x/vuln/cmd/govulncheck@latest on every run; the lane now installs
// it once at its pin, and the version probe is the check in its place, its own
// exec under the default Expect.
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
		out, code, err := output(ctx, r.goBuilt(dir).
			WithExec([]string{"govulncheck", "-version"}).
			WithExec([]string{"govulncheck", "./..."}, anyExit))
		if err != nil {
			return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
		}
		// govulncheck's 3 is "vulnerabilities found" and reads as 1; any
		// other non-zero with a network fault in it is the database not
		// answering, which AuditVerdict reads as could-not-run.
		exit := checks.GovulncheckExit(code)
		// AND ONLY THEN, THE ALLOWANCE. It is checked against a FINDINGS exit
		// alone — never a could-not-run — because a scan that did not complete
		// has found nothing to allow, and letting an allowance answer for it
		// would turn "the advisory database was unreachable" into a pass.
		// checks.SuppressedVulnReason fails closed on anything it cannot parse
		// and on any advisory not covered; see vulnallow.go for why this exists.
		if exit == 1 {
			if why, ok := checks.SuppressedVulnReason(out, time.Now()); ok {
				return checks.AllowedVulnVerdict(a, why, out)
			}
		}
		return checks.AuditVerdict(a, exit, out)
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
// THE CANARIES RUN FIRST AND DECIDE WHETHER THE REPORT IS A MEASUREMENT. A
// harness that cannot start the test child scores every mutant KILLED; the
// control is a module whose one mutant must LIVE (checks.GoMutationCanary). The
// bound it existed for — gremlins v0.6.0 mangled --test-cpu into one argv word,
// #7649 — is kept through the environment the child inherits: GOMAXPROCS caps
// the test binary, GOFLAGS=-p caps concurrent builds.
//
// AND A SECOND CONTROL, FOR A SECOND WAY TO BE WRONG. gremlins resolves a
// file's package from the PACKAGE CLAUSE, so a mutant in a `package main` is
// graded against the module root (gremlins#268, open since Jan 2026; fix open at
// #306). With no root package — 36 of the fleet's 38 Go repos — the run it
// grades against fails and the mutant reads KILLED: a FALSE GREEN this gate
// would publish. The package-main control is the same under-tested mutant in a
// main package with nothing at the module root, and a `go list` names the files
// it governs — the main packages BELOW the root, because a main package that IS
// the root resolves to itself and is graded correctly. It is EXPECTED to come
// back broken today, and while it does those
// mutants are not counted (checks.GoMutationVerdict). Nobody has to remember to
// undo that: the day a fixed gremlins lands the control answers OK and they
// count again.
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
// mutationDBLane names the mutation gate's own test-server binding. Any
// non-empty word works; this one reads in a service alias (db-mutation) and in
// the DSN the suite is handed.
const mutationDBLane = "mutation"

func goMutationIn(ctx context.Context, r *run, a checks.AtomDef, dir string) checks.Verdict {
	ctr := goDownload(r.gitReady(ctx, r.withBase(r.withDies(r.lane(checks.ImageGo)))), dir)
	// THE RECORD'S DATABASE, as go:test-race brings it: the DB-gated suites are
	// compiled for the coverage run and gremlins' (and serialised on the one
	// database), and the servers are bound. Without this every DB-touching line
	// read NOT COVERED by construction (foundry-tools#8608).
	// ITS OWN SERVER, because this lane runs BESIDE the complex checks rather
	// than after them, and go:test-race resets the same schema per test.
	ctr, dbs, scope := r.withTestDatabases(ctx, ctr, mutationDBLane)
	ctr, brokers, bscope := r.withTestBrokers(ctx, ctr, mutationDBLane)
	scope = scope + "\n" + bscope
	// The scope line is set on the verdict, not folded into the output: a pass
	// keeps no output (checks.VerdictOf), and the line is printed either way.
	//
	// A RUN THAT GRADED NOTHING RIDES BESIDE IT, for the same reason and only for
	// that case. It settles 0, so its reason would be discarded exactly where it
	// matters: a green that verified nothing, printed as an unqualified pass. The
	// reason's first line is the headline (GoMutationVerdict builds it that way)
	// and checks.GoMutationNothingGraded is the one string both ends agree on. A
	// PARTIALLY ungraded run is not lifted - its viable mutants were measured, and
	// the exclusion is in the summary the logs keep.
	settle := func(state int, reason string) checks.Verdict {
		v := checks.VerdictOf(a, state, a.ID+": "+reason)
		note := scope
		if state == 0 && strings.HasPrefix(reason, checks.GoMutationNothingGraded) {
			note += "\n" + a.ID + ": " + strings.SplitN(reason, "\n", 2)[0]
		}
		v.Reason = note + "\n" + v.Reason
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
		return settle(r.missingBase(noBase))
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
	if len(dbs) > 0 || len(brokers) > 0 {
		tags = []string{"-tags", checks.BuildTags(dbs, brokers)}
	}
	// NO CONFIG FILE IS FETCHED, AND NONE IS HONOURED. The knobs are the argv
	// below and the consts beside it; goMutationNoConfig is what stops the tree's
	// own .gomutants.yml joining them, and says why that is not optional.
	base := ctr

	// THE KEYS of the units this diff touches, and — under --reuse — which of
	// them a stored grading already answers (mutation_reuse.go). Those are not
	// graded again; with every unit answered, nothing below runs at all.
	keys, owner := goGradingKeys(ctx, base, dir, since, changed, tags)
	reused, reuseNote := r.reusable(ctx, "go", goMutationEngine(), keys)
	// A SAMPLED RUN GRADES COLD ANYWAY, and audits what it would have reused.
	var audited map[string]checks.ReusedGrading
	if r.audit {
		audited, reused = reused, nil
	}
	misses := checks.Misses(keys, reused)
	if len(reused) > 0 && len(misses) == 0 {
		return goFolded(settle, checks.GoMutationRun{Canary: checks.CanaryOK, MainCanary: checks.CanaryOK, Workers: goMutationWorkers},
			dir, reused, reuseNote, misses, owner, nil)
	}
	scopeArgs := goMutationCoverPackages(changed)
	mutateArgs := []string{"./..."}
	if len(reused) > 0 {
		scopeArgs = checks.MissPatterns(dir, misses)
		mutateArgs = scopeArgs
	}

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
	if len(dbs) > 0 || len(brokers) > 0 {
		coverArgs = append(coverArgs, "-tags", checks.BuildTags(dbs, brokers), "-p", "1")
	}
	covered := base.WithExec(append(coverArgs, scopeArgs...), anyExit)
	if _, err := covered.ExitCode(ctx); err != nil {
		return neverRan(err)
	}
	profile, _ := covered.File(path.Join("/src", dir, goMutationProfile)).Contents(ctx)

	// THE CANARY, in a module of its own.
	canary := checks.CanaryUnknown
	control := base.
		WithNewFile(mutationDir+"/canary/go.mod", checks.GoMutationCanaryMod).
		WithNewFile(mutationDir+"/canary/canary.go", checks.GoMutationCanaryCode).
		WithNewFile(mutationDir+"/canary/canary_test.go", checks.GoMutationCanaryTest).
		WithWorkdir(mutationDir+"/canary").
		WithExec([]string{"go", "test", "-cover", "-coverprofile", goMutationProfile, "./..."}, anyExit)
	if code, err := control.ExitCode(ctx); err == nil && code == 0 {
		canary = canaryVerdict(ctx, control, mutationDir+"/canary")
	}

	// THE PACKAGE-MAIN CONTROL, in a module of its own, SHAPED LIKE THE BUG:
	// one `package main` in a subdirectory and nothing at the module root. That
	// shape was measured, not assumed (2026-09-24, gremlins 0.6.0, this config,
	// the same code both ways): a main package AT the module root is graded
	// correctly and answers LIVED, so a control shaped like the harness canary
	// above is blind to #268. Only this shape sees it.
	mainCanary := checks.CanaryUnknown
	mainControl := base.
		WithNewFile(mutationDir+"/maincanary/go.mod", checks.GoMutationMainCanaryMod).
		WithNewFile(mutationDir+"/maincanary/cmd/tool/main.go", checks.GoMutationMainCanaryCode).
		WithNewFile(mutationDir+"/maincanary/cmd/tool/main_test.go", checks.GoMutationMainCanaryTest).
		WithWorkdir(mutationDir+"/maincanary").
		WithExec([]string{"go", "test", "-cover", "-coverprofile", goMutationProfile, "./..."}, anyExit)
	if code, err := mainControl.ExitCode(ctx); err == nil && code == 0 {
		mainCanary = canaryVerdict(ctx, mainControl, mutationDir+"/maincanary")
	}

	// WHICH FILES THAT CONTROL GOVERNS — THE TOOLCHAIN ANSWERS, NOT A GREP.
	// `go list` reports the package it actually resolved, so a testdata fixture
	// whose first line reads `package main` is not named and a build-tagged file
	// is placed by the same tags the cover step used. A grep would name the
	// fixture and widen the exclusion — the direction that hides mutants.
	// ParseGoMisgradedFiles then drops the module root's own main package, which
	// #268 resolves to correctly; THIS repo is the reason that filter exists —
	// its root is `package main`, and the first cut excluded 11 mutants of
	// atoms_go.go, three of them LIVED, on the pull that added the exclusion.
	// Failing to list them is carried, not swallowed: without the set there is
	// nothing to exclude with, and the verdict makes that a could-not-measure.
	misgraded := checks.GoMisgradedFiles{}
	var misgradedErr string
	listArgs := []string{"go", "list", "-e", "-f", checks.GoMainFilesFormat}
	if len(dbs) > 0 || len(brokers) > 0 {
		listArgs = append(listArgs, "-tags", checks.BuildTags(dbs, brokers))
	}
	switch out, code, err := output(ctx, base.WithExec(append(listArgs, "./..."), anyExit)); {
	case err != nil:
		misgradedErr = err.Error()
	case code != 0:
		misgradedErr = fmt.Sprintf("go list exited %d", code)
	default:
		misgraded = checks.ParseGoMisgradedFiles(out, path.Join("/src", dir))
	}

	// MUTATE.
	args := append([]string{"gomutants", "-output", goMutationReport,
		"-config", goMutationNoConfig,
		"-workers", strconv.Itoa(goMutationWorkers),
		"-disable", goMutationDisable,
		"-exclude-files", goMutationExclude,
		"-changed-since", since}, tags...)
	args = append(args, mutateArgs...)
	// THE SCOPE IS THE CHANGED LINES, and gomutants reads them itself off
	// `-changed-since` rather than being handed a file list. MEASURED
	// 2026-09-29 on ourea internal/gatejob at 5743c86 against 8d9f610: the
	// mutant population was gatejob.go 3083-3123 and watch.go 195-301 and
	// NOTHING ELSE — exactly the two edits, in two files of a 40-file package.
	//
	// diff.context=0 IS KEPT ANYWAY, and it is cheap insurance rather than a
	// measured need here. gremlins took each diff fragment as one contiguous
	// range from its first addition, so three lines of git context merged two
	// insertions six lines apart and dragged the untouched lines between them
	// into scope (kairos e138f50, 2026-09-18: a red on code the pull did not
	// write). gomutants was not observed doing that, but it reads the same git
	// and nothing about context=0 can widen a scope.
	//
	// THE BASELINE IS NEVER CACHED, and this is the knob that decided whether
	// gremlins was correct at all. It timed each mutant against a coverage
	// gather Go's test cache answered in 1.09s for a module whose suite runs
	// ~100s, making the per-mutant budget about eight seconds: MEASURED
	// 2026-09-18 on ourea aeb9cd9, killed 72 / TIMED OUT 79 / honest 1 — half
	// the population unanswered and the gate green over it. gomutants measures
	// its own baseline as a phase and states the ceiling it derived ("baseline
	// done (2.5s, ceiling: 25s)"), and -adaptive-timeout sizes each mutant off
	// per-test durations; -count=1 keeps every run under it a real one.
	//
	// AND gomutants' MEMORY CAP GETS A ps THAT ANSWERS IT. Each mutant's test
	// tree is meant to die at 2 GiB resident, read off `ps -o rss= -g <pgid>`
	// — a process-group selector on BSD that procps reads as a session, so
	// the cap read 0 and never fired, and a runaway mutant took the whole exec
	// down at its 16 GiB cgroup instead (internal/pgroupps has the
	// measurement). Prepended to PATH for this exec only.
	mutated := covered.
		WithFile(pgroupPSDir+"/ps", helperBinary("pgroupps")).
		WithEnvVariable("PATH", pgroupPSDir+":${PATH}", dagger.ContainerWithEnvVariableOpts{Expand: true}).
		WithEnvVariable("GOMAXPROCS", "1").
		WithEnvVariable("GOFLAGS", goMutationGoflags).
		WithEnvVariable("GIT_CONFIG_COUNT", "2").
		WithEnvVariable("GIT_CONFIG_KEY_0", "diff.relative").
		WithEnvVariable("GIT_CONFIG_VALUE_0", "true").
		WithEnvVariable("GIT_CONFIG_KEY_1", "diff.context").
		WithEnvVariable("GIT_CONFIG_VALUE_1", "0").
		WithExec(args, anyExit)
	// THE LOG COMES BACK WITH THE EXIT CODE, so a run that could not measure can
	// say why. outputBoth is what the Rust lane already uses for this; reading
	// only the exit code is what made a flaky unit test settle as an
	// unexplained "nothing was measured".
	log, status, err := outputBoth(ctx, mutated)
	if err != nil {
		return neverRan(err)
	}
	// gremlins writes no report when it has nothing to report; a read that
	// fails is that absence, and the verdict decides what it means.
	report, _ := mutated.File(path.Join("/src", dir, goMutationReport)).Contents(ctx)

	// NAME THE FILES FROM THE MODULE ROOT before anything reads them. gomutants
	// writes file_name relative to the common import-path prefix of the packages
	// it ran, so a module whose packages all live below cmd/<name> reports
	// "answer.go" and the classifier, looking in the module root, refuses the
	// whole report (checks.GoReportBase). The packages are listed with the
	// patterns and tags the run itself was given, so the base is the run's own.
	// A listing that fails leaves the report as it was: the classifier's own
	// refusal then speaks.
	classifyOn := mutated
	if report != "" {
		listPkgs := []string{"go", "list", "-e", "-f", checks.GoPackagesFormat}
		if tags := checks.BuildTags(dbs, brokers); tags != "" {
			listPkgs = append(listPkgs, "-tags", tags)
		}
		if out, code, err := output(ctx, base.WithExec(append(listPkgs, mutateArgs...), anyExit)); err == nil && code == 0 {
			if rel := checks.GoReportBase(out, path.Join("/src", dir)); rel != "" {
				if rebased, err := checks.RebaseGoReport([]byte(report), rel); err == nil {
					report = string(rebased)
					classifyOn = mutated.WithNewFile(path.Join("/src", dir, goMutationReport), report)
				}
			}
		}
	}

	// CLASSIFY, IN THE LANE, WHERE THE SOURCE IS. forge-testkit-go's gate
	// parses the tree to say which survivors no test could ever kill — a
	// mutant in a top-level const or var has no coverage block — and answers
	// them under -json for the scorer on the host to fold in. Its own exit
	// code is not the verdict here (this atom holds the canary and the timeout
	// budget); its stdout is. Nothing to write is nothing to classify.
	var classified, classifyErr string
	if report != "" {
		classified, classifyErr, _ = classify(ctx, classifyOn.WithExec([]string{"mutation-gate", "-report", goMutationReport, "-C", ".", "-json"}, anyExit))
	}

	return goFolded(settle, checks.GoMutationRun{
		Status: status, Log: log, Report: []byte(report), Profile: profile, Canary: canary, Workers: goMutationWorkers,
		Classified: []byte(classified), ClassifyErr: classifyErr,
		MainCanary: mainCanary, MisgradedFiles: misgraded, MisgradedFilesErr: misgradedErr,
	}, dir, reused, reuseNote, misses, owner, audited)
}

// goFolded settles a Go mutation run: what it graded, folded with what it
// reused (checks.GoMutationVerdictReusing — with nothing reused, the cold
// verdict exactly), and the gradings of the units it graded itself.
//
// THE FINDINGS COME FROM THE SCORE, NOT FROM THE REASON. checks.VerdictOf
// leaves them nil for this atom on purpose — FindingsOf does not parse the
// mutation report, because the report is a RENDERING of the score and reading
// it back would lose a distinction the score keeps (an ungraded LIVED mutant
// and a missed LIVED mutant render identically).
func goFolded(settle func(int, string) checks.Verdict, run checks.GoMutationRun, dir string,
	reused map[string]checks.ReusedGrading, note string, misses []checks.UnitKey, owner func(string) (string, bool),
	audited map[string]checks.ReusedGrading) checks.Verdict {
	list := checks.Reused(reused)
	f := checks.GoMutationVerdictReusing(run, dir, list)
	v := settle(f.State, f.Reason)
	// THE REUSE RIDES BESIDE THE VERDICT, like the scope line: a pass keeps no
	// output, and what a pass did not grade itself is what its reader needs.
	for _, line := range []string{note, checks.ReuseLine("go:mutation", list, len(list)+len(misses))} {
		if line != "" {
			v.Reason = line + "\n" + v.Reason
		}
	}
	v.Findings = f.Findings
	v.Gradings = checks.GoGradings(f.Fresh, dir, goMutationEngine(), checks.GoGradingTrusted(f.State, run.Canary), misses, owner)
	if len(audited) > 0 {
		v.Audit = checks.AuditGradings(checks.Reused(audited), v.Gradings)
		v.Reason = "go:mutation: audit — the " + strconv.Itoa(len(audited)) + " unit(s) a lookup answered were graded cold again: " + v.Audit + "\n" + v.Reason
	}
	return v
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
	// goMutationReport and goMutationProfile are what the run leaves in /src.
	goMutationReport  = "mutation-go.json"
	goMutationProfile = "mutation-cover.out"
	// pgroupPSDir holds the ps the mutation exec finds first (pgroupps).
	pgroupPSDir = "/opt/pgroupps"
	// goMutationWorkers is gomutants' -workers, and it is a CORRECTNESS knob,
	// not a speed one. Parallel mutants slow each other's tests down and trip
	// their own timeouts, and a TIMED OUT mutant answers neither killed nor
	// survived — it leaves the population, quietly. MEASURED 2026-09-29, ourea
	// internal/gatejob, the same 28-mutator run both ways:
	//
	//	-workers 16   killed 70, lived 22, TIMED OUT 15    148s
	//	-workers  4   killed 83, lived 24, TIMED OUT  0     69s
	//
	// All 15 timeouts were contention. Nine of them had a comparable mutant in
	// the second run and every one of the nine came back KILLED; not one killed
	// mutant flipped the other way. 4 is the number that measured clean, and it
	// is the number gremlins' own config had settled on for the same reason.
	goMutationWorkers = 4
	// goMutationNoConfig IS WHAT MAKES THE ARGV THE WHOLE CONFIGURATION, and it
	// closes a hole this lane shipped with.
	//
	// gomutants DOES read a config file: -config defaults to `.gomutants.yml`
	// and config.Load reads it from the WORKDIR — the tree under check — while
	// answering Default(),nil when it is absent. That silence is why the flip
	// landed without anyone noticing: no repo carries one (measured, 0 of 77
	// checkouts on origin/main), so the lane behaved exactly as if no such file
	// existed. It would have kept behaving that way until the first repo wrote
	// four lines.
	//
	// A FLAG ONLY WINS WHERE THERE IS A FLAG. Config.ApplyFlags runs after Load,
	// so every knob this lane passes overrides the file — and every knob it does
	// NOT pass is taken from the file unopposed. `only` is the sharp one, because
	// it disables every mutator it does not name. MEASURED 2026-09-29, this
	// argv, the canary fixture:
	//
	//	no file                    24 types enabled, 2 mutants found
	//	only: [INVERT_BITWISE]      1 type  enabled, 0 mutants found, exit 0
	//	  + -config /dev/null      24 types enabled, 2 mutants found
	//
	// A population of zero has no survivors, so the gate goes green over a repo
	// that graded nothing. exclude-calls, coverpkg, integration, test-flags and
	// mutants.enabled are unopposed the same way.
	//
	// THE RULE IS THE RETIRED .gremlins.yaml's RULE — A REPO HAS NO SAY — and it
	// used to be enforced by WRITING the canonical config over whatever the tree
	// carried. The knobs moved onto this argv, which is the better shape, but the
	// enforcement has to move with them: /dev/null is a file Load reads to empty
	// bytes with no error, so it keeps its own defaults and the tree's file is
	// never opened. Not a path that merely does not exist — absence is what was
	// indistinguishable from safety in the first place.
	//
	// AND POINTING IT AT A CANONICAL FILE INSTEAD BUYS NOTHING, which was worth
	// checking rather than assuming, because the obvious repair for a gate whose
	// config is /dev/null is to give it a real one. MEASURED 2026-09-29 against
	// `gomutants -h` at v0.6.1: EVERY knob the fleet's canonical config sets has
	// a flag — -adaptive-timeout, -timeout-coefficient, -timeout-margin,
	// -detect-equivalent, -disable — and two of the three beyond -disable are
	// already the tool's own defaults (adaptive-timeout true, coefficient 10). So
	// a file would have added a git clone of forge-testkit-go, a read that can
	// fail, and a CANNOT RUN branch for when the door is unreachable, to express
	// what this argv already expresses. The fleet's other rulesets answer the
	// same way: hadolint's config is foundry-tools' at a path outside /src,
	// staticcheck's and clippy's check sets are named on their command lines, and
	// cosmic-ray's TOML is generated by checks.CosmicRayConfig. The canonical
	// file in forge-testkit-go is for a human running gomutants by hand there —
	// and fleet:stop-justifications grades ITS disable set too.
	goMutationNoConfig = "/dev/null"
	// goMutationExclude keeps generated Go out of the mutant population —
	// vendored code, dagger's codegen, protobuf stubs and kubebuilder's
	// zz_generated. See goMutation for the measurement.
	goMutationExclude = `^vendor/|(^|/)dagger\.gen\.go$|\.pb\.go$|(^|/)zz_generated`
)

// goMutationDisable is the mutator set, by exclusion. gomutants ships 28
// operators; the first four perturb a NUMERIC LITERAL by one, and on this fleet's
// Go they generate mutants no correct test can kill.
//
// MEASURED 2026-09-29 on ourea internal/gatejob — 25 survivors with all 28
// enabled, 5 with these off, and the 20 that left were ALL of one shape:
//
//	18  `0` -> `1`/`-1` on the discarded value of `return 0, err`
//	                    and `return false, 0`. Go's contract says the other
//	                    returns are unspecified when the error is non-nil;
//	                    a test asserting them pins what the API does not
//	                    promise.
//	 2  `64` -> `63`/`65` in `strconv.ParseFloat(q, 64)`. PROVEN equivalent,
//	                    not argued: ParseFloat takes the 32-bit path only
//	                    for bitSize 32, so 63, 64 and 65 return bit-identical
//	                    results for every input including MaxFloat64.
//	 2  `1e6` -> `999999.0` as a nanocore divisor under int64 truncation.
//	                    Identical output at every realistic reading.
//
// The five that remain are real test gaps, and gremlins' five mutators
// never GENERATED any of them. So this set is 19 operators wider than the
// gate had before it, not narrower — the point is that it is STATED, which
// is the same reason the retired .gremlins.yaml listed gremlins' five.
//
// INCREMENT_DECREMENT IS NOT IN HERE and must not be: it mutates `i++` to
// `i--`, which is a real operator on a real statement, and it was one of
// gremlins' five. These four are its numeric-literal namesakes.
//
// THE SET IS RATIFIED DATA, NOT A LITERAL A SESSION CHOSE, and since
// 2026-09-29 something checks that. checks.RatifiedMutators carries one row
// per operator with Rob's words and the shape each one generates;
// TestTheDisabledOperatorsAreExactlyTheRatifiedSet refuses this string and
// that table disagreeing in either direction, and fleet:stop-justifications
// grades any .gomutants.yaml in any repository's tree against the same
// table. A fifth operator now needs a signed row before it can reach either
// surface — which is the rule every other suppression in the fleet already
// answered to, and this one did not.
//
// AND SINCE 2026-10-06 THE STRING IS THE TABLE'S, not a literal beside it: the
// low-value classes (boundary, arithmetic, loop control, error wrap) left every
// lane on Rob's decision, in each tool's words, so this is the Go rows joined
// and nothing a session types.
var goMutationDisable = strings.Join(checks.SkippedMutators(checks.MutatorGo), ",")

// canaryVerdict runs a control and reads its JSON REPORT, never its stdout.
//
// THE REPORT IS THE ONLY SURFACE WITH A CONTRACT. The old canary matched the
// literal string "Killed: 0, Lived: 1" in gremlins' summary line, which tied a
// control — the one thing in this lane that decides whether anything was
// measured at all — to a tool's human-readable formatting AND to the exact
// mutant COUNT. gomutants prints a multi-line block and found 2 mutants in the
// same fixture (an ARITHMETIC_BASE and a RETURN_ZERO; RETURN_ZERO alone since
// ARITHMETIC_BASE left the set on 2026-10-06), so that match would
// silently answer `unknown` forever: a control that cannot fail is not a control.
//
// The honest verdict is unchanged and is now stated as a shape rather than a
// string: the fixture's mutants are all under-tested on purpose, so a run that
// GRADED them kills none and survives at least one. Killing them means the
// runner is not running what it thinks it is.
func canaryVerdict(ctx context.Context, control *dagger.Container, dir string) string {
	run := control.WithExec([]string{"gomutants",
		"-config", goMutationNoConfig,
		"-workers", "1",
		"-cache=off",
		"-disable", goMutationDisable,
		"-output", goMutationReport,
		"./..."}, anyExit)
	// ONE RETURN, AND THE REPORT IS THE ONLY THING THAT CARRIES A VERDICT. A run
	// that did not exit 0 never has its report read, so report stays empty and
	// GoMutationCanary answers unknown — the same answer it gives an empty or
	// unparseable one, because they are the same fact: nothing was graded. Saying
	// that once, there, beats saying it three times here.
	//
	// THE THREE EARLY RETURNS THIS REPLACED WERE UNKILLABLE, every one, and the
	// tool this diff installs is what said so (2026-09-29: 13 mutants in this
	// package, 6 survivors, all six in these branches). Nothing compares
	// checks.CanaryUnknown anywhere — GoMutationVerdict tests only for CanaryOK
	// and CanaryBroken — so `return CanaryUnknown` and `return ""` are the same
	// program, and a branch whose body is deleted still falls through to the same
	// answer. The fix is to stop naming it, not to forgive the mutants.
	//
	// EXIT 0 OR IT DID NOT RUN, measured rather than guessed (gomutants v0.6.1,
	// these exact fixtures): the canary with TWO survivors exits 0, while an
	// unbuildable package and a package that does not exist both exit 1.
	// Survivors are not an error to this tool unless -threshold-efficacy asks,
	// and the controls do not ask. The first cut read `code > 1`, which tolerated
	// the exit 1 that means could-not-run and leaned on the report read to fail
	// instead — right answer, false reasoning.
	var report string
	if code, err := run.ExitCode(ctx); err == nil && code == 0 {
		report, _ = run.File(dir + "/" + goMutationReport).Contents(ctx)
	}
	return checks.GoMutationCanary(report)
}

// lastLine is the tail of a tool's output — the line a human reads first.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
