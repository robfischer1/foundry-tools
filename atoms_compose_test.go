package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE COMPOSE LANE'S TESTS, against the paper engine (engine_fake_test.go).
//
// The three atoms share one surface and one client, and both of those are
// where the ported workflows' defects lived: a scan that did not run reading
// as an empty list, and a parse that never happened reading as a clean tree.
// So the surface gets its own tests once, and each atom gets every decision it
// makes about what the client said.

// composeTree is everyLaneTree with paths added and removed. A prefix in drop
// removes the whole subtree, which is how a lane's marker is taken away.
func composeTree(add map[string]string, drop ...string) map[string]string {
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

// THE ABSENT SHAPE IS HELD BY wantState ALONE, and that is not a shortcut: a
// passing verdict's reason is collapsed to "<id>: PASS" (reasonFor), so a
// state-0 reason that still carries "ABSENT" can only have come from
// VerdictOf's AnnouncedAbsence branch — the one that sets Result "absent".
// Asserting the sentence therefore asserts the result.

// composeBuiltAContainer reports whether anything pulled an image. Named for
// this file rather than generically, because every lane's tests share one
// package and a generic helper name would collide at merge.
func composeBuiltAContainer() bool {
	for _, q := range engine.chains() {
		if strings.Contains(q, "container{from(") {
			return true
		}
	}
	return false
}

// ---- the shared surface ----

// A tree that tracks no compose spec is ABSENT for all three, and it costs one
// glob rather than a container: most of the fleet is Kubernetes YAML.
func TestComposeSurfaceIsAbsentWithoutASpecAndBuildsNoContainer(t *testing.T) {
	for _, id := range []string{"compose:config", "compose:no-tracked-secrets", "compose:third-party-pins"} {
		engine.reset()
		engine.withTree(composeTree(nil, "compose.yaml"))
		wantState(t, runAtom(t, id, ""), 0, id+": ABSENT", "tracks no compose.yaml/compose.yml")
		if composeBuiltAContainer() {
			t.Errorf("%s: an absent compose surface must not build a container:\n%v", id, engine.chains())
		}
	}
}

// A compose file in a gitignored scratch directory is not a spec this
// repository ships: the surface is the FILTERED tree, and the filter is the
// one call that says so.
func TestComposeSurfaceReadsTheTrackedTreeWithGitExcluded(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	runAtom(t, "compose:no-tracked-secrets", "")
	c := engine.chain(`glob(pattern:"**")`)
	wantCalls(t, c, []string{"filter", "gitignore:true", `exclude:[".git"]`})
}

// grep's third exit code, in Go: an empty surface read off a BROKEN scan is an
// absence this repository never declared, so the Glob error is a 2 on all
// three atoms rather than an ABSENT on any of them.
func TestComposeSurfaceFailureIsACannotRunNotAnAbsence(t *testing.T) {
	for _, id := range []string{"compose:config", "compose:no-tracked-secrets", "compose:third-party-pins"} {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.fail(`glob(pattern:"**")`, "the gitignore filter would not evaluate")
		wantState(t, runAtom(t, id, ""), 2, "the compose-surface scan itself failed", "would not evaluate")
	}
}

// ---- compose:config ----

func TestComposeConfigParsesEverySpecAndBuildsThePinnedClient(t *testing.T) {
	engine.reset()
	engine.withTree(composeTree(map[string]string{
		"docker-compose.yml": "services: {}\n",
		"stacks/compose.yml": "services: {}\n",
	}))

	// A PASS keeps no output (reasonFor collapses it to "<id>: PASS"), so the
	// claim "every tracked spec was parsed" is held on the CHAIN: ONE EXEC PER
	// SPEC, which is what replaced the old body's one opaque `while read`
	// loop and what lets the engine cache each parse on its own.
	wantState(t, runAtom(t, "compose:config", ""), 0)
	for _, spec := range []string{"compose.yaml", "docker-compose.yml", "stacks/compose.yml"} {
		if engine.chain(`"docker-compose","-f","`+spec+`","config"`) == "" {
			t.Errorf("no exec parsed %s:\n%v", spec, engine.chains())
		}
	}
	if engine.chain(`"docker-compose","-f","Dockerfile"`) != "" {
		t.Errorf("only a compose spec is parsed:\n%v", engine.chains())
	}

	c := engine.chain(`"docker-compose","-f","compose.yaml"`, "exitCode")
	if !strings.Contains(c, checks.ImageFleet) {
		t.Errorf("compose:config must run in the fleet lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withEnvVariable", `name:"CI"`, `value:"true"`},
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withFile", `path:"/usr/local/bin/docker-compose"`, "permissions:493"},
		[]string{"withExec", `args:["docker-compose","version"]`},
		[]string{"withExec", `expect:ANY`, `args:["docker-compose","-f","compose.yaml","config","--no-interpolate","--quiet"]`},
	)
	// The version probe is PROVISIONING and must carry the default Expect: a
	// client that downloaded but does not run is a could-not-run.
	if hasCall(c, "withExec", `args:["docker-compose","version"]`, "expect:ANY") {
		t.Errorf("the version probe must run under the default Expect:\n%s", c)
	}
	// --no-interpolate is load-bearing: these files use ${VAR:?message} to
	// make a missing variable a DEPLOY-TIME error.
	if !strings.Contains(c, "--no-interpolate") {
		t.Errorf("the parse must set --no-interpolate:\n%s", c)
	}
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("compose:config must not read GATE_BASE:\n%s", c)
	}
	// Nothing was stubbed, so the lane's own /src mount — and its cache key —
	// is kept rather than replaced.
	if strings.Contains(c, "withoutMount") {
		t.Errorf("a tree that needed no stub must keep the lane's mount:\n%s", c)
	}
}

