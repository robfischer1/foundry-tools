package main

import (
	"bytes"
	"context"
	"os"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

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

func capturedLog(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	old := toolsLog
	toolsLog = &buf
	t.Cleanup(func() { toolsLog = old })
	return func() string {
		toolsLogMu.Lock()
		defer toolsLogMu.Unlock()
		return buf.String()
	}
}

func TestADroppedToolIsLoggedWithItsCause(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tool  string
		fail  func()
		cause string
	}{
		{"a checksum that does not match", "opa", func() { engine.failLeaf(checks.ToolSHA256[checks.OpaURL], "sync", "sha256sum: FAILED") }, "did not verify against its pinned checksum"},
		{"an asset that will not fetch", "hadolint", func() { engine.failLeaf(checks.HadolintURL, "sync", "dial tcp: i/o timeout") }, "could not fetch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine.reset()
			log := capturedLog(t)
			tc.fail()
			if _, err := atomsTools(context.Background()).Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			var lines []string
			for _, l := range strings.Split(strings.TrimSpace(log()), "\n") {
				if strings.Contains(l, " "+tc.tool+" ") {
					lines = append(lines, l)
				}
			}
			if len(lines) != 1 || !strings.HasPrefix(lines[0], "atoms tools: "+tc.tool+" left out of the container: ") || !strings.Contains(lines[0], tc.cause) {
				t.Errorf("the log for %s is %q, want one line naming %q", tc.tool, lines, tc.cause)
			}
			if got := strings.Count(log(), "left out of the container"); got != 1 {
				t.Errorf("%d lines, want one: a tool that is there is not logged\n%s", got, log())
			}
		})
	}
	t.Run("a container with every layer logs nothing", func(t *testing.T) {
		engine.reset()
		log := capturedLog(t)
		if _, err := atomsTools(context.Background()).Sync(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := log(); got != "" {
			t.Errorf("nothing was left out and the log says %q", got)
		}
	})
}

