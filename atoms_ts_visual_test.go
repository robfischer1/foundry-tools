package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/visuallane"
)

const (
	visualSha        = "0123456789abcdef0123456789abcdef01234567"
	visualRepo       = "http://ourea.prime.svc.cluster.local:8215/rob/gijmo-ui.git"
	visualAuth       = `{"auths":{"registry.notusmi.com":{"username":"publisher","password":"pw"}}}`
	visualPassReport = `{"suites":[],"errors":[],"stats":{"expected":3}}`
	visualFailReport = `{"suites":[{"title":"gallery.spec.ts","specs":[{"title":"primitives render in dusk","tests":[{"status":"unexpected","results":[` +
		`{"errors":[{"message":"Error: expect(locator).toHaveScreenshot(expected) failed\n\n  9 pixels (ratio 0.01 of all image pixels) are different."}],` +
		`"attachments":[{"name":"dusk-diff.png","path":"/tmp/visual/results/g/dusk-diff.png"}]}]}]}]}],"errors":[],"stats":{"expected":2,"unexpected":1}}`

	visualInstallNeedle = `args:["bun","install","--frozen-lockfile"]`
	visualCheckNeedle   = `"--update-snapshots=none"`
	visualUpdateNeedle  = `"--update-snapshots=changed"`
	visualTurboNeedle   = `"turbo","run","build"`
)

// visualTree is gijmo-ui's shape: a turbo workspace whose gallery declares
// the suite, a lockfile pinning Playwright, and the report the check run
// wrote. extra overrides any of it; an empty value deletes the path.
func visualTree(extra map[string]string) {
	engine.reset()
	tree := map[string]string{
		"package.json":                   `{"name":"gijmo-ui"}`,
		"turbo.json":                     "{}",
		"visual.toml":                    "[[suite]]\ndir = \"apps/gallery\"\n",
		"bun.lock":                       `{"packages":{"@playwright/test":["@playwright/test@1.62.0"]}}`,
		"/src/apps/gallery/package.json": `{"name":"@gijmo/gallery"}`,
		visualReport:                     visualPassReport,
	}
	for k, v := range extra {
		if v == "" {
			delete(tree, k)
			continue
		}
		tree[k] = v
	}
	engine.withTree(tree)
}

// runVisual runs ts:visual as the door's lane does: a fetched commit, with the
// registry credential when auth is not "".
func runVisual(t *testing.T, repo, sha, auth string) checks.Verdict {
	t.Helper()
	var secret *dagger.Secret
	if auth != "" {
		secret = dag.SetSecret("visual-auth", auth)
	}
	return registry[visuallane.Atom](context.Background(), newRun(dag.Directory(), repo, "").withArtifacts(secret, sha))
}

func TestTSVisualIsAbsentWithoutADeclaration(t *testing.T) {
	visualTree(map[string]string{"visual.toml": ""})
	v := runVisual(t, visualRepo, visualSha, visualAuth)
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "ts:visual: ABSENT - no visual.toml at the root") {
		t.Fatalf("%+v", v)
	}
	if engine.chain("withExec") != "" {
		t.Errorf("an absent tree ran a container:\n%s", engine.chain("withExec"))
	}
}

func TestTSVisualRefusesWhatItCannotReadOrRun(t *testing.T) {
	visualTree(nil)
	engine.fail("entries", "the engine went away")
	wantState(t, runVisual(t, visualRepo, visualSha, ""), 2, "the tree's root could not be listed", "the engine went away")

	visualTree(nil)
	engine.failLeaf(`file(path:"visual.toml")`, "contents", "unreadable")
	wantState(t, runVisual(t, visualRepo, visualSha, ""), 2, "visual.toml could not be read", "unreadable")

	visualTree(map[string]string{"visual.toml": "[[suite]]\ndirs = \"x\"\n"})
	wantState(t, runVisual(t, visualRepo, visualSha, ""), 1, "FINDINGS - visual.toml carries a key the lane does not read")

	visualTree(map[string]string{"bun.lock": ""})
	wantState(t, runVisual(t, visualRepo, visualSha, ""), 2, "no readable bun.lock")

	visualTree(map[string]string{"bun.lock": "{}"})
	wantState(t, runVisual(t, visualRepo, visualSha, ""), 1, "FINDINGS - bun.lock resolves no @playwright/test")

	visualTree(nil)
	engine.exitCode(visualInstallNeedle, 1)
	engine.stdout(visualInstallNeedle, "lockfile had changes, but lockfile is frozen")
	wantState(t, runVisual(t, visualRepo, visualSha, ""), 2, "frozen lockfile install failed", "lockfile is frozen")

	visualTree(nil)
	engine.fail(visualInstallNeedle, "browser install failed")
	wantState(t, runVisual(t, visualRepo, visualSha, ""), 2, "the atom never ran", "browser install failed")
}

