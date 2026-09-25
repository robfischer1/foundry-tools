package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE DIES LANE'S TESTS, against the paper engine (engine_fake_test.go).
//
// Six atoms over one shape, one pinned opa and one built bundle. The split
// that matters is the file's own: opa-test and admission-dogfood grade the
// SOURCE, data-keys and canary-visibility grade the ARTIFACT, and the gap
// between them is the measured fail-open a source-only gate cannot see. Each
// atom gets its happy path, every decision it makes about what opa said, and
// the engine-error path.

var diesAtoms = []string{
	"dies:opa-test", "dies:admission-dogfood", "dies:data-keys",
	"dies:canary-visibility", "dies:contracts", "dies:schema", "dies:findings",
	"dies:canonical",
}

// diesBuiltAContainer reports whether anything pulled an image. Named for this
// file rather than generically: every lane's tests share one package.
func diesBuiltAContainer() bool {
	for _, q := range engine.chains() {
		if strings.Contains(q, "container{from(") {
			return true
		}
	}
	return false
}

// diesTree is everyLaneTree with paths added and removed; a prefix in drop
// removes the whole subtree, which is how a marker is taken away.
func diesTree(add map[string]string, drop ...string) map[string]string {
	out := map[string]string{}
	for k, v := range everyLaneTree {
		out[k] = v
	}
	for _, d := range drop {
		for k := range out {
			if k == d || strings.HasPrefix(k, d) {
				delete(out, k)
			}
		}
	}
	for k, v := range add {
		out[k] = v
	}
	return out
}

// withOpa scripts the pinned binary answering for itself. Every dies atom that
// fetches opa needs it, because the version is CHECKED rather than assumed.
func withOpa() {
	engine.stdout(`"opa","version"`, "Version: "+checks.OpaVersion+"\nBuild Commit: c0ffee\nGo Version: go1.24\n")
}

// diesData is a data.json that satisfies every required root, with chaos
// curating the canary.
const diesData = `{
  "authz_audience": {"star_only": {"chaos": ["chaos_probe", "chaos_burn"], "hades": ["gate"]}},
  "authz_grants": {"a": 1},
  "authz_meta": {"built": "yes"},
  "path_grants": {"p": ["r"]},
  "subject_aliases": {"rob": "rob"}
}`

func opaEvalJSON(value string) string {
	return `{"result":[{"expressions":[{"value":` + value + `,"text":"x"}]}]}`
}

// ---- the shared shape ----

// TWO MARKERS, BOTH REQUIRED. Either alone is ambiguous: policy/ turns up in
// more than one repo and fleet/stars/ is a name a fleet inventory could take.
func TestDiesShapeIsAbsentWithoutBothMarkers(t *testing.T) {
	for _, tc := range []struct {
		name string
		tree map[string]string
	}{
		{"no policy/ at all", diesTree(nil, "policy")},
		{"no fleet/ at all", diesTree(nil, "fleet")},
		{"policy/ without a .manifest", diesTree(map[string]string{"policy/admission/x.rego": ""}, "policy/.manifest")},
		{"fleet/ without stars/", diesTree(map[string]string{"fleet/roster.md": ""}, "fleet/stars")},
	} {
		for _, id := range diesAtoms {
			engine.reset()
			engine.withTree(tc.tree)
			withOpa()
			wantState(t, runAtom(t, id, ""), 0, id+": ABSENT", "not the policy die's source", "policy/.manifest and fleet/stars/")
			if diesBuiltAContainer() {
				t.Errorf("%s (%s): an absent shape must not build a container", id, tc.name)
			}
		}
	}
}

// A root listing that could not be READ is not an answer about the tree's
// shape, so it is a 2 rather than the ABSENT above.
func TestDiesShapeRefusesWhenTheTreeCannotBeRead(t *testing.T) {
	for _, id := range diesAtoms {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.fail("{directory{entries}}", "the directory would not evaluate")
		wantState(t, runAtom(t, id, ""), 2, "the repository root could not be read", "would not evaluate")

		engine.reset()
		engine.withTree(everyLaneTree)
		engine.fail(`directory(path:"fleet"){entries}`, "gone")
		wantState(t, runAtom(t, id, ""), 2, "fleet/ is in the root listing but could not be read", "gone")
	}
}

// ---- the pinned opa client, shared by four atoms ----

