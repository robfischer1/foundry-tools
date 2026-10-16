package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE RELEASE ATOMS' ENVIRONMENT, against the paper engine: the container each
// runs in and how it is handed the tree. What each decides is tested beside its
// atom (atoms/<ns>/release) and in internal/releasekit.

// releaseVerdict is the verdict a release atom wrote.
func releaseVerdict(id, lane string) string {
	return `{"atom":"` + id + `","stage":"prepush","lane":"` + lane + `","state":0,"result":"pass","logs":[]}`
}

// runRelease runs a release atom as a door's job does, naming the clone.
func runRelease(t *testing.T, id, repo string) string {
	t.Helper()
	needle := strings.Replace(id, ":", "__", 1) + ".json"
	engine.contents(needle, releaseVerdict(id, id[:strings.Index(id, ":")]))
	wantState(t, registry[id](context.Background(), newRun(dag.Directory(), repo, "")), 0)
	return engine.chain(needle, "contents")
}

// A release atom is handed tree@1 built from the engine's population, and the
// clone the repository came from, in place of a repository git can read: the
// image's own base carries no git.
func TestTheReleaseAtomsAreHandedTheTreeAsFacts(t *testing.T) {
	star := map[string]string{
		"Dockerfile": copiesHades, ".copier-answers.yml": "service_name: hades\n", "go.mod": "module x\n",
		"Cargo.toml": "[package]\n", "package.json": "{}",
	}
	for _, id := range []string{"go:release", "rust:release", "ts:release", "python:release"} {
		engine.reset()
		engine.withTree(star)
		c := runRelease(t, id, "http://ourea.default.svc.cluster.local:8215/hades.git")
		wantCalls(t, c,
			[]string{"withNewFile", `path:"/work/tree.json"`, `\"version\":1`, `\"origin\":\"http://ourea.default.svc.cluster.local:8215/hades.git\"`, `Dockerfile`},
			[]string{"withEnvVariable", `name:"ATOM_TREE"`, `value:"/work/tree.json"`},
			[]string{"withExec", "expect:ANY", `args:["/atom","/src"]`},
		)
		for _, n := range []string{`"git","init"`, `name:"ATOM_BASE"`, `name:"GATE_BASE"`, `name:"ATOM_FORK"`} {
			if strings.Contains(c, n) {
				t.Errorf("%s: a release reads nothing of the change or the repository's history (%s):\n%s", id, n, c)
			}
		}
	}
	// The hook names the clone with --origin, not --repo.
	engine.reset()
	engine.withTree(star)
	engine.contents("go__release.json", releaseVerdict("go:release", "go"))
	r := newRun(dag.Directory(), "", "").fromOrigin("http://ourea.notusmi.com:8215/hephaestus.git")
	wantState(t, registry["go:release"](context.Background(), r), 0)
	if c := engine.chain("go__release.json", "contents"); !strings.Contains(c, `\"origin\":\"http://ourea.notusmi.com:8215/hephaestus.git\"`) {
		t.Errorf("the hook's clone did not reach the atom:\n%s", c)
	}
	// A population that would not enumerate rides as the tree's error, for the
	// atom to word.
	engine.reset()
	engine.withTree(star)
	engine.fail("glob", "the tree went away")
	engine.contents("go__release.json", releaseVerdict("go:release", "go"))
	registry["go:release"](context.Background(), newRun(dag.Directory(), "", ""))
	if c := engine.chain("go__release.json", "contents"); !strings.Contains(c, `files_err`) || !strings.Contains(c, "the tree went away") {
		t.Errorf("the population's error did not reach the atom:\n%s", c)
	}
}

// The go release runs in the plain go lane on the whole tree, and the dies are
// mounted for the star's record.
func TestGoReleaseRunsInTheGoLane(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"Dockerfile": copiesHades, ".copier-answers.yml": "service_name: hades\n", "go.mod": "module x\n"})
	c := runRelease(t, "go:release", "")
	if !strings.Contains(c, checks.ImageGo) {
		t.Errorf("go:release must run in the go lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withMountedDirectory", `path:"/dies"`},
		[]string{"withEnvVariable", `name:"ATOM_DIES"`, `value:"/dies"`},
	)
}

// rust:release runs in the fetched lane with the release volume as the cargo
// target: shared, mounted before the target is named, and no helper binary to
// carry a file out of it, since the atom carries its own.
func TestRustReleaseRunsOnTheFetchedLayerWithTheReleaseVolumeAsItsTarget(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/tron /tron\n", ".copier-answers.yml": "service_name: tron\n", "Cargo.toml": "[package]\n"})
	c := runRelease(t, "rust:release", "")
	wantCalls(t, c,
		[]string{"withExec", `args:["cargo","fetch","--locked"]`},
		[]string{"withMountedCache", `path:"/cache/cargo-release"`, `sharing:SHARED`},
		[]string{"withEnvVariable", `name:"CARGO_TARGET_DIR"`, `value:"/cache/cargo-release"`},
	)
	fetch, vol := strings.Index(c, `"cargo","fetch","--locked"`), lastCall(c, "withMountedCache", `path:"/cache/cargo-release"`)
	target := lastCall(c, "withEnvVariable", `name:"CARGO_TARGET_DIR"`)
	if !(fetch >= 0 && vol >= 0 && vol < target) {
		t.Errorf("the target must be named after the volume that backs it (fetch %d, volume %d, target %d):\n%s", fetch, vol, target, c)
	}
	if strings.Contains(c, "copyout") {
		t.Errorf("the atom carries its own binaries out; no helper is mounted:\n%s", c)
	}
	if hasCall(c, "withEnvVariable", `name:"CARGO_BUILD_JOBS"`) {
		t.Errorf("the bound is the atom's, set on its execs:\n%s", c)
	}
}

// ts:release and python:release run in the base the image's Dockerfile FROMs,
// with the toolchain's cache and the tree at /src; an image on another base
// gets the toolchain's own lane, which the atom stands down in.
func TestTheBaseBuiltReleasesRunOnTheImagesBase(t *testing.T) {
	cases := []struct {
		id, base, cache string
		env             []string
		tree            map[string]string
		lane            string
	}{
		{"ts:release", bunBase, "/root/.bun/install/cache", nil, bunStar(), checks.ImageTS},
		{"python:release", pyBase, "/opt/uv-cache", []string{`name:"UV_CACHE_DIR"`, `value:"/opt/uv-cache"`}, pythonStar(), checks.ImagePython},
	}
	for _, c := range cases {
		engine.reset()
		engine.withTree(c.tree)
		chain := runRelease(t, c.id, "")
		wantCalls(t, chain,
			[]string{"from", `address:"` + c.base + `"`},
			[]string{"withMountedCache", `path:"` + c.cache + `"`},
			[]string{"withMountedDirectory", `path:"/src"`},
			[]string{"withWorkdir", `path:"/src"`},
		)
		if c.env != nil {
			wantCalls(t, chain, append([]string{"withEnvVariable"}, c.env...))
		}
		if strings.Contains(chain, c.lane) {
			t.Errorf("%s: an image on its base runs there, not in the toolchain's lane:\n%s", c.id, chain)
		}

		// the Dockerfile is on another base: the toolchain's lane
		engine.reset()
		other := map[string]string{}
		for k, v := range c.tree {
			other[k] = strings.ReplaceAll(v, c.base, goBase)
		}
		engine.withTree(other)
		if chain := runRelease(t, c.id, ""); !strings.Contains(chain, c.lane) || strings.Contains(chain, c.base) {
			t.Errorf("%s: an image on another base stands down in the toolchain's lane:\n%s", c.id, chain)
		}
	}
}