// The browsers are installed for the lockfile's Playwright BEFORE the tree is
// mounted, so their layer survives every commit; the suite's dependencies are
// built by turbo, and the suite runs with Playwright writing outside the tree.
func TestTSVisualPassesAndBuildsTheChainInOrder(t *testing.T) {
	visualTree(nil)
	v := runVisual(t, visualRepo, visualSha, visualAuth)
	wantState(t, v, 0)
	if len(v.Findings) != 0 {
		t.Errorf("%+v", v.Findings)
	}
	if said := strings.Join(v.Logs, "\n"); !strings.Contains(said, "apps/gallery: 3 passed, 0 failed, 0 flaky, 0 skipped") {
		t.Errorf("logs:\n%s", said)
	}
	c := engine.chain(visualCheckNeedle, "exitCode")
	wantCalls(t, c,
		[]string{"from", checks.ImageTS},
		[]string{"withEnvVariable", `name:"PLAYWRIGHT_BROWSERS_PATH"`, `value:"/ms-playwright"`},
		[]string{"withEnvVariable", `name:"TURBO_TELEMETRY_DISABLED"`, `value:"1"`},
		[]string{"withExec", `args:["bunx","playwright@1.62.0","install","--with-deps","chromium"]`},
		[]string{"withExec", `expect:ANY`, `args:["bunx","turbo","run","build","--filter=@gijmo/gallery^..."]`},
		[]string{"withEnvVariable", `name:"PLAYWRIGHT_JSON_OUTPUT_NAME"`, `value:"/tmp/visual/report.json"`},
		[]string{"withExec", `expect:ANY`, `args:["bunx","playwright","test","--reporter=list,json","--update-snapshots=none","--output=/tmp/visual/results"]`},
	)
	browsers := lastCall(c, "withExec", `"playwright@1.62.0"`)
	mount := lastCall(c, "withMountedDirectory", `path:"/src"`)
	install := lastCall(c, "withExec", `"bun","install"`)
	turbo := lastCall(c, "withExec", visualTurboNeedle)
	if browsers < 0 || mount < browsers || install < mount || turbo < install {
		t.Errorf("order: browsers %d, mount %d, install %d, turbo %d\n%s", browsers, mount, install, turbo, c)
	}
	if strings.Contains(c, `expect:ANY, args:["bunx","playwright@`) || hasCall(c, "withExec", "expect:ANY", `"playwright@1.62.0"`) {
		t.Errorf("the browser install is provisioning and takes the default Expect:\n%s", c)
	}
	// After turbo ran at the root, the suite runs from its own directory again.
	if suite := lastCall(c, "withWorkdir", `path:"/src/apps/gallery"`); suite < turbo || lastCall(c, "withWorkdir") != suite {
		t.Errorf("the suite runs from its own directory, after the build:\n%s", c)
	}
	if engine.chain(visualUpdateNeedle) != "" || engine.chain("publish") != "" {
		t.Error("a passing run regenerated or pushed something")
	}
}

// A tree with no turbo.json, or a suite with no package name, builds nothing
// first: its web server is the suite's own business.
func TestTSVisualBuildsNothingFirstWithoutTurboOrAName(t *testing.T) {
	for name, extra := range map[string]map[string]string{
		"no turbo.json": {"turbo.json": ""},
		"no name":       {"/src/apps/gallery/package.json": `{"private":true}`},
	} {
		visualTree(extra)
		wantState(t, runVisual(t, visualRepo, visualSha, ""), 0)
		if c := engine.chain(visualCheckNeedle); strings.Contains(c, visualTurboNeedle) {
			t.Errorf("%s: turbo ran:\n%s", name, c)
		}
	}
}

