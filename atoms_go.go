package main

import (
	"context"
	"path"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/releasekit"
)

// THE GO LANE, AS A CHAIN: go:mutation, which stays on Dagger (the mutation
// lane's reuse, re-ask and memo are the run's). Every other go atom is a file
// (atoms/go), run by the adapter (atom_adapter.go) in the environment atom_env.go
// and atom_env_go.go give it; this file keeps the helpers they and the chain
// share.

func init() {
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

// recordOfRepo is the record whose repo is key: its name and its JSON, or ""
// for both when no entry names key (releasekit.RecordOf, which the release
// atoms name a star by too).
func (r *run) recordOfRepo(ctx context.Context, key string) (string, string, error) {
	return releasekit.RecordOf(ctx, runTree{r}, key)
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

// lastLine is the tail of a tool's output — the line a human reads first.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