func TestAtomsToolsBuildsThePythonLayersFromTheLock(t *testing.T) {
	engine.reset()
	capturedLog(t)
	if _, err := atomsTools(context.Background()).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	stage := engine.chain(`"collection","install"`)
	if stage == "" {
		t.Fatalf("no stage installed the collections; the engine saw:\n%s", strings.Join(engine.chains(), "\n"))
	}
	wantCalls(t, stage,
		[]string{"withEnvVariable", `name:"UV_PYTHON_INSTALL_DIR"`, `value:"/opt/uv-python"`},
		[]string{"withEnvVariable", `name:"UV_NATIVE_TLS"`, `value:"1"`},
		[]string{"withFile", `path:"/usr/local/bin/uv"`},
		[]string{"withExec", `args:["uv","python","install","` + checks.FleetPython + `"]`},
		[]string{"withExec", `"uv","venv","/opt/atoms-py"`, `"--managed-python"`},
		[]string{"withFile", `path:"/tmp/pytools/requirements.txt"`},
		// The install is the lock's: hashes required, nothing built, so a wheel
		// that does not match is a failed build and not a different program.
		[]string{"withExec", `"uv","pip","install"`, `"--require-hashes"`, `"--no-build"`, `"-r","/tmp/pytools/requirements.txt"`},
		[]string{"withExec", `"import yaml, jsonschema, tomllib, tomli, ansible, ansiblelint, copier"`},
		[]string{"withExec", `"/opt/atoms-py/bin/ansible-playbook","--version"`},
		[]string{"withExec", `"/opt/atoms-py/bin/copier","--version"`},
		[]string{"withFile", `path:"/tmp/pytools/ansible-collections.yml"`},
		[]string{"withExec", `"/opt/atoms-py/bin/ansible-galaxy","collection","install","-r","/tmp/pytools/ansible-collections.yml","-p","/opt/ansible-collections"`},
	)
	// uv is the pinned image's binary, taken by file.
	if engine.chain(`from(address:"`+checks.ImageUV+`")`, `file(path:"/uv")`) == "" {
		t.Errorf("uv is not taken from %s", checks.ImageUV)
	}
	// Each step needs the one before it.
	prev := -1
	for _, needle := range []string{`"uv","--version"`, `"uv","python","install"`, `"uv","venv"`, `"uv","pip","install"`, `"import yaml`, `"collection","install"`} {
		at := strings.Index(stage, needle)
		if at < prev {
			t.Errorf("%s is applied before the step it depends on:\n%s", needle, stage)
		}
		prev = at
	}
	if strings.Contains(stage, "--clear-response-cache") {
		t.Errorf("the first install asked for the retry's flag:\n%s", stage)
	}

	c := engine.chain(`path:"/usr/local/bin/atoms"`)
	order := []string{
		`path:"/usr/local/bin/orbitparse"`, `path:"/usr/local/bin/uv"`, `path:"/opt/uv-python"`,
		`path:"/opt/atoms-py"`, `path:"/opt/ansible-collections"`, `path:"/usr/local/bin/atoms"`,
	}
	prev = -1
	for _, needle := range order {
		at := strings.Index(c, needle)
		if at < 0 || at < prev {
			t.Errorf("%s is at %d, after %d: the layers are the tools, uv, the interpreter, the packages, the collections, the binary:\n%s", needle, at, prev, c)
		}
		prev = at
	}
	wantCalls(t, c, []string{"withEnvVariable", `name:"PATH"`, `value:"/opt/atoms-py/bin:${PATH}"`, `expand:true`})
	if at, bin := strings.Index(c, `name:"PATH"`), strings.Index(c, `path:"/opt/atoms-py"`); at < bin {
		t.Errorf("the PATH is set before the venv it names is there")
	}
	for _, dir := range []string{"/opt/uv-python", "/opt/atoms-py", "/opt/ansible-collections"} {
		if engine.chain(`directory(path:"`+dir+`")`, "id") == "" {
			t.Errorf("%s is not taken from its stage", dir)
		}
	}
}

func TestTheCollectionInstallIsRetriedOnceWithClearResponseCache(t *testing.T) {
	engine.reset()
	log := capturedLog(t)
	engine.failLeaf(`"collection","install","-r"`, "sync", "404 Not Found")
	if _, err := atomsTools(context.Background()).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	retry := engine.chain(`"--clear-response-cache"`)
	if retry == "" {
		t.Fatalf("the install was not retried; the engine saw:\n%s", strings.Join(engine.chains(), "\n"))
	}
	wantCalls(t, retry, []string{"withExec", `"collection","install","--clear-response-cache","-r","/tmp/pytools/ansible-collections.yml"`})
	if !strings.Contains(engine.chain(`path:"/usr/local/bin/atoms"`), `path:"/opt/ansible-collections"`) {
		t.Error("the collections were left out though the retry installed them")
	}
	if got := log(); got != "" {
		t.Errorf("a retry that worked is not a drop: %q", got)
	}
}

func TestThePythonLockIsPinnedAndCoversWhatTheAtomsUse(t *testing.T) {
	in, err := os.ReadFile("pytools/requirements.in")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := os.ReadFile("pytools/requirements.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(in), "\ncopier=="+checks.CopierVersion+"\n") || !strings.Contains(string(lock), "\ncopier=="+checks.CopierVersion+" \\\n") {
		t.Errorf("copier is not pinned to checks.CopierVersion (%s) in both the requirement and the lock", checks.CopierVersion)
	}
	for _, name := range []string{"pyyaml", "tomli", "jsonschema", "ansible-core", "ansible-lint", "copier"} {
		if !regexp.MustCompile(`(?m)^` + name + `==\S+ \\$`).Match(lock) {
			t.Errorf("%s is not pinned in the lock", name)
		}
	}
	pinned := regexp.MustCompile(`(?m)^[A-Za-z0-9._-]+==[^ ]+ \\$`).FindAll(lock, -1)
	hashes := regexp.MustCompile(`(?m)^    --hash=sha256:[0-9a-f]{64}( \\)?$`).FindAll(lock, -1)
	if len(pinned) < 30 || len(hashes) < len(pinned) {
		t.Errorf("%d packages pinned with %d hashes: every package needs at least one", len(pinned), len(hashes))
	}
	for _, line := range strings.Split(string(lock), "\n") {
		if strings.Contains(line, "==") && !strings.HasSuffix(line, " \\") && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, " ") {
			t.Errorf("a pin with no hash after it: %q", line)
		}
	}
}