func TestTSVisualReadsTheSuitesOwnFailures(t *testing.T) {
	visualTree(map[string]string{"/src/apps/gallery/package.json": ""})
	wantState(t, runVisual(t, visualRepo, visualSha, ""), 1, "apps/gallery: FINDINGS - the suite has no package.json")

	visualTree(nil)
	engine.exitCode(visualTurboNeedle, 1)
	engine.stdout(visualTurboNeedle, "@gijmo/ui#build: tsc error")
	wantState(t, runVisual(t, visualRepo, visualSha, ""), 1, "what the suite imports did not build", "tsc error")

	visualTree(nil)
	engine.fail(visualTurboNeedle, "engine gone")
	wantState(t, runVisual(t, visualRepo, visualSha, ""), 2, "apps/gallery: CANNOT RUN - the build never ran", "engine gone")

	visualTree(nil)
	engine.fail(visualCheckNeedle, "engine gone")
	wantState(t, runVisual(t, visualRepo, visualSha, ""), 2, "the suite never ran", "engine gone")

	visualTree(map[string]string{visualReport: ""})
	engine.exitCode(visualCheckNeedle, 1)
	engine.stderr(visualCheckNeedle, "Error: no tests found")
	wantState(t, runVisual(t, visualRepo, visualSha, ""), 2, "Playwright wrote no JSON report (exit 1)", "no tests found")

	visualTree(map[string]string{visualReport: "not json"})
	wantState(t, runVisual(t, visualRepo, visualSha, ""), 2, "the Playwright JSON report for apps/gallery does not parse")

	visualTree(nil)
	engine.exitCode(visualCheckNeedle, 3)
	engine.stderr(visualCheckNeedle, "worker crashed")
	wantState(t, runVisual(t, visualRepo, visualSha, ""), 2, "apps/gallery: 3 passed", "worker crashed")
	if engine.chain(visualUpdateNeedle) != "" {
		t.Error("a run that could not run regenerated baselines")
	}
}

// A run that finds something regenerates the baselines it would accept and
// pushes them with the diffs, tagged by the commit, and says where.
func TestTSVisualPushesTheDiffsAndTheBaselinesItWouldAccept(t *testing.T) {
	visualTree(map[string]string{visualReport: visualFailReport})
	engine.exitCode(visualCheckNeedle, 1)
	ref := "registry.notusmi.com/foundry/visual/gijmo-ui:" + visualSha
	v := runVisual(t, visualRepo, visualSha, visualAuth)
	wantState(t, v, 1,
		"apps/gallery: 2 passed, 1 failed",
		"violated  apps/gallery: gallery.spec.ts › primitives render in dusk — 9 pixels",
		"(diff: results/apps/gallery/g/dusk-diff.png)",
		"artifact "+ref,
		"oras manifest fetch "+ref)
	if len(v.Findings) != 1 || v.Findings[0].Cause != visuallane.CauseMismatch {
		t.Errorf("%+v", v.Findings)
	}
	update := engine.chain(visualUpdateNeedle)
	if update == "" || strings.Contains(update, visualCheckNeedle) {
		t.Fatalf("the update run is not its own run from the built tree:\n%s", update)
	}
	wantCalls(t, update, []string{"withExec", "expect:ANY", `args:["bunx","playwright","test","--reporter=list","--update-snapshots=changed","--output=/tmp/visual/update"]`})
	if !hasCall(update, "directory", `path:"/src/apps/gallery"`) {
		t.Errorf("the update run's suite directory is not what is kept:\n%s", update)
	}
	pub := engine.chain("publish")
	wantCalls(t, pub,
		[]string{"withRegistryAuth", `address:"registry.notusmi.com"`, `username:"publisher"`},
		[]string{"publish", `address:"` + ref + `"`},
	)
	if !hasCall(pub, "withRootfs") {
		t.Errorf("the artifact is not the image's rootfs:\n%s", pub)
	}
	art := engine.chain(`path:"baselines/apps/gallery"`)
	wantCalls(t, art,
		[]string{"withDirectory", `path:"results/apps/gallery"`},
		[]string{"withDirectory", `path:"baselines/apps/gallery"`},
	)
	filt := engine.chain("filter(")
	if !strings.Contains(filt, `"**/*-snapshots/**"`) || !strings.Contains(filt, `"**/node_modules/**"`) {
		t.Errorf("baselines/ keeps only the snapshots:\n%s", filt)
	}
	if engine.chain("visual-registry-password") == "" {
		t.Error("the password is not handed over as a secret")
	}
}

// The artifact never changes the verdict; why it was not pushed is said.
func TestTSVisualSaysWhyItPushedNothing(t *testing.T) {
	for name, c := range map[string]struct {
		repo, sha, auth, fail, want string
	}{
		"local run":     {"", "", visualAuth, "", "artifact: not pushed — the run names no commit sha"},
		"no credential": {visualRepo, visualSha, "", "", "artifact: not pushed — the lane was given no --artifact-auth"},
		"bad json":      {visualRepo, visualSha, "nope", "", "artifact: not pushed — the registry credential is not a docker config JSON"},
		"push refused":  {visualRepo, visualSha, visualAuth, "publish", "denied"},
		"secret gone":   {visualRepo, visualSha, visualAuth, "plaintext", "artifact: not pushed — the registry credential did not read"},
	} {
		visualTree(map[string]string{visualReport: visualFailReport})
		engine.exitCode(visualCheckNeedle, 1)
		if c.fail != "" {
			engine.failLeaf("", c.fail, "denied")
		}
		v := runVisual(t, c.repo, c.sha, c.auth)
		if v.State != 1 || !strings.Contains(v.Reason, c.want) || !strings.Contains(v.Reason, "artifact: not pushed — ") {
			t.Errorf("%s: state %d, reason lacks %q:\n%s", name, v.State, c.want, v.Reason)
		}
	}
}