// THE PIN IS PART OF THE QUESTION: rego's semantics are a property of the
// binary, so a suite graded by another major answers a different question.
func TestDiesOpaClientIsPinnedFetchedAndProbed(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	withOpa()
	engine.stdout(`"opa","test"`, "PASS: 12/12\n")
	wantState(t, runAtom(t, "dies:opa-test", ""), 0)

	c := engine.chain(`"opa","test","policy/","-v"`, "exitCode")
	if !strings.Contains(c, checks.ImageFleet) {
		t.Errorf("the dies lane runs in the fleet image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withFile", `path:"/usr/local/bin/opa"`, "permissions:493"},
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withExec", "expect:ANY", `args:["opa","test","policy/","-v"]`},
	)
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("dies:opa-test must not read GATE_BASE:\n%s", c)
	}
	if engine.chain(`http(url:"`+checks.OpaURL+`")`, "id") == "" {
		t.Errorf("opa's release URL is fetched and its file is the one placed:\n%v", engine.chains())
	}
	// The version probe is its own exec and carries the tool's exit code back.
	if !hasCall(engine.chain(`"opa","version"`, "exitCode"), "withExec", `args:["opa","version"]`, "expect:ANY") {
		t.Errorf("the version probe must read opa's own exit:\n%v", engine.chains())
	}
}

func TestDiesOpaClientRefusesEveryProvisioningFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		script  func()
		needles []string
	}{
		{"the release URL has no such asset", func() { engine.fail("opa_linux_amd64_static", "404") },
			[]string{"could not be fetched", checks.OpaVersion}},
		{"the probe never ran", func() { engine.fail(`"opa","version"`, "engine went away") },
			[]string{"the opa version probe never ran", "engine went away"}},
		{"on disk but will not run", func() { engine.exitCode(`"opa","version"`, 126) },
			[]string{"opa is on disk but does not run (exit 126)"}},
		{"the wrong major answers", func() { engine.stdout(`"opa","version"`, "Version: 0.60.0\n") },
			[]string{"the rego semantics are not the pinned ones", "0.60.0"}},
		{"a prerelease is not the pin", func() { engine.stdout(`"opa","version"`, "Version: "+checks.OpaVersion+"-rc1\n") },
			[]string{"the rego semantics are not the pinned ones"}},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		withOpa()
		tc.script()
		v := runAtom(t, "dies:opa-test", "")
		wantState(t, v, 2, append(tc.needles, "A policy suite that never ran is not a policy suite that passed.")...)
	}
}

// ---- dies:opa-test ----

// A ZERO-TEST RUN IS REFUSED: `opa test` over a tree with no assertion exits 0,
// which renders as a clean suite and is not one.
func TestDiesOpaTestFoldsOpasCodesAndRefusesAZeroTestRun(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    int
		out     string
		state   int
		needles []string
	}{
		{"a real suite", 0, "data.authz test_hides_curated: PASS\nPASS: 12/12\n", 0, nil},
		{"the LAST summary line is the count", 0, "PASS: 3/3\nPASS: 41/41\n", 0, nil},
		{"nothing was examined", 0, "PASS: 0/0\n", 2, []string{"REFUSING a zero-test run", "nothing was examined"}},
		{"no summary at all", 0, "no test files found\n", 2, []string{"REFUSING a zero-test run"}},
		{"opa's load error", 1, "rego_parse_error: unexpected eof\n", 1, []string{"the rego suite did not come back clean", "rego_parse_error"}},
		{"opa's failing assertion", 2, "FAIL: data.authz.test_x\nPASS: 11/12\n", 1, []string{"the rego suite did not come back clean", "FAIL: data.authz.test_x"}},
		{"a code opa does not use", 5, "killed\n", 2, []string{"opa exited 5", "neither a clean suite (0)"}},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		withOpa()
		engine.exitCode(`"opa","test"`, tc.code)
		engine.stdout(`"opa","test"`, tc.out)
		wantState(t, runAtom(t, "dies:opa-test", ""), tc.state, tc.needles...)
	}
}

func TestDiesOpaTestCannotRunWhenTheSuiteNeverRan(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	withOpa()
	engine.fail(`"opa","test"`, "exit code: 137: OOMKilled")
	wantState(t, runAtom(t, "dies:opa-test", ""), 2, "the atom never ran", "OOMKilled")
}

// ---- dies:admission-dogfood ----

func TestDiesAdmissionDogfoodAsksTheDomainAboutOurOwnStar(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	withOpa()
	engine.stdout(`"opa","eval"`, opaEvalJSON(`[]`))
	wantState(t, runAtom(t, "dies:admission-dogfood", ""), 0)

	c := engine.chain(`"opa","eval"`, "exitCode")
	wantCalls(t, c, []string{"withExec", "expect:ANY",
		`args:["opa","eval","-d","policy/admission","-i","tests/fixtures/ouranos-self.json","data.admission.deny","--format","json"]`})
	// GO READS THE RESULT, NOT jq AND NOT python3: the lane images are not
	// promised to carry either.
	for _, q := range engine.chains() {
		if strings.Contains(q, `"jq"`) || strings.Contains(q, `"python3","-c"`) {
			t.Errorf("the deny set is read by encoding/json, not a tool:\n%s", q)
		}
	}
}