// THE CLIENT IS FETCHED MIRROR-FIRST, and the upstream is the fallback rather
// than a fallthrough: a spec that was never parsed is not a clean tree.
func TestComposeConfigFallsBackFromTheMirrorToUpstream(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "compose:config", ""), 0)
	if engine.chain(`http(url:"`+checks.ComposeMirror+`")`, "id") == "" {
		t.Errorf("the happy path must place the MIRROR's file:\n%v", engine.chains())
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(checks.ComposeMirror, "502 from the mirror")
	wantState(t, runAtom(t, "compose:config", ""), 0)
	if engine.chain(`http(url:"`+checks.ComposeURL+`")`, "id") == "" {
		t.Errorf("a dead mirror must place the UPSTREAM's file:\n%v", engine.chains())
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail("docker/compose/releases", "404")
	wantState(t, runAtom(t, "compose:config", ""), 2,
		"the pinned docker/compose client", checks.ComposeVersion, "never parsed")
}

// THE env_file TARGETS ARE STUBBED FIRST, into the TREE rather than the
// container: /src is a mount and a relative env_file resolves inside it.
func TestComposeConfigStubsTheEnvFileTargetsIntoTheTree(t *testing.T) {
	engine.reset()
	engine.withTree(composeTree(map[string]string{
		"compose.yaml": "services:\n  x:\n    env_file: secrets/x.env\n",
		"stacks/a.yml": "env_file:\n  - ./secrets/b.env\n",
	}))
	wantState(t, runAtom(t, "compose:config", ""), 0)

	for _, p := range []string{"secrets/x.env", "secrets/b.env"} {
		if engine.chain("withNewFile", `path:"`+p+`"`) == "" {
			t.Errorf("the tree must carry a stub for %s:\n%v", p, engine.chains())
		}
	}
	c := engine.chain(`"docker-compose","-f","compose.yaml"`, "exitCode")
	// Two mounts on one path is a shadowing rule this file must not rely on:
	// the stubbed tree REPLACES the lane's mount.
	wantCalls(t, c,
		[]string{"withoutMount", `path:"/src"`},
		[]string{"withMountedDirectory", `path:"/src"`},
	)
}

// A target the repository actually tracks is left alone — the old body's
// `[ -e "$p" ] || touch`.
func TestComposeConfigLeavesATrackedEnvFileAlone(t *testing.T) {
	engine.reset()
	engine.withTree(composeTree(map[string]string{
		"compose.yaml": "services:\n  x:\n    env_file: kept.env\n",
		"kept.env":     "A=1\n",
	}))
	wantState(t, runAtom(t, "compose:config", ""), 0)
	if engine.chain("withNewFile", `path:"kept.env"`) != "" {
		t.Errorf("a tracked env_file must not be overwritten with a stub:\n%v", engine.chains())
	}
	c := engine.chain(`"docker-compose","-f","compose.yaml"`, "exitCode")
	if strings.Contains(c, "withoutMount") {
		t.Errorf("nothing was stubbed, so the lane's mount stands:\n%s", c)
	}
}

// A body the engine listed and then could not read is an ERROR, never an empty
// body: an empty body matches nothing, which is the clean-scan-that-never-ran
// this module exists to refuse (nas01-stacks validate.yml:63-79).
func TestComposeConfigRefusesAnEnvFileScanThatDidNotRun(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`file(path:"compose.yaml")`, "i/o error")
	wantState(t, runAtom(t, "compose:config", ""), 2,
		"the env_file scan failed", "nothing to stub", "i/o error")
}

