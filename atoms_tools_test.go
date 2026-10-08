package main

import (
	"context"
	"path"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// THE LAYERS ARE ORDERED BY HOW OFTEN THEY CHANGE, and the atoms binary is the
// top one: an edit to an atom must rebuild that layer and nothing below it, and
// a tool's pin moving must not rebuild the OS packages under it. The plan is
// the data the build follows, so this holds the order the build has: the OS,
// the pinned tools least-moved first, the python layers in dependency order,
// the binary.
func TestToolsPlanOrdersLayersByVolatility(t *testing.T) {
	plan := toolsPlan()
	if len(plan) != len(pinnedTools)+len(pythonPlan)+2 {
		t.Fatalf("%d layers for %d tools and %d python layers", len(plan), len(pinnedTools), len(pythonPlan))
	}
	if plan[0].kind != layerOS || plan[len(plan)-1].kind != layerBinary || plan[len(plan)-1].name != "atoms" {
		t.Errorf("the OS must be first and the atoms binary last: %v", plan)
	}
	seen := map[string]bool{}
	moves := 0
	middle := plan[1 : len(plan)-1]
	tools, python := middle[:len(pinnedTools)], middle[len(pinnedTools):]
	for _, l := range tools {
		if l.kind != layerTool || l.tool == nil || l.tool.name != l.name {
			t.Errorf("layer %q between the OS and the python layers is not a tool: %+v", l.name, l)
			continue
		}
		if seen[l.name] {
			t.Errorf("tool %s is layered twice", l.name)
		}
		seen[l.name] = true
		if l.tool.moves < moves {
			t.Errorf("%s (pin moved on %d days) sits above a tool that moved on %d: the least-moved layers go first", l.name, l.tool.moves, moves)
		}
		moves = l.tool.moves
	}
	var names []string
	for _, l := range python {
		if l.kind != layerPython || l.tool != nil {
			t.Errorf("layer %q above the tools is not a python layer: %+v", l.name, l)
		}
		names = append(names, l.name)
	}
	if want := "uv python python-packages ansible-collections"; strings.Join(names, " ") != want {
		t.Errorf("the python layers are %v, want %s: each needs the one before it", names, want)
	}
}

// Each tool has exactly one source, and a release asset is held to a checksum
// and to the pin the chains fetch: the version and URL are the module's
// existing ones, not a second spelling.
func TestEveryPinnedToolHasOneVerifiedSource(t *testing.T) {
	for _, tool := range pinnedTools {
		sources := 0
		for _, set := range []bool{tool.url != "", tool.image != "", tool.build != nil} {
			if set {
				sources++
			}
		}
		if sources != 1 {
			t.Errorf("%s has %d sources", tool.name, sources)
		}
		if tool.image != "" && !strings.Contains(tool.image, "@sha256:") {
			t.Errorf("%s comes from %q, which is not pinned by digest", tool.name, tool.image)
		}
		if tool.url == "" {
			continue
		}
		if _, ok := checks.ToolSHA256[tool.url]; !ok {
			t.Errorf("%s: %s has no checksum", tool.name, tool.url)
		}
		if !strings.HasPrefix(tool.url, "https://") {
			t.Errorf("%s is fetched over %s", tool.name, tool.url)
		}
		if tool.member != "" && path.Base(tool.member) != tool.name {
			t.Errorf("%s: the tarball member %q is a different program", tool.name, tool.member)
		}
	}
	// The set of checksummed URLs is exactly the set of release assets fetched.
	var fetched []string
	for _, tool := range pinnedTools {
		if tool.url != "" {
			fetched = append(fetched, tool.url)
		}
	}
	if len(fetched) != len(checks.ToolSHA256) {
		t.Errorf("%d release assets are fetched and %d checksums are held", len(fetched), len(checks.ToolSHA256))
	}
}

// EVERY PROGRAM THE ATOMS EXEC IS ON THE CONTAINER'S PATH: git and tar from the
// base system, everything else a layer. An atom that execs a program no layer
// carries is a could-not-run on every shadow, and looks like a finding about
// the binary.
func TestToolsContainerCarriesEveryProgram(t *testing.T) {
	layered := map[string]bool{}
	for _, l := range toolsPlan() {
		layered[l.name] = true
		for _, p := range l.provides {
			layered[p] = true
		}
	}
	for _, p := range atoms.Programs {
		if p == "git" || p == "tar" {
			continue
		}
		if !layered[p] {
			t.Errorf("the atoms exec %q and no layer of the tools container carries it", p)
		}
	}
	for _, tool := range pinnedTools {
		if !slices.Contains(atoms.Programs, tool.name) {
			t.Errorf("the container carries %q and no atom execs it", tool.name)
		}
	}
}

// The container is built from the pinned Debian digest and the binary is applied
// last in the query the engine receives, not only in the plan.
func TestAtomsToolsAppliesTheBinaryLastOnTheDebianDigest(t *testing.T) {
	engine.reset()
	if _, err := atomsTools(context.Background()).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := engine.chain(`path:"/usr/local/bin/atoms"`, `from(address:"`+checks.ImageTools+`")`)
	if c == "" {
		t.Fatalf("no chain built the container; the engine saw:\n%s", strings.Join(engine.chains(), "\n"))
	}
	last := strings.Index(c, `path:"/usr/local/bin/atoms"`)
	prev := strings.Index(c, `"apt-get","update"`)
	if prev < 0 {
		t.Fatalf("the OS layer is not in the chain:\n%s", c)
	}
	for _, l := range toolsPlan() {
		if l.kind != layerTool {
			continue
		}
		at := strings.Index(c, `path:"/usr/local/bin/`+l.name+`"`)
		if at < 0 {
			t.Errorf("the %s layer is not in the chain", l.name)
			continue
		}
		if at < prev || at > last {
			t.Errorf("%s is applied at %d, outside the OS layer (%d) and the binary (%d)", l.name, at, prev, last)
		}
		prev = at
	}
	for _, pkg := range []string{`"git"`, `"ca-certificates"`, `"xz-utils"`} {
		if !strings.Contains(c, pkg) {
			t.Errorf("the OS layer does not install %s", pkg)
		}
	}
}

// A download that does not match its pinned checksum is not installed, and the
// others still are: one tool must not take the whole container with it.
func TestAToolThatFailsItsChecksumIsLeftOut(t *testing.T) {
	engine.reset()
	engine.failLeaf(checks.ToolSHA256[checks.OpaURL], "sync", "sha256sum: FAILED")
	if _, err := atomsTools(context.Background()).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := engine.chain(`path:"/usr/local/bin/atoms"`)
	if strings.Contains(c, `path:"/usr/local/bin/opa"`) {
		t.Errorf("opa was installed though its download did not verify")
	}
	for _, still := range []string{"hadolint", "opengrep", "orbitparse", "kubeconform"} {
		if !strings.Contains(c, `path:"/usr/local/bin/`+still+`"`) {
			t.Errorf("%s was dropped with it", still)
		}
	}
	if sums := engine.chain(`"sha256sum","-c"`, checks.ToolSHA256[checks.HadolintURL]); sums == "" {
		t.Errorf("hadolint's download was not checked against its checksum")
	}
}

// onTools keys the run to the call and mounts the tree above every layer.
func TestOnToolsMountsTheTreeAndTheReaskAboveTheLayers(t *testing.T) {
	engine.reset()
	r := newRun(dag.Directory(), "http://door/rob/x.git", "abc").reasked("n0nce")
	if _, err := r.onTools(atomsTools(context.Background())).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := engine.chain(`CA_REASK`, `path:"/usr/local/bin/atoms"`)
	if c == "" {
		t.Fatalf("no chain; the engine saw:\n%s", strings.Join(engine.chains(), "\n"))
	}
	if strings.Index(c, `CA_REASK`) < strings.Index(c, `path:"/usr/local/bin/atoms"`) {
		t.Errorf("the per-call key sits below the binary layer, so every call would rebuild it")
	}
	for _, want := range []string{`"CI"`, `"OTEL_SDK_DISABLED"`, `path:"/src"`} {
		if !strings.Contains(c, want) {
			t.Errorf("the run lacks %s", want)
		}
	}
}

// toolNamed is a tool of the plan, by name.
func toolNamed(t *testing.T, name string) pinnedTool {
	t.Helper()
	for _, tool := range pinnedTools {
		if tool.name == name {
			return tool
		}
	}
	t.Fatalf("no tool %q in the plan", name)
	return pinnedTool{}
}

// toolFile checks a download against its pinned checksum in a container of its
// own and answers the program: a tarball's one member is extracted with its
// leading directories stripped, and a bare binary is the download itself.
func TestToolFileVerifiesThenExtractsOnlyWhatTheAssetIs(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		tool    string
		tar     string
		noTar   bool
		path    string
		sumFrom string
	}{
		{"shellcheck", `"tar","xf","/tmp/pkg","-C","/out","--strip-components=1","` + checks.ShellcheckMember + `"`, false, "/out/shellcheck", checks.ShellcheckURL},
		{"wasm-tools", `"tar","xf","/tmp/pkg","-C","/out","--strip-components=1","` + checks.WasmToolsMember + `"`, false, "/out/wasm-tools", checks.WasmToolsURL},
		{"just", `"tar","xf","/tmp/pkg","-C","/out","just"]`, false, "/out/just", checks.JustURL},
		{"hadolint", "", true, "/tmp/pkg", checks.HadolintURL},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			engine.reset()
			f, err := toolFile(ctx, toolsOS(), toolNamed(t, tc.tool))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := dag.Container().From("scratch").WithFile("/b", f).Stdout(ctx); err != nil {
				t.Fatal(err)
			}
			verify := engine.chain(`"sha256sum","-c","/tmp/pkg.sha256"`, checks.ToolSHA256[tc.sumFrom])
			if verify == "" {
				t.Fatalf("the download was not checked against its pinned checksum; the engine saw:\n%s", strings.Join(engine.chains(), "\n"))
			}
			if tc.noTar {
				if strings.Contains(verify, `"tar"`) || strings.Contains(verify, `"mkdir"`) {
					t.Errorf("a bare binary was unpacked:\n%s", verify)
				}
			} else {
				if !strings.Contains(verify, tc.tar) {
					t.Errorf("the tarball was not unpacked as %s:\n%s", tc.tar, verify)
				}
				if sha, mk, tar := strings.Index(verify, `"sha256sum"`), strings.Index(verify, `"mkdir","-p","/out"`), strings.Index(verify, `"tar","xf"`); !(sha < mk && mk < tar) {
					t.Errorf("the checksum, the directory and the extraction are out of order (%d, %d, %d):\n%s", sha, mk, tar, verify)
				}
				if tc.tool == "just" && strings.Contains(verify, "--strip-components") {
					t.Errorf("a member at the root was stripped:\n%s", verify)
				}
			}
			if engine.chain(`file(path:"`+tc.path+`")`) == "" {
				t.Errorf("the program is not read from %s", tc.path)
			}
		})
	}
}

func TestToolFileRefusesWhatItCannotVerify(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		tool   pinnedTool
		script func()
		want   string
	}{
		{"a release asset with no checksum", pinnedTool{name: "x", url: "https://example.invalid/x"}, func() {}, "has no checksum"},
		{"an asset that will not fetch", toolNamed(t, "hadolint"), func() { engine.failLeaf(checks.HadolintURL, "sync", "dial tcp: i/o timeout") }, "could not fetch"},
		{"an asset that fails its checksum", toolNamed(t, "hadolint"), func() { engine.failLeaf(checks.ToolSHA256[checks.HadolintURL], "sync", "sha256sum: FAILED") }, "did not verify against its pinned checksum"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine.reset()
			tc.script()
			f, err := toolFile(ctx, toolsOS(), tc.tool)
			if err == nil || f != nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, %v; want an error containing %q", f, err, tc.want)
			}
		})
	}
	for _, name := range []string{"kubeconform", "orbitparse"} {
		t.Run(name+" needs no download", func(t *testing.T) {
			engine.reset()
			if f, err := toolFile(ctx, toolsOS(), toolNamed(t, name)); err != nil || f == nil {
				t.Errorf("got %v, %v", f, err)
			}
		})
	}
}