func TestDiesAdmissionDogfoodRefusesWithoutADomainOrAnInput(t *testing.T) {
	// policy/ is proved present by the shape, but the admission DOMAIN is a
	// separate question — and with no input there is no claim to make.
	engine.reset()
	engine.withTree(diesTree(map[string]string{"policy/authz/x.rego": ""}, "policy/admission"))
	withOpa()
	wantState(t, runAtom(t, "dies:admission-dogfood", ""), 2,
		"policy/admission is absent", "no admission domain to ask")

	engine.reset()
	engine.withTree(diesTree(nil, "tests/fixtures/ouranos-self.json"))
	withOpa()
	wantState(t, runAtom(t, "dies:admission-dogfood", ""), 2,
		"tests/fixtures/ouranos-self.json is absent", "no own-star shape to submit")

	// "not there" and "could not look" are different answers, and Glob is what
	// tells them apart.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`glob(pattern:"tests/fixtures/ouranos-self.json")`, "scan interrupted")
	withOpa()
	wantState(t, runAtom(t, "dies:admission-dogfood", ""), 2,
		"the tree could not be scanned for tests/fixtures/ouranos-self.json", "scan interrupted")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`directory(path:"policy"){entries}`, "unreadable")
	withOpa()
	wantState(t, runAtom(t, "dies:admission-dogfood", ""), 2, "policy/ could not be listed", "unreadable")
}

// A SHAPE THIS ATOM CANNOT READ IS A 2, never a deny count nobody computed.
func TestDiesAdmissionDogfoodReadsTheDenySet(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    int
		out     string
		fail    string
		state   int
		needles []string
	}{
		{"admitted", 0, opaEvalJSON(`[]`), "", 0, nil},
		{"two denies", 0, opaEvalJSON(`["star lacks meta.name","verb not curated"]`), "", 1,
			[]string{"deny count = 2", "star lacks meta.name", "verb not curated"}},
		{"an object deny is rendered, not dropped", 0, opaEvalJSON(`[{"msg":"nope"}]`), "", 1,
			[]string{"deny count = 1", `{"msg":"nope"}`}},
		{"eval did not complete", 3, "error: undefined function", "", 2,
			[]string{"opa eval did not complete", "undefined function"}},
		{"an undefined rule is not an empty deny set", 0, `{}`, "", 2,
			[]string{"a shape this atom cannot read", "no deny set to count"}},
		{"not JSON at all", 0, "panic: runtime error", "", 2, []string{"a shape this atom cannot read"}},
		{"the eval never ran", 0, "", `"opa","eval"`, 2, []string{"the atom never ran", "engine went away"}},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		withOpa()
		engine.exitCode(`"opa","eval"`, tc.code)
		engine.stdout(`"opa","eval"`, tc.out)
		if tc.fail != "" {
			engine.fail(tc.fail, "engine went away")
		}
		wantState(t, runAtom(t, "dies:admission-dogfood", ""), tc.state, tc.needles...)
	}
}

// ---- the built bundle, shared by data-keys and canary-visibility ----

// THE SOURCE TREE IS NOT A PROXY FOR THE ARTIFACT: `opa test policy/` passes on
// a tree whose BUILT BUNDLE is empty, so the bundle is built and opened.
func TestDiesBundleBuildsWithTheRevisionAndOpensTheDataDocument(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	withOpa()
	engine.stdout(`"git","rev-parse"`, "  f00dcafe\n")
	engine.stdout(`"tar","xzOf"`, diesData)
	wantState(t, runAtom(t, "dies:data-keys", ""), 0)

	c := engine.chain(`"tar","xzOf"`, "exitCode")
	wantCalls(t, c,
		[]string{"withExec", `args:["opa","build","-b","policy/","-o","/tmp/dies-bundle.tar.gz","--revision","f00dcafe","--ignore","*_test.rego"]`},
		[]string{"withExec", "expect:ANY", `args:["tar","xzOf","/tmp/dies-bundle.tar.gz","/data.json"]`},
	)
	// A BUILD THAT DID NOT COMPLETE IS PROVISIONING: the build exec carries the
	// DEFAULT Expect so its failure is a Dagger error, not a finding.
	if hasCall(c, "withExec", `"opa","build"`, "expect:ANY") {
		t.Errorf("opa build must run under the default Expect:\n%s", c)
	}
	// git is made safe first, because the process here is root and the clone is
	// not its own.
	if !hasCall(engine.chain(`"git","rev-parse"`, "exitCode"), "withExec", `"safe.directory"`) {
		t.Errorf("the revision read must come after git config safe.directory:\n%v", engine.chains())
	}
}