func TestComposeConfigReadsEachSpecsExitAndTables(t *testing.T) {
	engine.reset()
	engine.withTree(composeTree(map[string]string{"stacks/compose.yml": "services: {}\n"}))
	engine.exitCode(`"docker-compose","-f","stacks/compose.yml"`, 1)
	engine.stdout(`"docker-compose","-f","stacks/compose.yml"`, "services.x Additional property z is not allowed")
	v := runAtom(t, "compose:config", "")
	wantState(t, v, 1, "a tracked compose spec does not parse",
		"Additional property z", "stacks/compose.yml", "FAIL")
	if !strings.Contains(v.Reason, "compose.yaml") || !strings.Contains(v.Reason, "OK") {
		t.Errorf("the passing spec keeps its OK row:\n%s", v.Reason)
	}

	// Any non-zero code is a finding about the file; compose does not have a
	// third code this atom has to fold.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"docker-compose","-f","compose.yaml"`, 15)
	wantState(t, runAtom(t, "compose:config", ""), 1, "does not parse")
}

// The stub count is part of the table, and a FINDINGS verdict is the only
// place the table is observable: a PASS keeps no output.
func TestComposeConfigTablesTheStubCountBesideTheFailure(t *testing.T) {
	engine.reset()
	engine.withTree(composeTree(map[string]string{
		"compose.yaml": "services:\n  x:\n    env_file: secrets/x.env\n",
	}))
	engine.exitCode(`"docker-compose","-f","compose.yaml"`, 1)
	wantState(t, runAtom(t, "compose:config", ""), 1,
		"1 env_file reference(s) stubbed", "compose.yaml", "FAIL")
}

// The engine's own failure — an image that would not pull, a version probe
// that did not run — is the atom's 2, with the error text.
func TestComposeConfigCannotRunWhenTheParseNeverRan(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"docker-compose","-f"`, "exit code: 1: no space left on device")
	wantState(t, runAtom(t, "compose:config", ""), 2,
		"the parse of compose.yaml never ran", "no space left on device")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"docker-compose","version"`, "exec format error")
	wantState(t, runAtom(t, "compose:config", ""), 2, "never ran", "exec format error")
}

// ---- compose:no-tracked-secrets ----

// NO CONTAINER RUNS: the question is entirely about the file list, which the
// engine already answered.
func TestComposeNoTrackedSecretsNeedsNoImage(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "compose:no-tracked-secrets", ""), 0)
	if composeBuiltAContainer() {
		t.Errorf("the tracked-secrets assertion is a file list, not an image:\n%v", engine.chains())
	}
}

// THE IGNORE RULE IS ASSERTED, NOT TRUSTED: an ignore rule only protects files
// it was written before.
func TestComposeNoTrackedSecretsNamesEveryCredentialShape(t *testing.T) {
	engine.reset()
	engine.withTree(composeTree(map[string]string{
		".env":                "A=1\n",
		"tls/site.pem":        "",
		"keys/id_rsa":         "",
		"deploy/deploy.key":   "",
		"envs/prod/values.md": "",
		".env.production":     "",
	}))
	v := runAtom(t, "compose:no-tracked-secrets", "")
	wantState(t, v, 1, "must never be committed")
	for _, hit := range []string{".env", "tls/site.pem", "keys/id_rsa", "deploy/deploy.key", "envs/prod/values.md", ".env.production"} {
		if !strings.Contains(v.Reason, hit) {
			t.Errorf("the findings must name %s:\n%s", hit, v.Reason)
		}
	}
}

// ---- compose:third-party-pins ----

// A COUNT GATE, not a list gate: ANY ${PIN_} image interpolation is a red
// merge, reported as grep -rn's own file:line:text.
func TestComposeThirdPartyPinsReportsFileLineAndText(t *testing.T) {
	engine.reset()
	engine.withTree(composeTree(map[string]string{
		"compose.yaml": "services:\n  x:\n    image: ${PIN_CADDY}\n",
	}))
	v := runAtom(t, "compose:third-party-pins", "")
	wantState(t, v, 1, "1 ${PIN_} interpolation(s) found", "the pin era does not reopen",
		"compose.yaml:3:    image: ${PIN_CADDY}")
	if composeBuiltAContainer() {
		t.Errorf("the ratchet is a regexp over bodies, not an image:\n%v", engine.chains())
	}
}

// A workflow file may legitimately MENTION ${PIN_} while describing the era
// that closed; the gate is about what the stack DEPLOYS.
func TestComposeThirdPartyPinsIgnoresForgejoAndPassesClean(t *testing.T) {
	engine.reset()
	engine.withTree(composeTree(map[string]string{
		".forgejo/workflows/ci.yml": "# the pin era: image: ${PIN_OLD}\n",
	}))
	wantState(t, runAtom(t, "compose:third-party-pins", ""), 0)

	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "compose:third-party-pins", ""), 0)
}

// A COUNT GATE ONLY STAYS CLOSED IF THE COUNT HAPPENED.
func TestComposeThirdPartyPinsRefusesAScanThatDidNotRun(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`file(path:"flux/x.yaml")`, "i/o error")
	wantState(t, runAtom(t, "compose:third-party-pins", ""), 2,
		"the ${PIN_} scan itself failed", "a closed ratchet on a scan that did not run", "i/o error")
}
