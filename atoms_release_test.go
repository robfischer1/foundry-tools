package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

var (
	bunBase = "registry.notusmi.com/foundry/base-images/bun:stable@sha256:" + strings.Repeat("b", 64)
	pyBase  = "registry.notusmi.com/foundry/base-images/python:stable@sha256:" + strings.Repeat("c", 64)
	goBase  = "registry.notusmi.com/foundry/base-images/go:stable@sha256:" + strings.Repeat("d", 64)
)

// copiesHades is the three-line shape: a Dockerfile that asks for the Gate's
// artifact by copying it from release/ (buildlane.CopiesRelease).
const copiesHades = "FROM x\nCOPY release/hades /hades\n"

// A bun star on the release path: its Dockerfile is the base and a COPY, its
// package.json defines the release script, and the script leaves server.js
// under release/.
func bunStar() map[string]string {
	return map[string]string{
		"Dockerfile":             "FROM " + bunBase + "\nCOPY release/ /app/\nCMD [\"bun\", \"server.js\"]\n",
		".copier-answers.yml":    "service_name: calliope\n",
		"package.json":           `{"scripts":{"release":"bun x.ts release"}}`,
		"bun.lock":               "",
		"/src/release/server.js": "",
	}
}

// A python star on the release path: the base and a COPY of the venv.
func pythonStar() map[string]string {
	return map[string]string{
		"Dockerfile":          "FROM " + pyBase + "\nCOPY --chown=999:999 release/app /app\nCMD [\"iris\"]\n",
		".copier-answers.yml": "service_name: iris\n",
		"pyproject.toml":      "[project]\nname = \"iris\"\n",
		"uv.lock":             "",
		"/app/.venv/bin/iris": "",
	}
}

// RELEASE() FOLLOWS THE IMAGE'S BASE. A python star's release is its venv,
// handed over under app/.venv for `COPY release/app /app`; a bun star's is
// the release/ its steps left; a star on the go base still compiles.
func TestReleaseFollowsTheImagesBase(t *testing.T) {
	ctx := context.Background()

	engine.reset()
	engine.withTree(pythonStar())
	dir, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx, "")
	if err != nil || dir == nil {
		t.Fatalf("dir %v err %v", dir, err)
	}
	if _, err := dag.Directory().WithDirectory("release", dir).Entries(ctx); err != nil {
		t.Fatalf("the release directory could not be used: %v", err)
	}
	venv := engine.chain(`directory(path:"/app/.venv")`, "{id}")
	if venv == "" || !strings.Contains(venv, `"uv","sync","--locked"`) {
		t.Fatalf("the venv is not read back off the sync:\n%v", engine.chains())
	}
	if c := engine.chain(`withDirectory(`, `path:"app/.venv"`); c == "" || !strings.Contains(c, fakeID(venv)) {
		t.Errorf("the release directory does not carry the venv at app/.venv:\n%s\n%v", c, engine.chains())
	}
	if engine.chain(`"go","build"`) != "" || engine.chain(`"cargo","build"`) != "" {
		t.Errorf("a python star was compiled:\n%v", engine.chains())
	}

	// A bun star's release is its steps' release/.
	engine.reset()
	engine.withTree(bunStar())
	dir, err = (&FoundryTools{Source: dag.Directory()}).Release(ctx, "")
	if err != nil || dir == nil {
		t.Fatalf("dir %v err %v", dir, err)
	}
	if _, err := dag.Directory().WithDirectory("release", dir).Entries(ctx); err != nil {
		t.Fatalf("the release directory could not be used: %v", err)
	}
	if engine.chain(`"bun","run","release"`, `directory(path:"/src/release")`) == "" {
		t.Errorf("the bun release is not read off the script's release/:\n%v", engine.chains())
	}

	// A failed release is an error, never an empty directory.
	engine.reset()
	engine.withTree(pythonStar())
	engine.exitCode(`"uv","sync"`, 2)
	engine.stderr(`"uv","sync"`, "error: The lockfile needs to be updated")
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx, ""); err == nil || !strings.Contains(err.Error(), "the release build failed") {
		t.Errorf("err %v", err)
	}
	engine.reset()
	engine.withTree(bunStar())
	engine.exitCode(`"bun","run","release"`, 1)
	engine.stderr(`"bun","run","release"`, `error: Script not found "release"`)
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx, ""); err == nil || !strings.Contains(err.Error(), "Script not found") {
		t.Errorf("a bun star with no release script: err %v", err)
	}

	// A tree that cannot be read, and a release that could not run at all
	// (as against one that ran and failed), are errors that say which.
	engine.reset()
	engine.withTree(pythonStar())
	engine.fail("glob", "the tree went away")
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx, ""); err == nil || !strings.Contains(err.Error(), "the tree could not be read") {
		t.Errorf("err %v", err)
	}
	engine.reset()
	engine.withTree(bunStar())
	engine.exitCode(`"bun","install","--frozen-lockfile"`, 127)
	engine.stderr(`"bun","install","--frozen-lockfile"`, "bun: not found")
	if dir, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx, ""); err == nil || dir != nil || !strings.Contains(err.Error(), "the release build did not run") {
		t.Errorf("dir %v err %v", dir, err)
	}

	// A Go star on the fleet's go base is the compiled lane's, as before.
	engine.reset()
	engine.withTree(map[string]string{
		"go.mod": "module x\n", "cmd/mnemosyne/main.go": "package main\n",
		".copier-answers.yml": "service_name: mnemosyne\n",
		"Dockerfile":          "FROM " + goBase + "\nCOPY release/mnemosyne /mnemosyne\n",
	})
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx, ""); err != nil {
		t.Fatalf("err %v", err)
	}
	if engine.chain(`"go","build"`) == "" || engine.chain(`"uv","sync"`) != "" {
		t.Errorf("a go-based image was not compiled by the go lane:\n%v", engine.chains())
	}
}

