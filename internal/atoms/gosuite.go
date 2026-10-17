package atoms

import (
	"context"
	"errors"
	"fmt"
	"path"

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
		dbs, brokers, scope = GoTestServers(ctx, in, cwd)
		args = append(args, "-race")
		// `-p 1` for EITHER kind. The databases share one server and the suites
		// reset its schema; one broker serves every package and its isolation
		// unit is the topic. Parallel packages break both the same way.
		if len(dbs) > 0 || len(brokers) > 0 {
			args = append(args, "-tags", checks.BuildTags(dbs, brokers), "-p", "1")
		}
		args = append(args, "-coverprofile", checks.GoCoverProfile)
	}
	args = append(args, "./...")
	out, code := in.run(ctx, Cmd{Dir: cwd, Name: "go", Args: args, Env: env, Both: true})
	if code < 0 {
		return CouldNotStart(a, out)
	}
	v := checks.VerdictOf(a, code, out)
	v.Reason = scope + "\n" + plan.Scope + "\n" + v.Reason
	return v
}

// goTagRead is the tree's single-tag build constraints, read as the chain read
// them: one recursive grep in the module, whose exit 1 is "no match", an answer.
func goTagRead(ctx context.Context, in Input, cwd string) (out string, code int) {
	return in.run(ctx, Cmd{Dir: cwd, Name: "grep", Args: []string{"-rhoE", `^//go:build [A-Za-z0-9_]+$`, "--include=*_test.go", "."}})
}

// GoTestServers is which test servers a module's suites need, and the scope
// line the atom prints either way: the databases (checks.TestDBsFor over the
// tree's tags) and the brokers (checks.SelectBrokers over the tags and the
// Kafka flows the tree's own code states). The tree states both; no record is
// asked. The adapter binds the same servers off the same reads.
func GoTestServers(ctx context.Context, in Input, cwd string) ([]checks.TestDB, []checks.TestBroker, string) {
	out, code := goTagRead(ctx, in, cwd)
	var dbs []checks.TestDB
	var dbLine string
	if code > 1 || code < 0 {
		dbLine = checks.TestDBScope(nil, nil, "the tree's build tags could not be read")
	} else {
		tags := checks.GoBuildTags(out)
		dbs = checks.TestDBsFor(tags)
		if len(dbs) == 0 {
			dbLine = checks.TestDBScope(nil, nil, "no test file sits behind a tag the fleet names")
		} else {
			dbLine = checks.TestDBScope(dbs, checks.UncompiledTags(tags, dbs, checks.TestBrokers), "")
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
	return dbs, brokers, dbLine + "\n" + brokerLine
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
