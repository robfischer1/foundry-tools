package main

import (
	"os"
	"path"
	"regexp"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
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
		[]string{"withMountedDirectory", `path:"/orbitparse"`},
		[]string{"withWorkdir", `path:"/orbitparse"`},
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

// THE READER'S MODULE IS THE ONE THE BUILD MOUNTS: the directory named by
// orbitParseDir holds a go.mod Renovate can read, a go.sum that pins the
// version it requires, and the main over policy.ParseACL. A rename that left
// the constant behind would build an empty directory on the cluster.
func TestTheOrbitReaderModuleIsARealPinnedModule(t *testing.T) {
	mod := readRepoFile(t, path.Join(orbitParseDir, "go.mod"))
	m := regexp.MustCompile(`(?m)^require git\.notusmi\.com/rob/stellar-core-go (v\d+\.\d+\.\d+)$`).FindStringSubmatch(mod)
	if m == nil {
		t.Fatalf("the reader's go.mod requires no stellar-core-go version on a line Renovate's gomod manager reads:\n%s", mod)
	}
	sum := readRepoFile(t, path.Join(orbitParseDir, "go.sum"))
	if !strings.Contains(sum, "git.notusmi.com/rob/stellar-core-go "+m[1]+" h1:") {
		t.Fatalf("go.sum pins no hash for stellar-core-go %s — the build would refuse it", m[1])
	}
	main := readRepoFile(t, path.Join(orbitParseDir, "main.go"))
	if !strings.Contains(main, `"git.notusmi.com/rob/stellar-core-go/policy"`) || !strings.Contains(main, "policy.ParseACL") {
		t.Fatal("the reader no longer parses with the star's own policy.ParseACL")
	}
	if dirs := checks.GoModuleDirs([]string{"go.mod", path.Join(orbitParseDir, "go.mod")}); len(dirs) != 1 || dirs[0] != "." {
		t.Fatalf("the reader's module must stay out of the lanes' module enumeration, got %v", dirs)
	}
}

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
