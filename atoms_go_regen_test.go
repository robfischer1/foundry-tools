package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

const regenRev = "f450dd4c17fabbc2820904727f2d07f3f4916eb7"

func gravityProv(rev string) string {
	return `{"generator":{"command":"gravity --name x-core --package x ./guest.wasm","commit":"` + rev + `"}}`
}

func regenTree(extra map[string]string) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(extra)
	engine.stdout(`"go","list"`, "11\n")
}

func TestGoTestStagesGravityAtThePinnedRevAndRequiresTheRegenTests(t *testing.T) {
	regenTree(map[string]string{
		"classescore/provenance.json": gravityProv(regenRev),
		"classescore/regen_test.go":   "package classescore\n",
		"stamp/provenance.json":       gravityProv(regenRev),
		"stamp/regen_test.go":         "package stamp\n",
		"alarm/provenance.json":       gravityProv("824e00f7108f62fac3c5e0d730e3bded5673da5e"), // no regen test: not held
	})
	v := runAtom(t, "go:test", "")
	wantState(t, v, 0, "gravity "+regenRev+" staged on PATH", "CLASSESCORE_REGEN_REQUIRED=1, STAMP_REGEN_REQUIRED=1")

	c := engine.chain(`"go","test","./..."`, "exitCode")
	wantCalls(t, c,
		[]string{"withFile", `path:"/usr/local/bin/gravity"`, `permissions:493`},
		[]string{"withEnvVariable", `name:"CLASSESCORE_REGEN_REQUIRED"`, `value:"1"`},
		[]string{"withEnvVariable", `name:"STAMP_REGEN_REQUIRED"`, `value:"1"`},
	)
	if hasCall(c, "withEnvVariable", `ALARM_REGEN_REQUIRED`) {
		t.Errorf("a package with no regen test is not held to one:\n%s", c)
	}
	// The binary is gravity at the rev, built by cargo from the fleet's fork,
	// on the rust image, and asked its version before it is trusted.
	g := engine.chain(`"cargo","install"`, "file(")
	if !strings.Contains(g, checks.ImageRust) {
		t.Errorf("gravity builds on the rust lane image:\n%s", g)
	}
	wantCalls(t, g,
		[]string{"withExec", `"cargo","install","--locked","--git","` + checks.GravityGit + `","--rev","` + regenRev + `","--root","/opt/gravity","arcjet-gravity"`},
		[]string{"withExec", `"/opt/gravity/bin/gravity","--version"`},
		[]string{"file", `path:"/opt/gravity/bin/gravity"`},
	)
	// The regen tests run in the SAME exec as the rest of the suite.
	if !hasCall(c, "withExec", `expect:ANY`, `"go","test","./..."`) {
		t.Errorf("the suite runs under ANY:\n%s", c)
	}
}

func TestGoTestStagesNothingWithoutAGravityPackageThatHasTheTest(t *testing.T) {
	regenTree(map[string]string{
		"alarm/provenance.json": gravityProv(regenRev),
		"other/regen_test.go":   "package other\n",
		"ts/provenance.json":    `{"tools":{"jco":"1.35.0"}}`,
	})
	wantState(t, runAtom(t, "go:test", ""), 0, "no package pairs a gravity-generated provenance.json with a regen_test.go")
	c := engine.chain(`"go","test","./..."`, "exitCode")
	if hasCall(c, "withFile", `/usr/local/bin/gravity`) || strings.Contains(c, "REGEN_REQUIRED") {
		t.Errorf("nothing is staged where nothing opted in:\n%s", c)
	}
	if engine.chain(`"cargo","install"`) != "" {
		t.Error("gravity must not be built for a tree that does not use it")
	}
}

func TestGoTestFilesAProvenanceItCannotHonourAsAFinding(t *testing.T) {
	regenTree(map[string]string{
		"a/provenance.json": gravityProv(regenRev),
		"a/regen_test.go":   "",
		"b/provenance.json": gravityProv("824e00f7108f62fac3c5e0d730e3bded5673da5e"),
		"b/regen_test.go":   "",
	})
	wantState(t, runAtom(t, "go:test", ""), 1, "FINDINGS", "a pins gravity "+regenRev, "b pins 824e00f")

	regenTree(map[string]string{"a/provenance.json": "{", "a/regen_test.go": ""})
	wantState(t, runAtom(t, "go:test", ""), 1, "FINDINGS", "a/provenance.json is not JSON")
}

func TestGoTestCannotRunWhenTheTreeWillNotShowItsProvenance(t *testing.T) {
	for name, fail := range map[string][3]string{
		"provenance listing": {`provenance.json`, "glob", "engine: glob failed"},
		"regen test listing": {`regen_test.go`, "glob", "engine: glob failed"},
		"provenance read":    {`a/provenance.json`, "contents", "engine: read failed"},
	} {
		regenTree(map[string]string{"a/provenance.json": gravityProv(regenRev), "a/regen_test.go": ""})
		engine.failLeaf(fail[0], fail[1], fail[2])
		wantState(t, runAtom(t, "go:test", ""), 2, "CANNOT RUN", "the tree's provenance could not be read", fail[2])
		if t.Failed() {
			t.Fatalf("case %q", name)
		}
	}
}