// THE REVISION IS BEST-EFFORT, exactly as the shell's `|| echo unknown` was: a
// tree git cannot resolve a HEAD in builds as "unknown" rather than not at all.
func TestDiesBundleStampsUnknownWhenTheRevisionCannotBeRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script func()
	}{
		{"git exits non-zero", func() { engine.exitCode(`"git","rev-parse"`, 128) }},
		{"git prints nothing", func() { engine.stdout(`"git","rev-parse"`, "  \n") }},
		{"the read never ran", func() { engine.fail(`"git","rev-parse"`, "no git in the image") }},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		withOpa()
		tc.script()
		engine.stdout(`"tar","xzOf"`, diesData)
		wantState(t, runAtom(t, "dies:data-keys", ""), 0)
		if engine.chain(`"--revision","unknown"`) == "" {
			t.Errorf("%s: the build must still run, stamped unknown:\n%v", tc.name, engine.chains())
		}
	}
}

// A BUILD THAT DID NOT COMPLETE IS A 2; A BUNDLE WITH NO data.json IS A 1 — the
// second IS the measured fail-open, and calling it "could not run" would file
// the finding as an absence.
func TestDiesBundleSeparatesABrokenBuildFromAnEmptyArtifact(t *testing.T) {
	for _, id := range []string{"dies:data-keys", "dies:canary-visibility"} {
		engine.reset()
		engine.withTree(everyLaneTree)
		withOpa()
		engine.fail(`"opa","build"`, "exit code: 1: rego_type_error")
		wantState(t, runAtom(t, id, ""), 2, "opa build did not produce a bundle",
			"no artifact to interrogate", "rego_type_error")

		engine.reset()
		engine.withTree(everyLaneTree)
		withOpa()
		engine.exitCode(`"tar","xzOf"`, 2)
		engine.stdout(`"tar","xzOf"`, "tar: /data.json: Not found in archive")
		wantState(t, runAtom(t, id, ""), 1, "NO data.json member at all",
			"every verb visible to every principal", "Not found in archive")
	}
}

// ---- dies:data-keys ----

func TestDiesDataKeysGradesTheBuiltDataDocument(t *testing.T) {
	for _, tc := range []struct {
		name    string
		data    string
		state   int
		needles []string
	}{
		{"every root, and the roster survived", diesData, 0, nil},
		{"a root that is not there", `{"authz_grants":{"a":1},"authz_meta":{"b":1},"path_grants":{"c":1},"subject_aliases":{"d":1}}`, 1,
			[]string{"missing/empty", "authz_audience", "reads ONLY files named data.json"}},
		{"EMPTY COUNTS AS MISSING", `{"authz_audience":{},"authz_grants":{},"authz_meta":{},"path_grants":{},"subject_aliases":{}}`, 1,
			[]string{"authz_audience", "authz_grants", "authz_meta", "path_grants", "subject_aliases"}},
		{"the silent fail-open, arriving exactly as described", `{}`, 1, []string{"missing/empty"}},
		{"a shape this atom cannot read", `not json`, 2,
			[]string{"a shape this atom cannot read", "no data document to grade"}},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		withOpa()
		engine.stdout(`"tar","xzOf"`, tc.data)
		wantState(t, runAtom(t, "dies:data-keys", ""), tc.state, tc.needles...)
	}
}

// ---- dies:canary-visibility ----

// THE CANARY IS READ OFF THE ROSTER, NOT NAMED — a named canary goes stale the
// day its verb leaves the roster, and the gate then goes red on a roster that
// is MORE correct. Twice now (post-mortem 2026-08-22).
func TestDiesCanaryVisibilityAssertsBothDirections(t *testing.T) {
	const canary = "chaos_probe"
	for _, tc := range []struct {
		name    string
		session string
		star    string
		state   int
		needles []string
	}{
		{"still hidden", `["search"]`, `["search","` + canary + `"]`, 0, nil},
		{"the fail-open", `["search","` + canary + `"]`, `["search","` + canary + `"]`, 1,
			[]string{"VISIBLE to a session principal", "canary: " + canary}},
		{"a session principal seeing nothing is also wrong", `[]`, `["search","` + canary + `"]`, 1,
			[]string{"VISIBLE to a session principal"}},
		{"curation narrowed both audiences", `["search"]`, `["search"]`, 1,
			[]string{"INVISIBLE to a star principal", "curation narrowed both audiences"}},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		withOpa()
		engine.stdout(`"tar","xzOf"`, diesData)
		engine.stdout(`probe-session.json`, opaEvalJSON(tc.session))
		engine.stdout(`probe-star.json`, opaEvalJSON(tc.star))
		wantState(t, runAtom(t, "dies:canary-visibility", ""), tc.state, tc.needles...)
	}
}

