package main

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE GO LANE, AS CHAINS: go:release and go:mutation, which stay on Dagger (the
// build, publish and cast lanes consume the release compile; the mutation lane's
// reuse, re-ask and memo are the run's). Every other go atom is a file
// (atoms/go), run by the adapter (atom_adapter.go) in the environment atom_env.go
// and atom_env_go.go give it; this file keeps the helpers they and the two
// chains share.

func init() {
	register("go:release", goRelease)
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

// testDatabases is which databases the suites need, and the scope line. It binds
// no service, so a caller can ask BEFORE it knows whether anything will run
// (go:mutation asks before its change set is known to be empty).
func (r *run) testDatabases(ctx context.Context, ctr *dagger.Container) ([]checks.TestDB, string) {
	// grep exits 1 for no match, which is an answer; 2 and up is grep failing.
	out, code, err := output(ctx, ctr.WithExec([]string{
		"grep", "-rhoE", `^//go:build [A-Za-z0-9_]+$`, "--include=*_test.go", ".",
	}, anyExit))
	if err != nil || code > 1 {
		return nil, checks.TestDBScope(nil, nil, "the tree's build tags could not be read")
	}
	tags := checks.GoBuildTags(out)
	dbs := checks.TestDBsFor(tags)
	if len(dbs) == 0 {
		return nil, checks.TestDBScope(nil, nil, "no test file sits behind a tag the fleet names")
	}
	return dbs, checks.TestDBScope(dbs, checks.UncompiledTags(tags, dbs, checks.TestBrokers), "")
}

// bindTestDatabases binds each database's server to ctr under scope's alias.
func bindTestDatabases(ctr *dagger.Container, dbs []checks.TestDB, scope string) *dagger.Container {
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
		// NON-DURABLE, through the entrypoint: checks.TestDB.ServerArgs carries
		// the flags and the measurements. WithDefaultArgs, never WithExec — a
		// service built on a WithExec never reads ready (TestBroker.StartArgs).
		svc := base.WithExposedPort(5432).
			WithDefaultArgs(d.ServerArgs()).
			AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})
		ctr = ctr.WithServiceBinding(d.AliasFor(scope), svc).WithEnvVariable(d.Env, d.DSNFor(scope))
	}
	return ctr
}

// testBrokers is the broker-gated suites' two reads, binding nothing
// (testDatabases says why the two halves are apart).
func (r *run) testBrokers(ctx context.Context, ctr *dagger.Container) ([]checks.TestBroker, string) {
	// NO BRANCH LIVES HERE. This makes the reads and hands them over as
	// facts; every decision is checks.SelectBrokers', where a unit test can reach
	// it. The first cut guarded in place and the mutation lane graded those guards
	// NOT COVERED and LIVED on the pull that added them (#196, six survivors) — a
	// guard no test can execute is a guard that is not there.
	out, code, tagsErr := output(ctx, ctr.WithExec([]string{
		"grep", "-rhoE", `^//go:build [A-Za-z0-9_]+$`, "--include=*_test.go", ".",
	}, anyExit))
	flows, flowsErr := r.treeFlows(ctx)
	return checks.SelectBrokers(checks.BrokerReads{
		Tags: out, TagsErr: tagsErr, TagsCode: code,
		Flows: flows, FlowsErr: flowsErr,
	})
}

// treeFlows counts the non-family Kafka flows the tree's own code states. No
// constant table is fetched: an unresolved name is still a flow, and the
// count is all the broker decision reads.
func (r *run) treeFlows(ctx context.Context) (int, error) {
	files, err := r.kafkaSources(ctx)
	if err != nil {
		return 0, err
	}
	flows, _ := checks.KafkaFlows(files, nil)
	return checks.NonFamilyFlows(flows), nil
}