// THE STAMP REACHES THE COMPILE (foundry-tools#18676): Release(version) links
// the version in with the module path go.mod names, and Release("") — the
// Gate's compile — links nothing, so its exec is the one the cache holds.
func TestReleaseStampsTheVersionOnlyWhenAsked(t *testing.T) {
	tree := map[string]string{
		"Dockerfile":          copiesHades,
		".copier-answers.yml": "service_name: hades\n",
		"go.mod":              "module git.notusmi.com/rob/hades\n",
		"cmd/hades/main.go":   "package main\n",
	}
	ctx := context.Background()

	engine.reset()
	engine.withTree(tree)
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx, "1792086104-b3db6f1"); err != nil {
		t.Fatal(err)
	}
	c := engine.chain(`"-o","/out/hades"`)
	for _, want := range []string{
		`-X main.version=1792086104-b3db6f1`,
		`-X git.notusmi.com/rob/hades/internal/config.Version=1792086104-b3db6f1`,
	} {
		if !strings.Contains(c, want) {
			t.Errorf("the compile lacks %q:\n%v", want, engine.chains())
		}
	}

	engine.reset()
	engine.withTree(tree)
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if c := engine.chain(`"-o","/out/hades"`); c == "" || strings.Contains(c, "-X ") {
		t.Errorf("the Gate's compile is stamped:\n%v", engine.chains())
	}

	// A version that would split the ldflags is refused before any compile.
	engine.reset()
	engine.withTree(tree)
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx, "1 -X main.evil=x"); err == nil {
		t.Error("an unsafe version was stamped")
	}
}