// THE PROBE INPUT IS A FILE, NOT A REDIRECT: `--stdin-input` needed a `<` and a
// `<` needs a shell, which rule 7 does not allow.
func TestDiesCanaryVisibilityProbesBothPrincipalsAgainstTheBundle(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	withOpa()
	engine.stdout(`"tar","xzOf"`, diesData)
	engine.stdout(`probe-session.json`, opaEvalJSON(`["search"]`))
	engine.stdout(`probe-star.json`, opaEvalJSON(`["search","chaos_probe"]`))
	wantState(t, runAtom(t, "dies:canary-visibility", ""), 0)

	for _, principal := range []string{"session", "star"} {
		c := engine.chain(`probe-`+principal+`.json`, "exitCode")
		wantCalls(t, c,
			[]string{"withNewFile", `path:"/tmp/probe-` + principal + `.json"`, `\"type\":\"` + principal + `\"`, `chaos_probe`},
			[]string{"withExec", "expect:ANY",
				`args:["opa","eval","-b","/tmp/dies-bundle.tar.gz","-i","/tmp/probe-` + principal + `.json","--format","json","data.authz.visible.allowed"]`},
		)
	}
}

// AN EMPTY chaos ROW IS ITSELF THE FAILURE: nothing curated means the roster
// did not survive the build.
func TestDiesCanaryVisibilityFailsWithNoChaosRow(t *testing.T) {
	for _, data := range []string{
		`{"authz_audience":{"star_only":{"hades":["gate"]}},"authz_grants":{"a":1},"authz_meta":{"b":1},"path_grants":{"c":1},"subject_aliases":{"d":1}}`,
		`{"authz_audience":{"star_only":{"chaos":[]}},"authz_grants":{"a":1},"authz_meta":{"b":1},"path_grants":{"c":1},"subject_aliases":{"d":1}}`,
		`{}`,
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		withOpa()
		engine.stdout(`"tar","xzOf"`, data)
		wantState(t, runAtom(t, "dies:canary-visibility", ""), 1,
			"chaos has no star_only row", "the roster did not survive the build")
		if engine.chain("probe-session.json") != "" {
			t.Errorf("with no canary there is nothing to probe:\n%v", engine.chains())
		}
	}
}

func TestDiesCanaryVisibilityRefusesAProbeThatDidNotEvaluate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		principal string
		script    func(match string)
	}{
		{"the session probe never ran", "session", func(m string) { engine.fail(m, "engine went away") }},
		{"the star probe exited non-zero", "star", func(m string) {
			engine.exitCode(m, 1)
			engine.stdout(m, "error: bundle not found")
		}},
		{"the star probe answered a shape this atom cannot read", "star", func(m string) {
			engine.stdout(m, `{"result":[]}`)
		}},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		withOpa()
		engine.stdout(`"tar","xzOf"`, diesData)
		engine.stdout(`probe-session.json`, opaEvalJSON(`["search"]`))
		engine.stdout(`probe-star.json`, opaEvalJSON(`["search","chaos_probe"]`))
		tc.script("probe-" + tc.principal + ".json")
		wantState(t, runAtom(t, "dies:canary-visibility", ""), 2,
			"the "+tc.principal+"-principal probe against the built bundle did not evaluate")
	}
}

// ---- dies:contracts ----

// NINE MUST FAIL AND FOUR MUST PASS, and the door is probed before the live
// check: a gate that cannot fail is a gate that is not there.
func TestDiesContractsProvesDetectionThenProbesTheDoorThenChecks(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	scriptContractFixtures()
	wantState(t, runAtom(t, "dies:contracts", ""), 0)

	nFail, nPass := 0, 0
	for _, f := range diesContractFixtures {
		c := engine.chain(`"--contract","`+f.name+`"`, "exitCode")
		if c == "" {
			t.Fatalf("fixture %s never ran:\n%v", f.name, engine.chains())
		}
		// THE PROBE SCRIPT LANDS AFTER THE FIXTURES, so editing it does not
		// invalidate thirteen cached fixture runs that never read it.
		if strings.Contains(c, "dies-door-probe.py") {
			t.Errorf("fixture %s must not carry the door probe in its cache key:\n%s", f.name, c)
		}
		wantCalls(t, c,
			[]string{"withExec", `args:["uv","--version"]`},
			[]string{"withExec", "expect:ANY", `"--manifest","tests/contracts/fixtures.toml","--contract","` + f.name + `"`},
			// THROUGH uv, UNCONDITIONALLY: the image's system python3 is not
			// promised to be >= 3.11.
			[]string{"withExec", `"uv","run","--no-project","--quiet","--with","tomli>=2.0","python3"`},
		)
		if f.expectFail {
			nFail++
		} else {
			nPass++
		}
	}
	if nFail != 9 || nPass != 4 {
		t.Errorf("the detection proof is nine fixtures and four controls, got %d/%d", nFail, nPass)
	}

	door := engine.chain(`"python3","/tmp/dies-door-probe.py"`, "exitCode")
	wantCalls(t, door,
		[]string{"withNewFile", `path:"/tmp/dies-door-probe.py"`},
		[]string{"withExec", "expect:ANY", `"python3","/tmp/dies-door-probe.py"`},
	)
	if engine.chain(`"tools/check_contracts.py"]`, "exitCode") == "" {
		t.Errorf("the live manifest is never checked:\n%v", engine.chains())
	}
}

