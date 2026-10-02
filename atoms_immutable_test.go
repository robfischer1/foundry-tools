package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// ops:immutable's needles.
const (
	imBaseNeedle   = `"rev-parse","--verify","--quiet","base-sha^{commit}"`
	imMergeNeedle  = `"git","merge-base","base-sha","HEAD"`
	imWorktree     = `"worktree","add","--detach","/tmp/immutable-base","` + sinceSha + `"`
	imHeadBuild    = `"kustomize","foundry","-o","/tmp/immutable-head.0.yaml"`
	imBaseBuild    = `"kustomize","/tmp/immutable-base/foundry","-o","/tmp/immutable-base.0.yaml"`
	imHeadContents = `/tmp/immutable-head.0.yaml`
	imBaseContents = `/tmp/immutable-base.0.yaml`
)

func imJob(cpu string) string {
	return "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: devpi-provision-3\n  namespace: foundry\n" +
		"spec:\n  template:\n    spec:\n      containers:\n      - name: c\n        resources: {limits: {cpu: " + cpu + "}}\n"
}

// imTree is a repository that is the Flux tree, one Kustomization (foundry/),
// with its base and head renders scripted.
func imTree(base, head string) {
	engine.reset()
	engine.withTree(map[string]string{
		"clusters/pantheon/kustomization.yaml": "resources: [foundry.yaml]\n",
		"clusters/pantheon/foundry.yaml":       "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nspec:\n  path: ./foundry\n  sourceRef: {name: flux-system}\n",
		"foundry/kustomization.yaml":           "resources: []\n",
	})
	engine.stdout(imMergeNeedle, sinceSha+"\n")
	engine.script(script{match: imHeadContents, leaf: "contents", value: head})
	engine.script(script{match: imBaseContents, leaf: "contents", value: base})
}

// flux #206: a Job's pod template edited in place is a finding naming the
// path, the object and the field; the base is the merge base, checked out
// beside the head.
func TestOpsImmutableFindsAJobTemplateEdit(t *testing.T) {
	imTree(imJob("200m"), imJob("1"))
	wantState(t, runAtom(t, "ops:immutable", "base-sha"), 1,
		"FINDINGS - these edits change a field the API server refuses",
		"foundry: Job/foundry/devpi-provision-3: spec.template",
		"kustomize.toolkit.fluxcd.io/force: enabled")
	for _, n := range []string{imWorktree, imHeadBuild, imBaseBuild} {
		if engine.chain(n) == "" {
			t.Errorf("no exec %s", n)
		}
	}
}

func TestOpsImmutablePassesAnUnchangedTree(t *testing.T) {
	imTree(imJob("200m"), imJob("200m"))
	v := runAtom(t, "ops:immutable", "base-sha")
	wantState(t, v, 0)
	wantLogs(t, v, "PASS - 1 path(s) compared against "+sinceSha)
}

// wantLogs: a passing atom's reason is just PASS; what it said is in Logs.
func wantLogs(t *testing.T, v checks.Verdict, needles ...string) {
	t.Helper()
	logs := strings.Join(v.Logs, "\n")
	for _, n := range needles {
		if !strings.Contains(logs, n) {
			t.Errorf("logs lack %q:\n%s", n, logs)
		}
	}
}

// A head that does not build is ops:flux's to report; a base that does not
// build (new, or broken when it landed) is not this pull's finding. Both are
// noted and skipped, never a finding and never a CANNOT RUN.
func TestOpsImmutableSkipsWhatDoesNotBuild(t *testing.T) {
	imTree(imJob("200m"), imJob("1"))
	engine.exitCode(imHeadBuild, 1)
	v := runAtom(t, "ops:immutable", "base-sha")
	wantState(t, v, 0)
	wantLogs(t, v, "foundry: head does not build (ops:flux reports it)", "PASS - 1 path(s)")
	if engine.chain(imBaseBuild) != "" {
		t.Error("a head that does not build needs no base render")
	}

	imTree(imJob("200m"), imJob("1"))
	engine.exitCode(imBaseBuild, 1)
	v = runAtom(t, "ops:immutable", "base-sha")
	wantState(t, v, 0)
	wantLogs(t, v, "foundry: no buildable base (new or broken there), skipped", "PASS - 1 path(s)")
}

func TestOpsImmutableStandsDownOrRefuses(t *testing.T) {
	// No Flux tree: ABSENT, and nothing runs.
	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module x\n"})
	v := runAtom(t, "ops:immutable", "base-sha")
	wantState(t, v, 0, "ABSENT - no Flux tree here")
	if engine.chain(`"kubectl"`) != "" {
		t.Error("no Flux tree runs nothing")
	}

	// No base (a tip or a local run): nothing live to differ from.
	imTree(imJob("200m"), imJob("1"))
	wantState(t, runAtom(t, "ops:immutable", ""), 0, "ABSENT - no base to compare against")
	if engine.chain(`"kubectl"`) != "" {
		t.Error("no base runs nothing")
	}

	// A base the history does not reach is a CANNOT RUN, not a pass.
	imTree(imJob("200m"), imJob("1"))
	engine.exitCode(imBaseNeedle, 1)
	wantState(t, runAtom(t, "ops:immutable", "base-sha"), 2, "CANNOT RUN - the base base-sha is not in this history")

	// No merge base is a CANNOT RUN naming git's answer.
	imTree(imJob("200m"), imJob("1"))
	engine.exitCode(imMergeNeedle, 1)
	wantState(t, runAtom(t, "ops:immutable", "base-sha"), 2, "CANNOT RUN - git found no merge base")

	// An unreadable cluster manifest is a CANNOT RUN naming it.
	imTree(imJob("200m"), imJob("1"))
	engine.fail(`file(path:"clusters/pantheon/foundry.yaml")`, "read evaporated")
	wantState(t, runAtom(t, "ops:immutable", "base-sha"), 2, "CANNOT RUN - clusters/pantheon/foundry.yaml could not be read", "read evaporated")

	// A render that does not parse is a CANNOT RUN naming the path and side.
	imTree(": [bad\n", imJob("1"))
	wantState(t, runAtom(t, "ops:immutable", "base-sha"), 2, "CANNOT RUN - foundry: base: ")

	// The tree would not enumerate.
	imTree(imJob("200m"), imJob("1"))
	engine.fail(`glob(pattern:"**")`, "mount evaporated")
	wantState(t, runAtom(t, "ops:immutable", "base-sha"), 2, "the tree would not enumerate", "mount evaporated")
}

