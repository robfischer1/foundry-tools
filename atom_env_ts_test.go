package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

const visualLockTree = `{"packages":{"@playwright/test":["@playwright/test@1.62.0"]}}`

// ts:visual runs in the ts lane with the lockfile's browsers installed BEFORE
// the tree is mounted, so their layer survives every commit.
func TestVisualRunsWhereTheBrowsersAreInstalledBeforeTheTree(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"package.json": "{}", "bun.lock": visualLockTree})
	v, c := runFleetAtom(t, newRun(dag.Directory(), "", ""), "ts:visual")
	wantState(t, v, 0)
	wantCalls(t, c,
		[]string{"from", checks.ImageTS},
		[]string{"withEnvVariable", `name:"PLAYWRIGHT_BROWSERS_PATH"`, `value:"/ms-playwright"`},
		[]string{"withExec", `args:["bunx","playwright@1.62.0","install","--with-deps","chromium"]`},
		[]string{"withMountedDirectory", `path:"/src"`},
	)
	browsers := lastCall(c, "withExec", `"playwright@1.62.0"`)
	mount := lastCall(c, "withMountedDirectory", `path:"/src"`)
	if browsers < 0 || mount < browsers {
		t.Errorf("the browsers are installed after the tree is mounted:\n%s", c)
	}
	if hasCall(c, "withExec", "expect:ANY", `"playwright@1.62.0"`) {
		t.Errorf("the browser install is provisioning and takes the default Expect:\n%s", c)
	}
}

// A lockfile that names no Playwright installs nothing; the atom says why.
func TestVisualInstallsNoBrowsersWithoutAPlaywrightPin(t *testing.T) {
	for name, lock := range map[string]string{"none": "{}", "absent": ""} {
		engine.reset()
		tree := map[string]string{"package.json": "{}"}
		if lock != "" {
			tree["bun.lock"] = lock
		}
		engine.withTree(tree)
		_, c := runFleetAtom(t, newRun(dag.Directory(), "", ""), "ts:visual")
		if strings.Contains(c, "playwright@") {
			t.Errorf("%s: browsers installed:\n%s", name, c)
		}
	}
}

// The atom is handed the commit its artifact is tagged by, the origin it takes
// the repository name from and, from the Dagger runner, the static login.
func TestVisualIsHandedTheCommitTheOriginAndTheLogin(t *testing.T) {
	sha := strings.Repeat("a", 40)
	auth := dag.SetSecret("visual-artifact-auth", `{"auths":{}}`)
	engine.reset()
	engine.withTree(map[string]string{"package.json": "{}", "bun.lock": visualLockTree})
	r := newRun(dag.Directory(), "http://door/rob/gijmo-ui.git", "").withArtifacts(auth, sha)
	_, c := runFleetAtom(t, r, "ts:visual")
	wantCalls(t, c,
		[]string{"withEnvVariable", `name:"ATOM_ORIGIN"`, `value:"http://door/rob/gijmo-ui.git"`},
		[]string{"withEnvVariable", `name:"ATOM_SHA"`, `value:"` + sha + `"`},
		[]string{"withSecretVariable", `name:"VISUAL_ARTIFACT_AUTH"`},
	)

	engine.reset()
	engine.withTree(map[string]string{"package.json": "{}", "bun.lock": visualLockTree})
	_, c = runFleetAtom(t, newRun(dag.Directory(), "", ""), "ts:visual")
	if strings.Contains(c, "ATOM_SHA") || strings.Contains(c, "VISUAL_ARTIFACT_AUTH") {
		t.Errorf("a run with no commit and no login was handed them:\n%s", c)
	}
}

// gate-file hands the visual lane's registry credential, and the commit it
// tags by, to the run that grades it.
func TestGateFileKeepsTheArtifactCredential(t *testing.T) {
	m := gateOn(t, cleanVector)
	auth := dag.SetSecret("visual-artifact-auth", `{"auths":{}}`)
	if _, err := m.GateFile(context.Background(), fakeTree, gatePin, "base-sha", "visual", nil, false, nil, auth, "", nil, ""); err != nil || m.artifactAuth != auth {
		t.Fatalf("artifactAuth %v err %v", m.artifactAuth, err)
	}
	r := newRun(dag.Directory(), "", "").withArtifacts(auth, strings.Repeat("b", 40))
	if r.artifactAuth != auth || r.sha != strings.Repeat("b", 40) {
		t.Errorf("%+v", r)
	}
	if laneOf(checks.StageVisual) != "visual" {
		t.Errorf("the visual stage's record is labelled %q", laneOf(checks.StageVisual))
	}
	for _, a := range checks.AtomsForStage(checks.StageVisual) {
		if a.ID != "ts:visual" || checks.IsPullPath(a.Stage) {
			t.Errorf("%+v", a)
		}
	}
}
