package main

import (
	"strings"
	"testing"
)

// orbitTool is the parse exec's needle.
const orbitTool = `"/usr/local/bin/orbitparse","/src/`

func orbitTree() {
	opsTree(map[string]string{
		"clusters/home/kustomization.yaml":  "resources: []\n",
		"prime/orbits/kustomization.yaml":   "# owned\n",
		"prime/orbits/urania.orbit.toml":    "[[produces]]\n",
		"prime/orbits/athena.orbit.toml":    "[[consumes]]\n",
		"prime/orbits/notes.orbit.toml.bak": "x",
	}, nil)
}

// A star is not an ops tree: no listing past the surface, no build.
func TestOrbitSidecarsStandsDownOffTheOpsShape(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module x\n", "urania.orbit.toml": "x"})
	wantState(t, runAtom(t, "ops:orbit-sidecars", ""), 0, "ABSENT", "no ops shape")
	if engine.chain("orbitparse") != "" {
		t.Errorf("no ops shape means no tool:\n%s", engine.chain("orbitparse"))
	}
}

// An ops tree that tracks no sidecar has nothing a star reads: ABSENT, and
// the tool is never built.
func TestOrbitSidecarsIsAbsentWithoutASidecar(t *testing.T) {
	opsTree(map[string]string{"flux/x.yaml": "a: 1\n", "prime/orbits/kustomization.yaml": "x"}, nil)
	v := runAtom(t, "ops:orbit-sidecars", "")
	wantState(t, v, 0, "ABSENT", "tracks no composed orbit sidecar")
	if engine.chain("orbitparse") != "" {
		t.Errorf("nothing to parse means no tool:\n%s", engine.chain("orbitparse"))
	}
}

// The tool is the embedded main over stellar-core-go policy.ParseACL, built
// with its own go.mod and go.sum under the default Expect, and it is handed
// every tracked sidecar, sorted, under /src — and nothing that is not one.
func TestOrbitSidecarsBuildsTheReaderAndParsesEverySidecar(t *testing.T) {
	orbitTree()
	wantState(t, runAtom(t, "ops:orbit-sidecars", ""), 0)

	b := engine.chain(`"go","build"`)
	wantCalls(t, b,
		[]string{"withNewFile", `path:"/orbitparse/main.go"`, "git.notusmi.com/rob/stellar-core-go/policy", "policy.ParseACL"},
		[]string{"withNewFile", `path:"/orbitparse/go.mod"`, "require git.notusmi.com/rob/stellar-core-go v0.66.0"},
		[]string{"withNewFile", `path:"/orbitparse/go.sum"`, "git.notusmi.com/rob/stellar-core-go v0.66.0 h1:"},
		[]string{"withExec", `args:["go","build","-trimpath","-o","/out/orbitparse","."]`},
	)
	if hasCall(b, "withExec", `"go","build"`, `expect:ANY`) {
		t.Errorf("the build is provisioning and must run under the default Expect:\n%s", b)
	}
	if !strings.Contains(b, "GOPROXY") {
		t.Errorf("the build carries no GOPROXY:\n%s", b)
	}
	c := engine.chain(orbitTool, "exitCode")
	wantCalls(t, c,
		[]string{"withFile", `path:"/usr/local/bin/orbitparse"`, "permissions:493"},
		[]string{"withExec", `expect:ANY`, `args:["/usr/local/bin/orbitparse","/src/prime/orbits/athena.orbit.toml","/src/prime/orbits/urania.orbit.toml"]`},
	)
	if strings.Contains(c, "notes.orbit.toml.bak") || strings.Contains(c, `"/src/prime/orbits/kustomization.yaml"`) {
		t.Errorf("a file that is not a sidecar was handed to the reader:\n%s", c)
	}
}

// The tool's 0/1/2 reach the verdict untouched; a build that fails is the
// engine's, never a finding.
func TestOrbitSidecarsPassesTheReadersExitThrough(t *testing.T) {
	orbitTree()
	engine.exitCode(orbitTool, 1)
	engine.stdout(orbitTool, `REFUSED  /src/prime/orbits/urania.orbit.toml: urania: two produces blocks name consumer "athena"`)
	wantState(t, runAtom(t, "ops:orbit-sidecars", ""), 1, "two produces blocks")

	orbitTree()
	engine.exitCode(orbitTool, 2)
	engine.stderr(orbitTool, "CANNOT RUN - open /src/x: permission denied")
	wantState(t, runAtom(t, "ops:orbit-sidecars", ""), 2, "CANNOT RUN")

	orbitTree()
	engine.exitCode(orbitTool, 137)
	wantState(t, runAtom(t, "ops:orbit-sidecars", ""), 2)

	orbitTree()
	engine.fail(`"go","build"`, "go: git.notusmi.com/rob/stellar-core-go@v0.66.0: 502 Bad Gateway")
	wantState(t, runAtom(t, "ops:orbit-sidecars", ""), 2, "the atom never ran", "502")
}

// A tree that would not enumerate is a stop, not an absence.
func TestOrbitSidecarsCannotEnumerateIsTwo(t *testing.T) {
	orbitTree()
	engine.fail(opsLsNeedle, "the tree went away")
	wantState(t, runAtom(t, "ops:orbit-sidecars", ""), 2)
}