// Two suites fold to the worst, each reason kept.
func TestTSVisualFoldsEverySuite(t *testing.T) {
	visualTree(map[string]string{
		"visual.toml":                 "[[suite]]\ndir = \"apps/gallery\"\n\n[[suite]]\ndir = \"apps/docs\"\n",
		"/src/apps/docs/package.json": "",
	})
	v := runVisual(t, visualRepo, visualSha, "")
	wantState(t, v, 1, "apps/gallery: 3 passed", "apps/docs: FINDINGS - the suite has no package.json")
}

// gate-file hands the visual lane's registry credential, and the commit it
// tags by, to the run that grades it.
func TestGateFileKeepsTheArtifactCredential(t *testing.T) {
	m := gateOn(t, cleanVector)
	auth := dag.SetSecret("visual-artifact-auth", visualAuth)
	if _, err := m.GateFile(context.Background(), fakeTree, gatePin, "base-sha", "visual", nil, false, nil, auth, "", nil); err != nil || m.artifactAuth != auth {
		t.Fatalf("artifactAuth %v err %v", m.artifactAuth, err)
	}
	r := newRun(dag.Directory(), "", "").withArtifacts(auth, visualSha)
	if r.artifactAuth != auth || r.sha != visualSha {
		t.Errorf("%+v", r)
	}
	if laneOf(checks.StageVisual) != "visual" {
		t.Errorf("the visual stage's record is labelled %q", laneOf(checks.StageVisual))
	}
	for _, a := range checks.AtomsForStage(checks.StageVisual) {
		if a.ID != visuallane.Atom || checks.IsPullPath(a.Stage) {
			t.Errorf("%+v", a)
		}
	}
}

// A suite whose imports did not build wrote nothing, so there is no artifact
// to push. An empty rootfs would be a layerless manifest the registry refuses.
func TestTSVisualPushesNothingWhenNoSuiteRan(t *testing.T) {
	visualTree(map[string]string{visualReport: visualFailReport})
	engine.exitCode(visualTurboNeedle, 2)
	v := runVisual(t, visualRepo, visualSha, visualAuth)
	wantState(t, v, 1, "what the suite imports did not build", "artifact: not pushed — no suite ran far enough to write results or baselines")
	if engine.chain("publish") != "" || engine.chain("visual-registry-password") != "" {
		t.Errorf("nothing was carried, yet a push was attempted:\n%v", engine.chains())
	}
}

// An absent package.json is said as one; an engine that failed to answer is
// said as itself, not as an absence.
func TestTSVisualSaysWhyThePackageJSONDidNotRead(t *testing.T) {
	visualTree(map[string]string{
		"visual.toml": "[[suite]]\ndir = \"apps/missing\"\n",
	})
	v := runVisual(t, visualRepo, visualSha, "")
	wantState(t, v, 1, "apps/missing: FINDINGS - the suite has no package.json: no such file")

	visualTree(map[string]string{
		"visual.toml": "[[suite]]\ndir = \"apps/gallery\"\n",
	})
	engine.failLeaf(`apps/gallery/package.json`, "exists", "engine went away")
	v = runVisual(t, visualRepo, visualSha, "")
	wantState(t, v, 1, "apps/gallery: FINDINGS - the suite has no package.json: engine went away")
}

// One suite that passed and wrote results, beside one whose imports did not
// build: the results are carried, so the artifact is pushed.
func TestTSVisualPushesWhatAPassingSuiteWrote(t *testing.T) {
	visualTree(map[string]string{
		"visual.toml":                 "[[suite]]\ndir = \"apps/gallery\"\n\n[[suite]]\ndir = \"apps/docs\"\n",
		"/src/apps/docs/package.json": `{"name":"@gijmo/docs"}`,
	})
	engine.exitCode(`"--filter=@gijmo/docs^..."`, 2)
	v := runVisual(t, visualRepo, visualSha, visualAuth)
	wantState(t, v, 1, "apps/gallery: 3 passed", "apps/docs: FINDINGS - what the suite imports did not build")
	if engine.chain("publish") == "" {
		t.Errorf("the passing suite's results were not pushed:\n%v", engine.chains())
	}
}
