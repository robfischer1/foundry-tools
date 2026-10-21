package atoms

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// THE GO SUITE, once, for the three atoms that run it: go:test (the commit's
// unit run), go:test-race (the push's race + live-database run) and
// diff-coverage (which reads the profile the race run writes). It is the
// body of what the chain called goTestIn, as execs in the module's directory of
// the container the adapter gives the atom.
//
// THE CONTAINER IS THE ADAPTER'S, AND IT HOLDS THE SERVERS. A test server is a
// Dagger service bound to the lane container (atom_env.go); the atom reads the
// same tree facts the binding was decided on (GoTestServers) to say which tags
// to compile, and inherits the DSN the binding put in its environment.

// GoSuite runs one module's suite: race + the record's live databases when
// race is set (the push), the plain unit run when it is not (the commit).
//
// THE PUSH'S RUN WRITES A COVERAGE PROFILE (checks.GoCoverProfile), and that is
// the only difference the flag makes: -race already forces -covermode=atomic,
// the exit code and the output's FAIL lines are go test's either way, and the
// file sits outside the tree. NO -coverpkg, so each package's coverage is its
// own tests' — exactly the profile gomutants gathers, which is what keeps the
// two NOT COVERED signals agreeing.
// suiteScrub blanks the executor's per-atom variables for the unit suite.
var suiteScrub = []string{"ATOM_WORK=", "ATOM_OUT=", "ATOM_TREE="}

func GoSuite(ctx context.Context, in Input, a checks.AtomDef, dir string, race bool) checks.Verdict {
	return goSuite(ctx, in, a, dir, race, nil)
}

// GoSuiteFor is the push's race run for a caller who asked for it by hand
// (dev:go-test-live): the same servers, tags and -p 1, with the caller's own
// arguments folded in (SuiteArgs). It writes no coverage profile and skips the
// parallel build warm-up, which exist for go:diff-coverage's sake.
func GoSuiteFor(ctx context.Context, in Input, a checks.AtomDef, dir string, caller SuiteArgs) checks.Verdict {
	return goSuite(ctx, in, a, dir, true, &caller)
}