// With no Kustomization CR the fallback trees are compared, and a tree with
// neither is ABSENT.
func TestOpsImmutableFallsBackAndCountsPaths(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{
		"clusters/pantheon/kustomization.yaml": "resources: []\n",
		"foundry/kustomization.yaml":           "resources: []\n",
		"data/kustomization.yaml":              "resources: []\n",
	})
	engine.stdout(imMergeNeedle, sinceSha+"\n")
	engine.script(script{match: `/tmp/immutable-`, leaf: "contents", value: imJob("1")})
	v := runAtom(t, "ops:immutable", "base-sha")
	wantState(t, v, 0)
	wantLogs(t, v, "PASS - 2 path(s) compared")
	if !strings.Contains(engine.chain(`"kustomize","data","-o"`), "data") || engine.chain(`"kustomize","foundry","-o"`) == "" {
		t.Error("the fallback compares every <dir>/kustomization.yaml but clusters/")
	}

	engine.reset()
	engine.withTree(map[string]string{"clusters/pantheon/kustomization.yaml": "resources: []\n"})
	wantState(t, runAtom(t, "ops:immutable", "base-sha"), 0, "ABSENT - the Flux tree carries no Kustomization CR and no kustomization.yaml")
}

// Two paths, data/ before foundry/: whichever side of data/ does not build,
// it is skipped and foundry/'s edit is still found. And a CR-applied tree is
// what is compared, not every kustomization.yaml in the root.
func TestOpsImmutableKeepsGoingAndComparesTheCRPaths(t *testing.T) {
	two := func() {
		engine.reset()
		engine.withTree(map[string]string{
			"clusters/pantheon/kustomization.yaml": "resources: [data.yaml, foundry.yaml]\n",
			"clusters/pantheon/data.yaml":          "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nspec:\n  path: ./data\n  sourceRef: {name: flux-system}\n",
			"clusters/pantheon/foundry.yaml":       "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nspec:\n  path: ./foundry\n  sourceRef: {name: flux-system}\n",
			"data/kustomization.yaml":              "resources: []\n",
			"foundry/kustomization.yaml":           "resources: []\n",
			"loose/kustomization.yaml":             "resources: []\n",
		})
		engine.stdout(imMergeNeedle, sinceSha+"\n")
		engine.script(script{match: `/tmp/immutable-head.`, leaf: "contents", value: imJob("1")})
		engine.script(script{match: `/tmp/immutable-base.`, leaf: "contents", value: imJob("200m")})
	}
	two()
	engine.exitCode(`"kustomize","data","-o"`, 1)
	wantState(t, runAtom(t, "ops:immutable", "base-sha"), 1, "foundry: Job/foundry/devpi-provision-3: spec.template")

	two()
	engine.exitCode(`"kustomize","/tmp/immutable-base/data","-o"`, 1)
	wantState(t, runAtom(t, "ops:immutable", "base-sha"), 1, "foundry: Job/foundry/devpi-provision-3: spec.template")

	two()
	engine.script(script{match: `/tmp/immutable-base.`, leaf: "contents", value: imJob("1")})
	v := runAtom(t, "ops:immutable", "base-sha")
	wantState(t, v, 0)
	wantLogs(t, v, "PASS - 2 path(s) compared")
	if engine.chain(`"kustomize","loose","-o"`) != "" {
		t.Error("a kustomization.yaml no CR applies is not compared when CRs exist")
	}
}

// Every way the engine can fail is a CANNOT RUN, never a pass.
func TestOpsImmutableEngineFailuresAreCannotRun(t *testing.T) {
	imTree(imJob("200m"), imJob("1"))
	engine.fail(`http(url:"`+checks.KubectlURL+`")`, "dl.k8s.io unreachable")
	wantState(t, runAtom(t, "ops:immutable", "base-sha"), 2, "CANNOT RUN - kubectl could not be fetched", "dl.k8s.io unreachable")

	// Each failure is scoped to ONE leaf: the exec's exit code, or the read
	// of what it wrote. Unscoped, the exit-code query (which names the output
	// path too) would fail first and the read would never be reached.
	for name, f := range map[string]script{
		"head exit":     {match: imHeadBuild, leaf: "exitCode", fail: "engine went away"},
		"base exit":     {match: imBaseBuild, leaf: "exitCode", fail: "engine went away"},
		"head contents": {match: imHeadContents, leaf: "contents", fail: "engine went away"},
		"base contents": {match: imBaseContents, leaf: "contents", fail: "engine went away"},
	} {
		imTree(imJob("200m"), imJob("1"))
		engine.script(f)
		v := runAtom(t, "ops:immutable", "base-sha")
		if v.State != 2 || !strings.Contains(v.Reason, "CANNOT RUN") || !strings.Contains(v.Reason, "engine went away") {
			t.Errorf("%s: state %d, reason %s", name, v.State, v.Reason)
		}
	}
}