// THE BUILD LANE'S RELEASE() IS THE COMPILE IT WAS, command for command. The
// gate's atoms are files now, run by the adapter; Release() still builds the
// directory the image is made from, as a chain of its own, and these hold what
// that chain runs: the image, the mounts, the order and the argv.
func TestReleaseCompilesAGoStarWithTheFleetsFlags(t *testing.T) {
	ctx := context.Background()
	engine.reset()
	engine.withTree(map[string]string{"Dockerfile": copiesHades, ".copier-answers.yml": "service_name: hades\n", "go.mod": "module x\n"})
	engine.withTree(cmdMain("hades"))
	dir, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx, "")
	if err != nil || dir == nil {
		t.Fatalf("dir %v err %v", dir, err)
	}
	c := engine.chain(`"go","build","-trimpath"`, "exitCode")
	if !strings.Contains(c, checks.ImageGo) {
		t.Errorf("the compile runs in the go lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withExec", `args:["go","mod","download"]`},
		[]string{"withEnvVariable", `name:"CGO_ENABLED"`, `value:"0"`},
		[]string{"withExec", `expect:ANY`, `args:["go","build","-trimpath","-ldflags=-s -w","-o","/out/hades","./cmd/hades"]`},
	)
	if download, build := strings.Index(c, `"go","mod","download"`), strings.Index(c, `"go","build"`); !(download >= 0 && download < build) {
		t.Errorf("the module graph is downloaded before the compile:\n%s", c)
	}
	// the tree it compiles has no .git and none of the inert files
	if engine.chain(`filter(`, `".git"`) == "" {
		t.Errorf("the compile reads the tree without its history:\n%v", engine.chains())
	}

	// A vendored module is compiled with -mod=vendor and downloads nothing.
	engine.reset()
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/ourea /ourea\n", ".copier-answers.yml": "service_name: ourea\n", "go.mod": "module x\n", "vendor/modules.txt": "# x\n"})
	engine.withTree(cmdMain("ourea"))
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx, ""); err != nil {
		t.Fatal(err)
	}
	c = engine.chain(`"-o","/out/ourea"`, "exitCode")
	wantCalls(t, c, []string{"withExec", `args:["go","build","-mod=vendor","-trimpath","-ldflags=-s -w","-o","/out/ourea","./cmd/ourea"]`})
	if strings.Contains(c, `"go","mod","download"`) {
		t.Errorf("a vendored release build ran go mod download:\n%s", c)
	}

	// The Dockerfile copies more than one, and each gets its own exec.
	engine.reset()
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/blade-runner /blade-runner\nCOPY release/blade-controller /blade-controller\n", ".copier-answers.yml": "service_name: blade-runner\n", "go.mod": "module x\n"})
	engine.withTree(cmdMain("blade-runner", "blade-controller"))
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx, ""); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"blade-controller", "blade-runner"} {
		if engine.chain(`"-o","/out/`+b+`"`) == "" {
			t.Errorf("no exec builds %s:\n%v", b, engine.chains())
		}
	}
}