// scriptContractFixtures makes the nine detected and leaves the four controls,
// the door probe and the live check on the default exit 0.
func scriptContractFixtures() {
	for _, f := range diesContractFixtures {
		if f.expectFail {
			engine.exitCode(`"--contract","`+f.name+`"`, 1)
		}
	}
}

func TestDiesContractsFailsWhenTheProofNoLongerHolds(t *testing.T) {
	// A fixture the checker stopped detecting.
	engine.reset()
	engine.withTree(everyLaneTree)
	scriptContractFixtures()
	engine.exitCode(`"--contract","expired_retiring"`, 0)
	wantState(t, runAtom(t, "dies:contracts", ""), 1,
		"the fixtures no longer prove the gate detects",
		"fixture 'expired_retiring' PASSED - the checker no longer detects it")

	// A control the checker started failing — a checker that simply fails
	// everything is not a checker that detects.
	engine.reset()
	engine.withTree(everyLaneTree)
	scriptContractFixtures()
	engine.exitCode(`"--contract","holding_pending"`, 1)
	wantState(t, runAtom(t, "dies:contracts", ""), 1,
		"control 'holding_pending' FAILED - the checker invents divergence")

	// A broken proof stops before the door is ever probed.
	if engine.chain(`"python3","/tmp/dies-door-probe.py"`) != "" {
		t.Errorf("the door must not be probed once the proof has failed:\n%v", engine.chains())
	}
}

// AN UNREACHABLE DOOR IS A 2: a copy that could not be FETCHED is not a copy
// that DISAGREES, and reporting one as the other sends a reader to reconcile
// lists that may be identical.
func TestDiesContractsSeparatesADeadDoorFromADisagreement(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	scriptContractFixtures()
	engine.exitCode(`"python3","/tmp/dies-door-probe.py"`, 2)
	engine.stdout(`"python3","/tmp/dies-door-probe.py"`, "urlopen error timed out")
	wantState(t, runAtom(t, "dies:contracts", ""), 2,
		"the door's raw API is unreachable", "not a copy that agrees", "timed out")

	engine.reset()
	engine.withTree(everyLaneTree)
	scriptContractFixtures()
	engine.fail(`"python3","/tmp/dies-door-probe.py"`, "engine went away")
	wantState(t, runAtom(t, "dies:contracts", ""), 2,
		"the door reachability probe never ran", "engine went away")

	// The live check's own answer stays the checker's.
	engine.reset()
	engine.withTree(everyLaneTree)
	scriptContractFixtures()
	engine.exitCode(`"tools/check_contracts.py"]`, 1)
	engine.stdout(`"tools/check_contracts.py"]`, "::error::hades lags the authority")
	wantState(t, runAtom(t, "dies:contracts", ""), 1, "hades lags the authority")
}

func TestDiesContractsRefusesWithoutTheCheckerTheFixturesOrTheManifest(t *testing.T) {
	for _, tc := range []struct {
		path   string
		needle string
	}{
		{"tools/check_contracts.py", "there is no checker to run"},
		{"tests/contracts/fixtures.toml", "a gate that cannot prove it detects is a gate that is not there"},
		{"contracts/contracts.toml", "there is no live manifest to check"},
	} {
		engine.reset()
		engine.withTree(diesTree(nil, tc.path))
		wantState(t, runAtom(t, "dies:contracts", ""), 2, tc.path+" is absent", tc.needle)
	}

	// The provisioning probe is `uv --version` under the default Expect, so an
	// image without uv is a could-not-run rather than a finding.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"uv","--version"`, "executable file not found")
	wantState(t, runAtom(t, "dies:contracts", ""), 2,
		"the lagging fixture never ran", "executable file not found")
}

// ---- dies:findings ----

// findingsPaths are what dies:findings requires. They are NOT added to
// everyLaneTree on purpose: a .py in the shared fixture widens the python lane's
// asserted population and reds TestPythonForgeTestkitLintsEachModeOverItsOwnPopulation.
// A shared fixture is a shared assertion; this atom's needs are its own.
var findingsPaths = map[string]string{
	"schema/findings.schema.json": "{}",
	"tools/check_findings.py":     "",
}

// The checker is THE TREE'S, not embedded, and it runs through uv with
// jsonschema — the dies:contracts shape rather than dies:schema's, because
// foundry-dies owns the contract and so should own the checker.
func TestDiesFindingsRunsTheTreesCheckerThroughUv(t *testing.T) {
	engine.reset()
	engine.withTree(diesTree(findingsPaths))
	wantState(t, runAtom(t, "dies:findings", ""), 0)

	c := engine.chain("tools/check_findings.py", "exitCode")
	wantCalls(t, c,
		[]string{"withExec", `args:["uv","--version"]`},
		[]string{"withExec", "expect:ANY", `"--with","jsonschema>=4.20","python3","tools/check_findings.py"`},
	)
	// NOTHING IS EMBEDDED. An embedded copy would make this module the author of
	// a rule about another repo's data, which is the coupling check_contracts.py's
	// own header rejects.
	if strings.Contains(c, "withNewFile") {
		t.Errorf("dies:findings wrote a script into the container; the tree's own checker is the tool:\n%s", c)
	}
}