func TestTheCollectionsAreExactReleases(t *testing.T) {
	body, err := os.ReadFile("pytools/ansible-collections.yml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Collections []struct{ Name, Version string }
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range doc.Collections {
		got[c.Name] = c.Version
	}
	want := map[string]string{"community.routeros": "3.22.0", "vyos.vyos": "6.0.0", "ansible.netcommon": "8.6.2", "ansible.utils": "6.1.0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("collections %v, want %v", got, want)
	}
}

func TestTheChainsContractFixturesAreTheBinarys(t *testing.T) {
	if len(diesContractFixtures) != len(checks.ContractFixtures) {
		t.Fatalf("%d fixtures in the chain, %d in the binary", len(diesContractFixtures), len(checks.ContractFixtures))
	}
	for i, f := range diesContractFixtures {
		if g := checks.ContractFixtures[i]; f.name != g.Name || f.expectFail != g.ExpectFail {
			t.Errorf("fixture %d: chain %+v, binary %+v", i, f, g)
		}
	}
}

// A STAGE THAT DOES NOT BUILD IS LEFT OUT WITH ITS CAUSE, and so is every stage
// above it; the ones below still are. The atoms that need what is missing settle
// 2 on their own probe.
func TestAPythonStageThatFailsIsLeftOutWithTheOnesAboveIt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fail    func()
		cause   string
		present []string
		absent  []string
	}{
		{"the interpreter", func() { engine.failLeaf(`"uv","python","install"`, "sync", "download failed") },
			"the python interpreter", nil, []string{`path:"/usr/local/bin/uv"`, `path:"/opt/uv-python"`, `path:"/opt/atoms-py"`, `path:"/opt/ansible-collections"`, `name:"PATH"`}},
		{"the packages", func() { engine.failLeaf(`"--require-hashes"`, "sync", "hash mismatch") },
			"the python packages", []string{`path:"/usr/local/bin/uv"`, `path:"/opt/uv-python"`}, []string{`path:"/opt/atoms-py"`, `path:"/opt/ansible-collections"`, `name:"PATH"`}},
		{"the collections, after the retry", func() { engine.failLeaf(`"collection","install"`, "sync", "404 Not Found") },
			"the ansible collections", []string{`path:"/usr/local/bin/uv"`, `path:"/opt/uv-python"`, `path:"/opt/atoms-py"`, `name:"PATH"`}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine.reset()
			log := capturedLog(t)
			tc.fail()
			if _, err := atomsTools(context.Background()).Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			c := engine.chain(`path:"/usr/local/bin/atoms"`)
			for _, want := range tc.present {
				if !strings.Contains(c, want) {
					t.Errorf("%s was dropped with the stage above it", want)
				}
			}
			for _, not := range tc.absent {
				if strings.Contains(c, not) {
					t.Errorf("%s was applied though its stage did not build", not)
				}
			}
			if !strings.Contains(c, `path:"/usr/local/bin/hadolint"`) {
				t.Error("a python stage took the tools with it")
			}
			if line := log(); !strings.Contains(line, "atoms tools: "+tc.cause+" left out of the container: ") {
				t.Errorf("the log lacks the cause for %s: %q", tc.cause, line)
			}
		})
	}
}