// bindTestBrokers binds each broker to ctr under scope's alias.
func bindTestBrokers(ctr *dagger.Container, brokers []checks.TestBroker, scope string) *dagger.Container {
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
	return ctr
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
	ctr, err := r.releaseBuild(ctx, plan, "")
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
// 2026-09-19): not copier-templated, no .copier-answers.yml, and a catalog entry at
// fleet/repos/hephaestus.json saying repo rob/hephaestus — the door
// already routes its lanes by that record, so the release build reads the
// same name. The clone URL is the door's (--repo) or the hook's (--origin);
// a run given neither, and no answers file, is not a star: an error here is
// "not a star", never "could not read".
//
// AN ANSWERS FILE WITH NO service_name FALLS BACK TO THE RECORD TOO. Only
// the go, rust and python templates ask for one; the frontend template asks
// for repo_name, which is not the star (demeter's is demeter-mcp, its catalog
// entry fleet/repos/demeter.json). Measured on calliope #46, 2026-09-19: the push's
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

// starOfRepo is the repo whose catalog entry says repo == key, or "" when no
// entry does. The entries are read at their one home (r.dies,
// fleet/repos/<name>.json, which every fleet repo has whether or not flux
// deploys it); a tree that cannot be listed is an error, not "no star".
func (r *run) starOfRepo(ctx context.Context, key string) (string, error) {
	star, _, err := r.recordOfRepo(ctx, key)
	return star, err
}

// recordOfRepo is starOfRepo with the entry itself: the record's name and its
// JSON, or "" for both when no entry names key.
func (r *run) recordOfRepo(ctx context.Context, key string) (string, string, error) {
	paths, err := r.dies.Glob(ctx, "fleet/repos/*.json")
	if err != nil {
		return "", "", fmt.Errorf("the fleet's records could not be listed: %w", err)
	}
	for _, p := range paths {
		slag, err := r.dies.File(p).Contents(ctx)
		if err != nil {
			return "", "", fmt.Errorf("record %s could not be read: %w", p, err)
		}
		if checks.RecordRepo(slag) == key {
			return checks.StarOfRecordPath(p), slag, nil
		}
	}
	return "", "", nil
}

// forkRecord is the name of the record that declares this repository a fork
// of an upstream (checks.RecordIsFork), or "" — the planner's Tree.Fork.
//
// EVERY DOUBT ANSWERS "NOT A FORK". No clone URL, no record, a record that
// will not read: the tree takes the fleet's whole bar. Standing an atom down
// is the one direction a read fault must never push, because a fork verdict
// passes and a missed one only grades more.
func (r *run) forkRecord(ctx context.Context) string {
	key := checks.RepoKey(r.repo)
	if key == "" {
		key = checks.RepoKey(r.origin)
	}
	if key == "" {
		return ""
	}
	// THE RECORD NAMED FOR THE REPO IS READ FIRST. Most records are named for
	// their repo (forks/gravity is fleet/repos/gravity.json) and every gate
	// asks this, so the common case is one read; a repo whose record is named
	// otherwise falls through to the scan.
	if _, name, ok := strings.Cut(key, "/"); ok {
		if slag, err := r.dies.File("fleet/repos/" + name + ".json").Contents(ctx); err == nil && checks.RecordRepo(slag) == key {
			return forkName(name, slag)
		}
	}
	name, slag, err := r.recordOfRepo(ctx, key)
	if err != nil {
		return ""
	}
	return forkName(name, slag)
}

// forkName is name when slag declares a fork, else "".
func forkName(name, slag string) string {
	if !checks.RecordIsFork(slag) {
		return ""
	}
	return name
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
//
// version "" IS THE GATE'S COMPILE and stamps nothing, so its exec is the same
// one it always was. The build lane passes the stamp tag's version, which
// exists only once the commit it builds does, and so compiles once more with
// the version linked in (foundry-tools#18676: every star reported "dev").
func (r *run) releaseBuild(ctx context.Context, plan checks.ReleasePlan, version string) (*dagger.Container, error) {
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
	// THE TREE ONLY, NO .git (laneRelease): the gate's go:release and the build
	// lane's Release() both land here, and the engine answers the second from
	// the first only if their mounts are the same bytes. A first-ask gate run
	// carries no CA_REASK; a re-ask after a could-not-run does, on purpose,
	// and that keyed-afresh compile is the one the build lane must not reuse.
	base := r.laneRelease(checks.ImageGo)
	if plan.Vendored {
		base = inModule(base, ".")
	} else {
		base = goDownload(base, ".")
	}
	module := ""
	if version != "" {
		// go.mod is there (the root-module check above); a body that names no
		// module fails ReleaseStamp's module check below.
		gomod, _, _ := fileIfPresent(ctx, r.src, "go.mod")
		module = checks.GoModulePath(gomod)
		if _, err := checks.ReleaseStamp(module, version); err != nil {
			return nil, err
		}
	}
	ctr := base.WithEnvVariable("CGO_ENABLED", "0")
	for _, b := range plan.Binaries {
		ctr = ctr.WithExec(checks.GoReleaseStampedArgs(b, plan.Vendored, module, version), anyExit)
	}
	return ctr, nil
}

// lastLine is the tail of a tool's output — the line a human reads first.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