func goSuite(ctx context.Context, in Input, a checks.AtomDef, dir string, race bool, caller *SuiteArgs) checks.Verdict {
	cwd := GoModuleRoot(in, dir)
	if v, ok := Provision(ctx, in, a, cwd, "go", "mod", "download"); !ok {
		return v
	}
	counts, code := in.run(ctx, Cmd{Dir: cwd, Name: "go", Args: []string{"list", "-f", "{{len .TestGoFiles}}{{len .XTestGoFiles}}", "./..."}})
	if code < 0 {
		return CouldNotStart(a, counts)
	}
	if code != 0 {
		// A module that will not even list is a red about the module, and the
		// old atom filed it as FINDINGS on purpose: the tests cannot be counted,
		// so they cannot be shown to exist.
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - go list ./... failed, so the tests cannot be counted: "+LastLine(counts))
	}
	if !checks.GoHasTestFiles(counts) {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - no test file in any package; nothing is built without tests")
	}
	// THE REGEN TESTS RUN INSTEAD OF SKIPPING where the tree has packages
	// gravity generated: a stale regeneration is a finding, not just a hand edit.
	plan, err := goGravityPlan(in)
	if errors.Is(err, errGoGravityRead) {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+err.Error())
	}
	if err != nil {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - "+err.Error())
	}
	var env []string
	for _, g := range plan.Regens {
		env = append(env, g.Env+"=1")
	}
	if len(plan.Regens) > 0 {
		bin, set, err := stagedGravity(plan.Rev)
		if err == nil && bin == "" {
			return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the tree pins gravity %s and none is staged: the pod provisions the prebuilts of checks.GravityPrebuilt (%s), so a new rev needs its prebuilt pushed to the registry (checks.GravityBlobURL) and a tools.toml [tool.%s]", a.ID, plan.Rev, strings.Join(checks.GravityNeeds(), ", "), checks.GravityTool(plan.Rev)))
		}
		var sum string
		if err == nil {
			sum, err = gravitySum(bin)
		}
		if err != nil {
			return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the staged gravity could not be hashed: "+err.Error())
		}
		if set != "" {
			env = append(env, "GRAVITY="+set)
		}
		env = append(env, checks.GravitySHA256Env+"="+sum)
	}
	// THE WIT SNAPSHOT TESTS RUN INSTEAD OF SKIPPING where the tree holds snapshots
	// (<pkg>/wit/SOURCE.json): the wasm-tools they need is the lane's.
	wits, err := witSnapshotPlan(in)
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+err.Error())
	}
	for _, w := range wits {
		env = append(env, w.Env+"=1")
	}
	var witScope string
	if len(wits) > 0 {
		witScope = checks.WitSnapshotScope(wits) + "\n"
	}
	// THE SUITE RUNS AS A DEVELOPER RUNS IT: the pod's per-atom variables name
	// the atom's work directory, which tests that provoke an unwritable
	// TMPDIR would otherwise be redirected around (WorkDir). An empty value
	// reads as unset to every os.Getenv(...) != "" test.
	env = append(env, suiteScrub...)
	args := []string{"test"}
	scope := "unit suite: no race detector and no database — go:test-race runs those at the push"
	if race {
		var dbs []checks.TestDB
		var brokers []checks.TestBroker
		var caches []checks.TestCache
		var kubes []checks.TestKube
		dbs, brokers, caches, kubes, scope = GoTestServers(ctx, in, cwd)
		// A TREE THAT CARRIES live_kube NEEDS THE API SERVER THE LANE BINDS. The
		// adapter binds one and sets the three variables; the pod binds one for an atom whose Needs name service:etcd and service:kube-apiserver.
		// Compiling the suite with nothing behind it would fail every test on a
		// missing URL, so the atom says it cannot run, and names what is absent.
		for _, k := range kubes {
			if missing := k.MissingEnv(in.getenv); len(missing) > 0 {
				v := checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+k.NotBound(missing))
				v.Reason = scope + "\n" + v.Reason
				return v
			}
		}
		args = append(args, "-race")
		var callerNote string
		var callerTags []string
		if caller != nil {
			callerTags = caller.Tags
			callerNote = caller.Note(dbs, brokers, caches, kubes)
			scope += callerNote
		}
		// `-p 1` for EITHER kind. The databases share one server and the suites
		// reset its schema; one broker serves every package and its isolation
		// unit is the topic. Parallel packages break both the same way.
		var tagArgs []string
		if word := UnionTags(checks.BuildTags(dbs, brokers, caches, kubes), callerTags); word != "" {
			tagArgs = []string{"-tags", word}
			args = append(args, tagArgs...)
			if caller == nil || !caller.P {
				args = append(args, "-p", "1")
			}
		}
		if caller == nil {
			warmBuildCache(ctx, in, cwd, tagArgs)
			args = append(args, "-coverprofile", checks.GoCoverProfile)
		}
	}
	if caller != nil {
		args = append(args, caller.Rest...)
	}
	if caller == nil || !caller.Packages {
		args = append(args, "./...")
	}
	out, code := in.run(ctx, Cmd{Dir: cwd, Name: "go", Args: args, Env: env, Both: true})
	if code < 0 {
		return CouldNotStart(a, out)
	}
	v := checks.VerdictOf(a, code, out)
	v.Reason = scope + "\n" + plan.Scope + "\n" + witScope + v.Reason
	return v
}

// warmBuildCache is the race run's parallel build (DEF-B4-PERF-3). `-p 1` caps
// the go tool's concurrent build and link actions as well as the test binaries,
// so on a cold build cache the suite compiled every package on one core (glaucus,
// 16 cores: 112 s serial, 28 s at the machine's width). This pass has the suite's
// flags less `-p`: `-exec true` compiles and links each test binary and runs
// none, so no database is touched and no test result is cached, and `-cover` makes
// it the same compile as the profile run's. Its outcome is dropped on purpose; a
// tree that does not build builds no better here and says so, in go test's words,
// in the run that follows.
func warmBuildCache(ctx context.Context, in Input, cwd string, tagArgs []string) {
	args := append([]string{"test", "-race"}, tagArgs...)
	args = append(args, "-cover", "-exec", "true", "./...")
	in.run(ctx, Cmd{Dir: cwd, Name: "go", Args: args})
}

// goTagRead is the tree's single-tag build constraints, read as the chain read
// them: one recursive grep in the module, whose exit 1 is "no match", an answer.
func goTagRead(ctx context.Context, in Input, cwd string) (out string, code int) {
	return in.run(ctx, Cmd{Dir: cwd, Name: "grep", Args: []string{"-rhoE", `^//go:build [A-Za-z0-9_]+$`, "--include=*_test.go", "."}})
}