// THE SCRIPT'S EXIT CODE IS THE VERDICT, unmapped. 0/1/2 out of
// check_findings.py are pass/findings/could-not-run here, which is the same
// three-state vocabulary the schema it validates makes normative. A switch
// between them would be a place for the two to disagree.
func TestDiesFindingsPassesTheExitCodeStraightThrough(t *testing.T) {
	for code, want := range map[int]int{0: 0, 1: 1, 2: 2} {
		engine.reset()
		engine.withTree(diesTree(findingsPaths))
		engine.exitCode(`"python3","tools/check_findings.py"`, code)
		wantState(t, runAtom(t, "dies:findings", ""), want)
	}
}

// A tree missing either half is a COULD-NOT-RUN naming which half, never a pass:
// there is nothing to validate, or nothing to validate it with.
func TestDiesFindingsCannotRunWithoutASchemaOrAChecker(t *testing.T) {
	for _, c := range []struct{ drop, names string }{
		{"schema/findings.schema.json", "schema/findings.schema.json is absent"},
		{"tools/check_findings.py", "tools/check_findings.py is absent"},
	} {
		// diesTree applies drop BEFORE add, so the add map must not carry the
		// path under test or it puts it straight back.
		add := map[string]string{}
		for k, v := range findingsPaths {
			if k != c.drop {
				add[k] = v
			}
		}
		engine.reset()
		engine.withTree(diesTree(add, c.drop))
		wantState(t, runAtom(t, "dies:findings", ""), 2, c.names)
	}
}

// A tree that is not the policy die's source is ABSENT, not a finding — the same
// gate every dies atom carries.
func TestDiesFindingsIsAbsentOutsideTheDie(t *testing.T) {
	engine.reset()
	engine.withTree(diesTree(findingsPaths, "policy"))
	v := runAtom(t, "dies:findings", "")
	wantState(t, v, 0, "ABSENT")
	if diesBuiltAContainer() {
		t.Error("dies:findings pulled an image for a tree it does not grade")
	}
}

// ---- dies:schema ----

func TestDiesSchemaValidatesThroughUvWithTheEmbeddedValidator(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "dies:schema", ""), 0)

	c := engine.chain("dies-schema.py", "exitCode")
	wantCalls(t, c,
		[]string{"withExec", `args:["uv","--version"]`},
		// jsonschema COMES THROUGH uv, NOT pip: the lane images are uv-managed,
		// where a `python3 -m pip install` is refused outright.
		[]string{"withExec", `"--with","jsonschema>=4.20","python3","-c","import jsonschema"`},
		[]string{"withNewFile", `path:"/tmp/dies-schema.py"`, "Draft202012Validator"},
		[]string{"withExec", "expect:ANY", `"jsonschema>=4.20","python3","/tmp/dies-schema.py"`},
	)
	if hasCall(c, "withExec", `"import jsonschema"`, "expect:ANY") {
		t.Errorf("the resolver probe is provisioning and takes the default Expect:\n%s", c)
	}
	for _, q := range engine.chains() {
		if strings.Contains(q, `"pip"`) {
			t.Errorf("jsonschema must not come through pip:\n%s", q)
		}
	}
}

func TestDiesSchemaRefusesWithoutEitherSchema(t *testing.T) {
	for _, tc := range []struct {
		path   string
		needle string
	}{
		{"schema/slag.schema.json", "there is no payload to validate"},
		{"schema/slag-v2.schema.json", "the v2 records cannot be discriminated"},
	} {
		engine.reset()
		engine.withTree(diesTree(nil, tc.path))
		wantState(t, runAtom(t, "dies:schema", ""), 2, tc.path+" is absent", tc.needle)
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`glob(pattern:"schema/slag.schema.json")`, "scan interrupted")
	wantState(t, runAtom(t, "dies:schema", ""), 2,
		"the tree could not be scanned for schema/slag.schema.json", "scan interrupted")
}

// REFUSING TO REPORT A VALIDATED SCHEMA THAT WAS NEVER VALIDATED is the whole
// of the two provisioning probes.
func TestDiesSchemaReadsTheValidatorsExit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		script  func()
		state   int
		needles []string
	}{
		{"a record that does not satisfy the schema", func() {
			engine.exitCode(`"python3","/tmp/dies-schema.py"`, 1)
			engine.stderr(`"python3","/tmp/dies-schema.py"`, "::error file=fleet/stars/x/x.slag::meta/name")
		}, 1, []string{"meta/name"}},
		{"the validator crashed", func() {
			engine.exitCode(`"python3","/tmp/dies-schema.py"`, 2)
			engine.stderr(`"python3","/tmp/dies-schema.py"`, "Traceback")
		}, 2, []string{"Traceback"}},
		{"uv is not in the image", func() { engine.fail(`"uv","--version"`, "executable file not found") },
			2, []string{"the atom never ran", "executable file not found"}},
		{"the resolver could not reach an index", func() {
			engine.fail(`"-c","import jsonschema"`, "No solution found when resolving")
		}, 2, []string{"the atom never ran", "No solution found"}},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		tc.script()
		wantState(t, runAtom(t, "dies:schema", ""), tc.state, tc.needles...)
	}
}