// A RUST STAR'S RELEASE IS ITS RELEASE PROFILE, --locked, into the repository's
// release volume and copied out of it by the same exec: the lane provisioned,
// the tree dated, the target named, then the build under copyout.
func TestReleaseCompilesARustStarIntoTheReleaseVolume(t *testing.T) {
	ctx := context.Background()
	engine.reset()
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/tron /tron\n", ".copier-answers.yml": "service_name: tron\n", "Cargo.toml": "[package]\n"})
	dir, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx, "")
	if err != nil || dir == nil {
		t.Fatalf("dir %v err %v", dir, err)
	}
	if _, err := dag.Directory().WithDirectory("release", dir).Entries(ctx); err != nil {
		t.Fatalf("the release directory could not be used: %v", err)
	}
	c := engine.chain(`"cargo","build","--release"`, "exitCode")
	wantCalls(t, c,
		[]string{"withExec", `args:["cargo","fetch","--locked"]`},
		[]string{"withMountedCache", `path:"/cache/cargo-release"`, `sharing:SHARED`},
		[]string{"withFile", `path:"/usr/local/bin/copyout"`},
		[]string{"withEnvVariable", `name:"CARGO_TARGET_DIR"`, `value:"/cache/cargo-release"`},
		[]string{"withExec", `expect:ANY`, `args:["/usr/local/bin/copyout","/cache/cargo-release/release/tron=/out/tron","--","cargo","build","--release","--locked","-p","tron"]`},
	)
	// The dependencies are provisioned before the compile, the tree is stamped so
	// the volume's artifacts of an older tree are rebuilt, and the target
	// directory is the release volume - set AFTER the lane's cache mounts named
	// the debug volume's, so the override is the one cargo sees.
	fetch, stamp := strings.Index(c, `"cargo","fetch","--locked"`), strings.Index(c, `"touch","-c","-d"`)
	target, build := strings.LastIndex(c, `name:"CARGO_TARGET_DIR"`), strings.Index(c, `"cargo","build","--release"`)
	if !(fetch < stamp && stamp < target && target < build) {
		t.Errorf("fetch, stamp, the target override, then the build - got %d %d %d %d:\n%s", fetch, stamp, target, build, c)
	}
	// It is bounded but keeps its profile: the release binary is the shipped
	// artifact, its debuginfo and linker the star's own.
	wantBounded(t, "Release()", c, `"cargo","build","--release"`)
	for _, n := range []string{leverDevDb, leverTstDb, leverFlags, `"mold","-run"`} {
		if strings.Contains(c, n) {
			t.Errorf("the release build must leave %s alone:\n%s", n, c)
		}
	}

	// A crate that does not compile is the build lane's error, in the compiler's words.
	engine.reset()
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/tron /tron\n", ".copier-answers.yml": "service_name: tron\n", "Cargo.toml": "[package]\n"})
	engine.exitCode(`"-p","tron"`, 101)
	engine.stderr(`"-p","tron"`, "error[E0425]: cannot find value `x` in this scope")
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx, ""); err == nil || !strings.Contains(err.Error(), "the release build failed") || !strings.Contains(err.Error(), "E0425") {
		t.Errorf("err %v", err)
	}
	engine.reset()
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/tron /tron\n", ".copier-answers.yml": "service_name: tron\n", "Cargo.toml": "[package]\n"})
	engine.exitCode(`"-p","tron"`, 137)
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx, ""); err == nil || !strings.Contains(err.Error(), "the release build did not run") {
		t.Errorf("err %v", err)
	}
}

// RELEASE() NAMES A STAR THE WAY THE ATOMS DO: by its answers file, else by its
// record under the clone the run names, and says why when it cannot - an error,
// never an empty directory.
func TestReleaseNamesItsStarAsTheAtomsDo(t *testing.T) {
	ctx := context.Background()
	noAnswers := map[string]string{"Dockerfile": copiesHades, "go.mod": "module x\n", "cmd/hephaestus/main.go": "package main\n", "/dies/fleet/repos/hephaestus.json": `{"repo":"rob/hephaestus"}`}
	engine.reset()
	engine.withTree(map[string]string{"Dockerfile": copiesHades, "go.mod": "module x\n", "/dies/fleet/repos/hephaestus.json": `{"repo":"rob/hephaestus"}`})
	engine.withTree(cmdMain("hephaestus"))
	if _, err := (&FoundryTools{Source: dag.Directory(), Repo: "http://ourea.default.svc.cluster.local:8215/hephaestus.git"}).Release(ctx, ""); err == nil || !strings.Contains(err.Error(), "copies release/hades") {
		// the record names the star hephaestus; the Dockerfile copies hades, which has no cmd/
		t.Errorf("err %v", err)
	}
	engine.reset()
	engine.withTree(noAnswers)
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx, ""); err == nil || !strings.Contains(err.Error(), "no clone URL to find a record by") {
		t.Errorf("a repository naming no star: err %v", err)
	}
	engine.reset()
	engine.withTree(noAnswers)
	engine.fail(`glob(pattern:"fleet/repos/*.json")`, "the dies went away")
	if _, err := (&FoundryTools{Source: dag.Directory(), Repo: "http://ourea.default.svc.cluster.local:8215/hephaestus.git"}).Release(ctx, ""); err == nil || !strings.Contains(err.Error(), "the fleet's records could not be listed") {
		t.Errorf("err %v", err)
	}
}
