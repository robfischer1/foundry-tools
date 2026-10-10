package main

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/devlane"
)

// THE DEV SURFACE against the paper engine. What these hold is the module's own
// decisions: which lane image a verb runs in, which argv, which cache volume,
// and that a tool's non-zero exit reaches the verdict exec — so `dagger call`
// exits non-zero. Whether cargo or go does the right thing is the engine's.

var (
	devGoTree = map[string]string{
		"go.mod":  "module example.com/dev\n\ngo 1.26\n",
		"main.go": "package main\n",
	}
	devRustTree = map[string]string{
		"Cargo.toml": "[package]\nname = \"devcrate\"\n",
		"src/lib.rs": "",
	}
)

func devOver(t *testing.T, tree map[string]string, lang, repo string) *Dev {
	t.Helper()
	engine.reset()
	engine.withTree(tree)
	d, err := (&FoundryTools{}).Dev(context.Background(), dag.Directory(), lang, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// settledWith asserts the call ended on the verdict exec carrying the reason as
// a FILE (so the engine does not echo it into the exec's title a second time),
// and answers the file's text. The code is the verdict exec's first argument.
func settledWith(t *testing.T, code string) string {
	t.Helper()
	chain := engine.chain(`"/usr/local/bin/verdict"`)
	if chain == "" {
		t.Fatal("the call never settled: no verdict exec reached the engine")
	}
	wantCalls(t, chain, []string{"withExec", `args:["/usr/local/bin/verdict","` + code + `","@/reason"]`})
	m := regexp.MustCompile(`withNewFile\(([^)]*path:"/reason"[^)]*)\)`).FindStringSubmatch(chain)
	if m == nil {
		t.Fatalf("the reason was not written to /reason:\n%s", chain)
	}
	body := regexp.MustCompile(`contents:"((?:[^"\\]|\\.)*)"`).FindStringSubmatch(m[1])
	if body == nil {
		t.Fatalf("no contents in %s", m[1])
	}
	var out string
	if err := json.Unmarshal([]byte(`"`+body[1]+`"`), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(chain, `"verdict","`+code+`","`) {
		t.Error("the reason must not ride the argument list")
	}
	return out
}

// A failing tool reaches the verdict exec with its output, and a passing one
// answers its output with no verdict exec at all.
func TestDevTestEndsOnTheToolsOwnExit(t *testing.T) {
	d := devOver(t, devGoTree, "", "")
	engine.stdout(`"go","test"`, "ok  \texample.com/dev\t0.1s\n")
	out, err := d.Test(t.Context(), nil, true, false)
	if err != nil || !strings.Contains(out, "ok  \texample.com/dev") {
		t.Fatalf("a passing suite answers its output: %q, %v", out, err)
	}
	if engine.chain(`"/usr/local/bin/verdict"`) != "" {
		t.Error("a passing suite must not run the verdict exec")
	}

	engine.reset()
	engine.withTree(devGoTree)
	engine.exitCode(`"go","test"`, 1)
	engine.stdout(`"go","test"`, "--- FAIL: TestX\n")
	engine.stderr(`"go","test"`, "FAIL\n")
	// The real engine fails the verdict exec with the tool's code; say so.
	engine.fail(`"/usr/local/bin/verdict"`, "exit code: 1")
	if _, err := d.Test(t.Context(), nil, true, false); err == nil || !strings.Contains(err.Error(), "exit code: 1") {
		t.Fatalf("a failing suite must fail the call: %v", err)
	}
	// stdout first, then stderr, once each.
	if got := settledWith(t, "1"); got != "--- FAIL: TestX\nFAIL\n" {
		t.Errorf("reason %q", got)
	}

	// A kill or a missing binary is not a finding.
	engine.reset()
	engine.withTree(devGoTree)
	engine.exitCode(`"go","test"`, 127)
	engine.fail(`"/usr/local/bin/verdict"`, "exit code: 2")
	if _, err := d.Test(t.Context(), nil, true, false); err == nil {
		t.Error("a missing binary must fail the call")
	}
	settledWith(t, "2")
}

func TestDevGoTestRunsRacedInTheGoLaneWithGitAndTheDies(t *testing.T) {
	d := devOver(t, devGoTree, "", "")
	if d.Lang != devlane.Go || d.Repo != "example.com/dev" {
		t.Fatalf("lang %q repo %q", d.Lang, d.Repo)
	}
	if _, err := d.Test(t.Context(), []string{"-run", "TestX", "./internal/foo/..."}, true, false); err != nil {
		t.Fatal(err)
	}
	c := engine.chain(`"go","test"`, "exitCode")
	if !strings.Contains(c, checks.ImageGo) {
		t.Errorf("go test must run in the go lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withMountedCache", `path:"/go/pkg/mod"`},
		[]string{"withMountedCache", `path:"/opt/go-build-cache"`},
		[]string{"withNewFile", `path:"/etc/gitconfig"`},
		[]string{"withExec", `args:["git","init","-q","."]`},
		[]string{"withExec", `args:["go","mod","download"]`},
		[]string{"withExec", `expect:ANY`, `args:["go","test","-race","-run","TestX","./internal/foo/..."]`},
	)
	if !strings.Contains(c, "FOUNDRY_DIES") {
		t.Errorf("the fleet's record tree rides along, as it does in the gate:\n%s", c)
	}
	if strings.Contains(c, "CA_REASK") {
		t.Errorf("an unforced run must not be keyed afresh:\n%s", c)
	}

	// --race=false, and the caller's own spelling.
	for _, args := range [][]string{nil, {"-race=false"}} {
		engine.reset()
		engine.withTree(devGoTree)
		if _, err := d.Test(t.Context(), args, false, false); err != nil {
			t.Fatal(err)
		}
		if got := engine.chain(`"go","test"`, "exitCode"); strings.Contains(got, `"go","test","-race",`) {
			t.Errorf("args %v with race off ran raced:\n%s", args, got)
		}
	}
}

func TestDevFreshKeysTheRunAfresh(t *testing.T) {
	d := devOver(t, devGoTree, "", "")
	if _, err := d.Build(t.Context(), nil, true); err != nil {
		t.Fatal(err)
	}
	c := engine.chain(`"go","build","./..."`, "exitCode")
	if !hasCall(c, "withEnvVariable", `name:"CA_REASK"`) {
		t.Errorf("--fresh must write a nonce into the lane:\n%s", c)
	}
	// Mounted after the toolchain layers, so only the verb's own execs re-run.
	if at := lastCall(c, "withEnvVariable", `name:"CA_REASK"`); at < lastCall(c, "withMountedCache", `path:"/go/pkg/mod"`) {
		t.Error("the nonce must come after the cache mounts")
	}
}

func TestDevBuildAndVetDefaultToEveryPackage(t *testing.T) {
	d := devOver(t, devGoTree, "", "")
	if _, err := d.Build(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, engine.chain(`"go","build"`, "exitCode"), []string{"withExec", `expect:ANY`, `args:["go","build","./..."]`})
	if _, err := d.Vet(t.Context(), []string{"./cmd/..."}, false); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, engine.chain(`"go","vet"`, "exitCode"), []string{"withExec", `expect:ANY`, `args:["go","vet","./cmd/..."]`})
}

func TestDevRustVerbsRunTheGatesCargoLane(t *testing.T) {
	d := devOver(t, devRustTree, "", "")
	if d.Lang != devlane.Rust || d.Repo != "devcrate" {
		t.Fatalf("lang %q repo %q", d.Lang, d.Repo)
	}
	for _, c := range []struct {
		run  func() (string, error)
		argv string
	}{
		{func() (string, error) { return d.Check(t.Context(), nil, false) }, `["cargo","check","--workspace"]`},
		{func() (string, error) { return d.Build(t.Context(), nil, false) }, `["cargo","build","--workspace"]`},
		{func() (string, error) { return d.Clippy(t.Context(), nil, false) }, `["cargo","clippy","--workspace","--all-targets","--","-W","clippy::all","-D","warnings"]`},
		{func() (string, error) { return d.Test(t.Context(), []string{"-p", "x", "name"}, true, false) }, `["cargo","test","-p","x","name"]`},
	} {
		engine.reset()
		engine.withTree(devRustTree)
		if _, err := c.run(); err != nil {
			t.Fatal(err)
		}
		chain := engine.chain(c.argv, "exitCode")
		if chain == "" {
			t.Fatalf("no exec of %s:\n%v", c.argv, engine.chains())
		}
		if !strings.Contains(chain, checks.ImageRust) {
			t.Errorf("%s must run in the rust lane image", c.argv)
		}
		// cargoFresh: the locked fetch, then the tree dated past every artifact.
		wantCalls(t, chain,
			[]string{"withExec", `args:["cargo","fetch","--locked"]`},
			[]string{"withExec", `"touch"`},
			[]string{"withExec", `expect:ANY`, `args:` + c.argv},
		)
	}
	// Only the test verb gets a repository to ask git about.
	engine.reset()
	engine.withTree(devRustTree)
	_, _ = d.Check(t.Context(), nil, false)
	if strings.Contains(engine.chain(`"cargo","check"`, "exitCode"), `"git","init"`) {
		t.Error("check has no use for a throwaway repository")
	}
	engine.reset()
	engine.withTree(devRustTree)
	_, _ = d.Test(t.Context(), nil, true, false)
	wantCalls(t, engine.chain(`"cargo","test"`, "exitCode"), []string{"withExec", `args:["git","init","-q","."]`})
}

// THE CARGO TARGET IS THE REPOSITORY'S AND THE TREE'S, AND LOCKED. Two trees of
// one crate name share /src-relative artifact paths, so a shared mount let tree
// A's test run tree B's binary (reproduced, exit 0). The key composes repo and
// tree; LOCKED holds the volume across the test binaries; and the gate's own
// mounts are left as they were.
func TestDevCargoTargetIsKeyedOnRepoAndTreeAndLocked(t *testing.T) {
	for _, c := range []struct{ repo, tree, wantRepo string }{
		{"", "/w/one", "devcrate"},
		{"", "/w/two", "devcrate"},
		{"cerberus", "", "cerberus"},
		{"cerberus", "/w/one", "cerberus"},
	} {
		engine.reset()
		engine.withTree(devRustTree)
		d, err := (&FoundryTools{}).Dev(t.Context(), dag.Directory(), "", c.repo, c.tree)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Check(t.Context(), nil, false); err != nil {
			t.Fatal(err)
		}
		key := checks.CachesForDev(checks.ImageRust, c.wantRepo, c.tree)[2].Key
		if engine.chain(`cacheVolume(`, `key:"`+key+`"`) == "" {
			t.Fatalf("repo %q tree %q: no volume keyed %s:\n%v", c.repo, c.tree, key, engine.chains())
		}
		chain := engine.chain(`"cargo","check"`, "exitCode")
		wantCalls(t, chain, []string{"withMountedCache", `path:"/cache/cargo-target"`, `sharing:LOCKED`})
		// The registry volumes are the gate's: shared, content-addressed.
		for _, p := range []string{"/usr/local/cargo/registry", "/usr/local/cargo/git"} {
			if hasCall(chain, "withMountedCache", `path:"`+p+`"`, `sharing:LOCKED`) {
				t.Errorf("%s must stay shared", p)
			}
		}
	}
	// Different trees, different volumes; the same tree, the same one; and never
	// the fleet-shared or the gate's per-repo key.
	one := checks.CachesForDev(checks.ImageRust, "r", "/w/one")[2].Key
	two := checks.CachesForDev(checks.ImageRust, "r", "/w/two")[2].Key
	if one == two || one == checks.CachesForDev(checks.ImageRust, "r", "")[2].Key {
		t.Errorf("trees must key apart: %s %s", one, two)
	}
	if one == checks.CachesForRepo(checks.ImageRust, "r")[2].Key || one == "foundry-cargo-target" {
		t.Errorf("dev must not reuse the gate's or the fleet's target: %s", one)
	}
}

// The gate's mounts are not locked.
func TestTheGatesCargoTargetIsStillShared(t *testing.T) {
	engine.reset()
	if _, err := newRun(dag.Directory(), "http://door/x.git", "").cargoDeps().Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	chain := engine.chain(`"cargo","fetch"`)
	if chain == "" || hasCall(chain, "withMountedCache", `path:"/cache/cargo-target"`, `sharing:LOCKED`) {
		t.Errorf("the gate's target must not be LOCKED:\n%s", chain)
	}
}

// With no identity there is no safe key: refuse, do not share the fleet's.
func TestDevRefusesASourceItCannotIdentify(t *testing.T) {
	for _, tree := range []map[string]string{
		{"Cargo.toml": "[workspace]\n"},
		{"go.mod": "go 1.26\n"},
	} {
		engine.reset()
		engine.withTree(tree)
		if _, err := (&FoundryTools{}).Dev(t.Context(), dag.Directory(), "", "", ""); err == nil || !strings.Contains(err.Error(), "--repo") {
			t.Errorf("%v: %v", tree, err)
		}
		if _, err := (&FoundryTools{}).Dev(t.Context(), dag.Directory(), "", "named", ""); err != nil {
			t.Errorf("--repo names it: %v", err)
		}
	}
}

func TestDevRefusesAVerbTheLanguageLacks(t *testing.T) {
	for _, c := range []struct {
		tree map[string]string
		run  func(*Dev) error
	}{
		{devGoTree, func(d *Dev) error { _, err := d.Clippy(t.Context(), nil, false); return err }},
		{devGoTree, func(d *Dev) error { _, err := d.Check(t.Context(), nil, false); return err }},
		{devGoTree, func(d *Dev) error { _, err := d.Lock(t.Context(), nil); return err }},
		{devRustTree, func(d *Dev) error { _, err := d.Vet(t.Context(), nil, false); return err }},
		{devRustTree, func(d *Dev) error { _, err := d.Tidy(t.Context()); return err }},
	} {
		d := devOver(t, c.tree, "", "")
		err := c.run(d)
		if err == nil || !strings.Contains(err.Error(), "does not exist for a "+d.Lang+" source") {
			t.Errorf("%s: err = %v", d.Lang, err)
		}
		if engine.chain("withExec") != "" {
			t.Errorf("%s: a refused verb must run nothing", d.Lang)
		}
	}
}

func TestDevConstructorReadsTheTree(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"README.md": ""})
	if _, err := (&FoundryTools{}).Dev(t.Context(), dag.Directory(), "", "", ""); err == nil || !strings.Contains(err.Error(), "no go.mod or Cargo.toml") {
		t.Errorf("an empty tree: %v", err)
	}
	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module a\n", "Cargo.toml": "[package]\nname=\"b\"\n"})
	if _, err := (&FoundryTools{}).Dev(t.Context(), dag.Directory(), "", "", ""); err == nil || !strings.Contains(err.Error(), "--lang") {
		t.Errorf("an ambiguous tree: %v", err)
	}
	d, err := (&FoundryTools{}).Dev(t.Context(), dag.Directory(), "rust", "", "")
	if err != nil || d.Lang != devlane.Rust || d.Repo != "b" {
		t.Errorf("--lang=rust picks the rust identity: %+v, %v", d, err)
	}
	// A virtual workspace is named by its lock's local crates.
	engine.reset()
	engine.withTree(map[string]string{
		"Cargo.toml": "[workspace]\nmembers = [\"crates/*\"]\n",
		"Cargo.lock": "[[package]]\nname = \"one\"\n[[package]]\nname = \"two\"\n",
	})
	if d, err = (&FoundryTools{}).Dev(t.Context(), dag.Directory(), "", "", ""); err != nil || d.Repo != "workspace:one,two" {
		t.Errorf("virtual workspace: %+v, %v", d, err)
	}
	// An explicit --repo reads no manifest.
	engine.reset()
	engine.withTree(devGoTree)
	if d, err = (&FoundryTools{}).Dev(t.Context(), dag.Directory(), "", "mine", ""); err != nil || d.Repo != "mine" {
		t.Errorf("--repo: %+v, %v", d, err)
	}
	if engine.chain(`path:"go.mod"`, "contents") != "" {
		t.Error("--repo given, nothing to read from go.mod")
	}
}