// ---- dies:canonical ----

// THE BYTES, NOT THE SHAPE. A record that is its own canonical form passes; one
// with a key out of sorted order, a different indent, unescaped non-ASCII or
// no trailing LF is findings naming the record and the re-emit that fixes it;
// one that is not JSON is findings too. No container is ever built: the read
// is the Directory's own.
func TestDiesCanonicalGradesEveryRecordsForm(t *testing.T) {
	canonical := "{\n  \"apiVersion\": 3,\n  \"tools\": {\n    \"build\": {\n      \"binaries\": [\n        \"clio\"\n      ]\n    },\n    \"calypso\": {\n      \"secret_path\": \"/fleet/clio\"\n    }\n  }\n}\n"
	engine.reset()
	engine.withTree(diesTree(map[string]string{
		"fleet/stars/clio/slag.json":  canonical,
		"fleet/stars/hades/slag.json": "{\n  \"apiVersion\": 3,\n  \"charter\": \"the door \\u2014 every ref's home\"\n}\n",
	}))
	wantState(t, runAtom(t, "dies:canonical", ""), 0)
	if diesBuiltAContainer() {
		t.Error("the form is graded in Go; no container should be built")
	}

	for name, tc := range map[string]struct{ body, needle string }{
		"a block out of sorted order": {
			"{\n  \"apiVersion\": 3,\n  \"tools\": {\n    \"calypso\": {\n      \"secret_path\": \"/fleet/clio\"\n    },\n    \"build\": {\n      \"binaries\": [\n        \"clio\"\n      ]\n    }\n  }\n}\n",
			"fleet/stars/clio/slag.json: diverges from its canonical form",
		},
		"four-space indent":        {"{\n    \"apiVersion\": 3\n}\n", "diverges from its canonical form"},
		"no trailing newline":      {"{\n  \"apiVersion\": 3\n}", "diverges from its canonical form"},
		"non-ascii left unescaped": {"{\n  \"charter\": \"the door \u2014 home\"\n}\n", "diverges from its canonical form"},
		"not json":                 {"{\"apiVersion\": 3,\n", "fleet/stars/clio/slag.json: not one JSON document"},
	} {
		engine.reset()
		engine.withTree(diesTree(map[string]string{
			"fleet/stars/clio/slag.json":  tc.body,
			"fleet/stars/hades/slag.json": canonical,
		}))
		wantState(t, runAtom(t, "dies:canonical", ""), 1, "1 of 2 record(s)", tc.needle)
		if diesBuiltAContainer() {
			t.Errorf("%s: a finding must not build a container", name)
		}
	}

	// Every finding is named, not just the first.
	engine.reset()
	engine.withTree(diesTree(map[string]string{
		"fleet/stars/a/slag.json": "{\"z\": 1, \"a\": 2}\n",
		"fleet/stars/b/slag.json": "{\n  \"a\": 2\n}",
		"fleet/stars/c/slag.json": canonical,
	}))
	wantState(t, runAtom(t, "dies:canonical", ""), 1, "2 of 3 record(s)", "fleet/stars/a/slag.json", "fleet/stars/b/slag.json")
}

// A die with the shape but no record is could-not-run, and a record the engine
// could not hand over is could-not-run naming it — neither is a verdict on
// the form.
func TestDiesCanonicalCannotRunWithoutARecordItCanRead(t *testing.T) {
	engine.reset()
	engine.withTree(diesTree(map[string]string{"fleet/stars/.keep": ""}, "fleet/stars/x"))
	wantState(t, runAtom(t, "dies:canonical", ""), 2, "fleet/stars/ carries no slag.json")

	engine.reset()
	engine.withTree(diesTree(map[string]string{"fleet/stars/clio/slag.json": "{}\n"}))
	engine.fail(`file(path:"fleet/stars/clio/slag.json"){contents}`, "the blob would not evaluate")
	wantState(t, runAtom(t, "dies:canonical", ""), 2, "fleet/stars/clio/slag.json could not be read", "would not evaluate")

	engine.reset()
	engine.withTree(diesTree(nil))
	engine.fail(`glob(pattern:"fleet/stars/*/slag.json")`, "the glob would not evaluate")
	wantState(t, runAtom(t, "dies:canonical", ""), 2, "fleet/stars/ could not be scanned", "would not evaluate")
}