// GoTestServers is which test servers a module's suites need, and the scope
// line the atom prints either way: the databases (checks.TestDBsFor over the
// tree's tags), the brokers (checks.SelectBrokers over the tags and the Kafka
// flows the tree's own code states), the caches (checks.TestCachesFor over
// the tags) and the API servers (checks.SelectKubes over the tags). The tree
// states all four; no record is asked. The adapter binds
// the same servers off the same reads.
func GoTestServers(ctx context.Context, in Input, cwd string) ([]checks.TestDB, []checks.TestBroker, []checks.TestCache, []checks.TestKube, string) {
	out, code := goTagRead(ctx, in, cwd)
	var dbs []checks.TestDB
	var caches []checks.TestCache
	var dbLine, cacheLine string
	if code > 1 || code < 0 {
		dbLine = checks.TestDBScope(nil, nil, "the tree's build tags could not be read")
		cacheLine = checks.TestCacheScope(nil, "the tree's build tags could not be read")
	} else {
		tags := checks.GoBuildTags(out)
		dbs = checks.TestDBsFor(tags)
		caches = checks.TestCachesFor(tags)
		if len(dbs) == 0 {
			dbLine = checks.TestDBScope(nil, nil, "no test file sits behind a tag the fleet names")
		} else {
			dbLine = checks.TestDBScope(dbs, checks.UncompiledTags(tags, dbs, checks.TestBrokers, checks.TestCaches, checks.TestKubes), "")
		}
		if len(caches) == 0 {
			cacheLine = checks.TestCacheScope(nil, "no test file sits behind a tag the fleet names")
		} else {
			cacheLine = checks.TestCacheScope(caches, "")
		}
	}
	flows, flowsErr := goTreeFlows(in)
	var tagsErr error
	if code < 0 {
		tagsErr = errors.New(out)
	}
	brokers, brokerLine := checks.SelectBrokers(checks.BrokerReads{
		Tags: out, TagsErr: tagsErr, TagsCode: code,
		Flows: flows, FlowsErr: flowsErr,
	})
	kubes, kubeLine := checks.SelectKubes(out, tagsErr, code)
	return dbs, brokers, caches, kubes, dbLine + "\n" + brokerLine + "\n" + cacheLine + "\n" + kubeLine
}

// goTreeFlows counts the non-family Kafka flows the tree's own code states. No
// constant table is fetched: an unresolved name is still a flow, and the count
// is all the broker decision reads.
func goTreeFlows(in Input) (int, error) {
	if in.FilesErr != nil {
		return 0, fmt.Errorf("the tree would not enumerate: %w", in.FilesErr)
	}
	var wanted []string
	for _, p := range in.Files {
		if checks.KafkaSourceFile(p) {
			wanted = append(wanted, p)
		}
	}
	files, bad := checks.ReadAll(in.tree().read, wanted)
	if bad != "" {
		return 0, errors.New(bad)
	}
	flows, _ := checks.KafkaFlows(files, nil)
	return checks.NonFamilyFlows(flows), nil
}

// errGoGravityRead marks a failure to read the tree, as opposed to a tree that
// reads and says something unusable.
var errGoGravityRead = errors.New("the tree's provenance could not be read")

// goGravityPlan holds a tree's gravity-generated packages to regenerate
// byte-identical (F6, foundry-tools#15441): the plan (checks.PlanGravity) over
// every provenance.json and regen test the tree carries. The gravity binary on
// PATH is the adapter's to stage off the same plan; the variables that make a
// missing generator a failure ride the go test exec.
func goGravityPlan(in Input) (checks.GravityPlan, error) {
	all, err := in.tree().files()
	if err != nil {
		return checks.GravityPlan{}, fmt.Errorf("%w: %v", errGoGravityRead, err)
	}
	files := map[string]string{}
	var tests []string
	var readErrs []error
	for _, p := range all {
		switch path.Base(p) {
		case "provenance.json":
			body, rerr := in.tree().read(p)
			readErrs = append(readErrs, rerr)
			files[p] = body
		case checks.RegenTestFile:
			tests = append(tests, p)
		}
	}
	if err := errors.Join(readErrs...); err != nil {
		return checks.GravityPlan{}, fmt.Errorf("%w: %v", errGoGravityRead, err)
	}
	return checks.PlanGravity(files, tests)
}

// witSnapshotPlan finds the packages whose WIT snapshots the suite re-extracts.
func witSnapshotPlan(in Input) ([]checks.WitSnapshot, error) {
	all, err := in.tree().files()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errGoGravityRead, err)
	}
	files := map[string]string{}
	var readErrs []error
	for _, p := range all {
		if path.Base(p) == checks.WitSnapshotSource && path.Base(path.Dir(p)) == "wit" {
			body, rerr := in.tree().read(p)
			readErrs = append(readErrs, rerr)
			files[p] = body
		}
	}
	if err := errors.Join(readErrs...); err != nil {
		return nil, fmt.Errorf("%w: %v", errGoGravityRead, err)
	}
	return checks.WitSnapshots(files), nil
}