func TestDevFmtRewritesInPlaceAndAnswersOnlyTheDifference(t *testing.T) {
	tree := map[string]string{"go.mod": "module m\n", "a.go": "package a\n", "x/b.go": "package x\n", "notes.txt": "n"}
	d := devOver(t, tree, "", "")
	dir, err := d.Fmt(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dir.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	c := engine.chain(`"gofmt","-w"`, "exitCode")
	wantCalls(t, c,
		[]string{"withNewFile", `path:"/tmp/files0"`, `a.go\u0000x/b.go`},
		[]string{"withExec", `expect:ANY`, `args:["xargs","-0","-a","/tmp/files0","gofmt","-w"]`},
	)
	if strings.Contains(c, "notes.txt") {
		t.Errorf("only Go files go to gofmt:\n%s", c)
	}
	// The answer is the difference, taken between the source and the tree after.
	after := engine.chain(`"gofmt","-w"`, `directory(path:"/src")`)
	if after == "" {
		t.Fatalf("fmt never read the tree back out of the container:\n%v", engine.chains())
	}
	if diff := engine.chain(`directory{diff(other:"` + fakeID(after) + `")`); diff == "" {
		t.Errorf("fmt must answer the source's diff against the tree after gofmt, not the whole tree:\n%v", engine.chains())
	}

	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module m\n"})
	if _, err := d.Fmt(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "no Go files") {
		t.Errorf("nothing to format is said, not run: %v", err)
	}

	// gofmt failing is the call failing.
	engine.reset()
	engine.withTree(tree)
	engine.exitCode(`"gofmt"`, 123)
	engine.stderr(`"gofmt"`, "a.go:1:1: expected 'package'\n")
	engine.fail(`"/usr/local/bin/verdict"`, "exit code: 1")
	if _, err := d.Fmt(t.Context(), nil); err == nil {
		t.Error("gofmt failing is the call failing")
	}
	if got := settledWith(t, "1"); !strings.Contains(got, "expected 'package'") {
		t.Errorf("reason %q", got)
	}
}

func TestDevTidyRunsOnlyGoModTidyAndLeavesVendorAlone(t *testing.T) {
	vendored := map[string]string{"go.mod": "module m\n", "vendor/modules.txt": "# x\n"}
	for _, tree := range []map[string]string{devGoTree, vendored} {
		d := devOver(t, tree, "", "")
		if _, err := d.Tidy(t.Context()); err != nil {
			t.Fatal(err)
		}
		if engine.chain(`"go","mod","tidy"`, "exitCode") == "" {
			t.Fatal("go mod tidy never ran")
		}
		if engine.chain(`"go","mod","vendor"`) != "" {
			t.Error("tidy must not re-vendor: vendor/ is exported by its own verb")
		}
	}

	d := devOver(t, vendored, "", "")
	engine.exitCode(`"go","mod","tidy"`, 1)
	engine.stderr(`"go","mod","tidy"`, "go: example.com/x: reading go.sum\n")
	engine.fail(`"/usr/local/bin/verdict"`, "exit code: 1")
	if _, err := d.Tidy(t.Context()); err == nil {
		t.Error("a failed tidy fails the call")
	}
	if got := settledWith(t, "1"); !strings.Contains(got, "reading go.sum") {
		t.Errorf("reason %q", got)
	}
}

// vendor answers the whole vendor directory, or an empty one when the tool
// removed it, so that `export --wipe` leaves vendor/ matching the tool.
func TestDevVendorAnswersTheVendorDirectory(t *testing.T) {
	vendored := map[string]string{"go.mod": "module m\n", "vendor/modules.txt": "# x\n"}
	d := devOver(t, vendored, "", "")
	engine.withTree(map[string]string{"/src/vendor/modules.txt": ""})
	dir, err := d.Vendor(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dir.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	c := engine.chain(`"go","mod","vendor"`, "exitCode")
	wantCalls(t, c,
		[]string{"withEnvVariable", `name:"GOFLAGS"`, `value:"-mod=mod"`},
		[]string{"withExec", `expect:ANY`, `args:["go","mod","vendor"]`},
	)
	if !strings.Contains(c, checks.ImageGo) {
		t.Error("go mod vendor runs in the go lane image")
	}
	if engine.chain(`"go","mod","vendor"`, `directory(path:"/src/vendor")`) == "" {
		t.Errorf("the vendor directory itself is the answer:\n%v", engine.chains())
	}

	// The tool removed vendor/ (no dependencies left): the answer is empty.
	d = devOver(t, vendored, "", "")
	dir, err = d.Vendor(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dir.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if engine.chain(`directory(path:"/src/vendor")`) != "" {
		t.Error("no vendor/ after the tool: the answer must not name it")
	}

	// Not a vendored tree, and not a Go tree.
	d = devOver(t, devGoTree, "", "")
	if _, err := d.Vendor(t.Context()); err == nil || !strings.Contains(err.Error(), "no vendor/") {
		t.Errorf("an unvendored tree: %v", err)
	}
	d = devOver(t, devRustTree, "", "")
	if _, err := d.Vendor(t.Context()); err == nil || !strings.Contains(err.Error(), "does not exist for a rust source") {
		t.Errorf("a rust tree: %v", err)
	}

	// A failed go mod vendor fails the call and answers no directory.
	d = devOver(t, vendored, "", "")
	engine.exitCode(`"go","mod","vendor"`, 1)
	engine.stderr(`"go","mod","vendor"`, "go: cannot find module\n")
	engine.fail(`"/usr/local/bin/verdict"`, "exit code: 1")
	if _, err := d.Vendor(t.Context()); err == nil {
		t.Error("a failed vendor fails the call")
	}
	settledWith(t, "1")

	// The engine cannot read the tree back.
	d = devOver(t, vendored, "", "")
	engine.failLeaf(`"go","mod","vendor"`, "entries", "engine gone")
	if _, err := d.Vendor(t.Context()); err == nil || !strings.Contains(err.Error(), "could not read the tree back") {
		t.Errorf("unreadable tree: %v", err)
	}
}

func TestDevRustRewritersTakeNoLockedFetch(t *testing.T) {
	d := devOver(t, devRustTree, "", "")
	for _, c := range []struct {
		run  func() error
		argv string
	}{
		{func() error { _, err := d.Fmt(t.Context(), nil); return err }, `["cargo","fmt","--all"]`},
		{func() error { _, err := d.Lock(t.Context(), nil); return err }, `["cargo","generate-lockfile"]`},
		{func() error { _, err := d.Update(t.Context(), []string{"-p", "serde"}); return err }, `["cargo","update","-p","serde"]`},
	} {
		engine.reset()
		engine.withTree(devRustTree)
		if err := c.run(); err != nil {
			t.Fatal(err)
		}
		chain := engine.chain(c.argv, "exitCode")
		if chain == "" {
			t.Fatalf("no exec of %s", c.argv)
		}
		// --locked would refuse the very lock these exist to repair.
		if strings.Contains(chain, `"fetch","--locked"`) {
			t.Errorf("%s must not fetch --locked first:\n%s", c.argv, chain)
		}
		if !strings.Contains(chain, checks.ImageRust) {
			t.Errorf("%s must run in the rust lane image", c.argv)
		}
	}
	// An engine that cannot run the tool is an error, not a clean answer.
	engine.reset()
	engine.withTree(devRustTree)
	engine.fail(`"cargo","fmt"`, "failed to pull rust: 404")
	if _, err := d.Fmt(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "fmt could not run") {
		t.Errorf("an engine failure is an error: %v", err)
	}
}

// ---- the upload ignore ----

// annotationList reads the JSON list off a `+ignore=[...]` comment line.
func annotationList(t *testing.T, file string) []string {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^\s*//\s*\+ignore=(\[.*\])\s*$`).FindSubmatch(src)
	if m == nil {
		t.Fatalf("%s has no +ignore annotation", file)
	}
	var out []string
	if err := json.Unmarshal(m[1], &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Dev uploads exactly what `just check` uploads (New's ignore) less .git, and
// devlane.Ignore says so: the three lists cannot drift apart.
func TestDevUploadsWhatJustCheckUploadsLessGit(t *testing.T) {
	gate, dev := annotationList(t, "main.go"), annotationList(t, "dev.go")
	if !reflect.DeepEqual(dev, devlane.Ignore) {
		t.Errorf("the annotation on Dev and devlane.Ignore disagree:\n%v\n%v", dev, devlane.Ignore)
	}
	want := append(slices.Clone(gate), ".git")
	if !reflect.DeepEqual(dev, want) {
		t.Errorf("Dev must ignore New's list plus .git:\n got %v\nwant %v", dev, want)
	}
	if slices.Contains(gate, ".git") {
		t.Error("the gate keeps .git (fleet:witness reads history); Dev is the one that drops it")
	}
}

// The engine failing to read the tree is said, not swallowed - at the root, and
// when fmt enumerates its files.
func TestDevSaysWhenTheEngineCannotReadTheTree(t *testing.T) {
	engine.reset()
	engine.fail(`entries`, "engine gone")
	if _, err := (&FoundryTools{}).Dev(t.Context(), dag.Directory(), "", "", ""); err == nil || !strings.Contains(err.Error(), "could not read the source root") {
		t.Errorf("root unreadable: %v", err)
	}

	d := devOver(t, map[string]string{"go.mod": "module m\n", "a.go": "package a\n"}, "", "")
	engine.fail(`glob(pattern:"**/*.go")`, "engine gone")
	if _, err := d.Fmt(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "could not enumerate") {
		t.Errorf("population unreadable: %v", err)
	}
}

// plan refuses a verb the argv builders do not know, even if Applies were
// bypassed: nothing reaches an exec with no command.
func TestDevPlanRefusesAnUnknownVerb(t *testing.T) {
	for _, tree := range []map[string]string{devGoTree, devRustTree} {
		d := devOver(t, tree, "", "")
		if _, _, err := d.plan(t.Context(), newRun(d.Source, d.Repo, ""), "nonesuch", nil, true); err == nil {
			t.Errorf("%s: an unknown verb planned", d.Lang)
		}
	}
}

// A tool that ran but whose output cannot be read is an error naming which half,
// and a vendor whose tool never ran is the same.
func TestDevSaysWhenTheOutputOrTheRunIsLost(t *testing.T) {
	for _, leaf := range []string{"stdout", "stderr"} {
		d := devOver(t, devGoTree, "", "")
		engine.failLeaf(`"go","build"`, leaf, "engine gone")
		out, err := d.Build(t.Context(), nil, false)
		if err == nil || !strings.Contains(err.Error(), "its output could not be read") || out != "" {
			t.Errorf("%s: %q, %v", leaf, out, err)
		}
	}
	d := devOver(t, map[string]string{"go.mod": "module m\n", "vendor/modules.txt": "# x\n"}, "", "")
	engine.failLeaf(`"go","mod","vendor"`, "exitCode", "failed to pull golang: 404")
	if _, err := d.Vendor(t.Context()); err == nil || !strings.Contains(err.Error(), "vendor could not run") {
		t.Errorf("vendor that never ran: %v", err)
	}
}
