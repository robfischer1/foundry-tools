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
	d, err := (&FoundryTools{}).Dev(context.Background(), dag.Directory(), lang, repo)
	if err != nil {
		t.Fatal(err)
	}
	return d
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
	// The paper engine answers the verdict exec with success; the exec is the proof.
	_, _ = d.Test(t.Context(), nil, true, false)
	settledOn(t, "1", "--- FAIL: TestX")
	settledOn(t, "1", "FAIL")

	// A kill or a missing binary is not a finding.
	engine.reset()
	engine.withTree(devGoTree)
	engine.exitCode(`"go","test"`, 127)
	_, _ = d.Test(t.Context(), nil, true, false)
	settledOn(t, "2", "")
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

// The cargo target is the repository's: the volume is keyed on the identity, so
// every worktree of the repo lands on it; --repo overrides.
func TestDevCargoTargetIsKeyedOnTheRepoNotTheWorktree(t *testing.T) {
	for _, c := range []struct{ repo, want string }{{"", "devcrate"}, {"cerberus", "cerberus"}} {
		d := devOver(t, devRustTree, "", c.repo)
		if _, err := d.Check(t.Context(), nil, false); err != nil {
			t.Fatal(err)
		}
		key := checks.CachesForRepo(checks.ImageRust, c.want)[2].Key
		if engine.chain(`cacheVolume(`, `key:"`+key+`"`) == "" {
			t.Errorf("repo %q: no volume keyed %s:\n%v", c.repo, key, engine.chains())
		}
	}
	// And the registry volumes are the gate's, shared.
	d := devOver(t, devRustTree, "", "")
	_, _ = d.Check(t.Context(), nil, false)
	for _, k := range []string{"foundry-cargo-registry", "foundry-cargo-git"} {
		if engine.chain(`cacheVolume(`, `key:"`+k+`"`) == "" {
			t.Errorf("no shared volume %s", k)
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
	if _, err := (&FoundryTools{}).Dev(t.Context(), dag.Directory(), "", ""); err == nil || !strings.Contains(err.Error(), "no go.mod or Cargo.toml") {
		t.Errorf("an empty tree: %v", err)
	}
	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module a\n", "Cargo.toml": "[package]\nname=\"b\"\n"})
	if _, err := (&FoundryTools{}).Dev(t.Context(), dag.Directory(), "", ""); err == nil || !strings.Contains(err.Error(), "--lang") {
		t.Errorf("an ambiguous tree: %v", err)
	}
	d, err := (&FoundryTools{}).Dev(t.Context(), dag.Directory(), "rust", "")
	if err != nil || d.Lang != devlane.Rust || d.Repo != "b" {
		t.Errorf("--lang=rust picks the rust identity: %+v, %v", d, err)
	}
	// A virtual workspace is named by its lock's local crates.
	engine.reset()
	engine.withTree(map[string]string{
		"Cargo.toml": "[workspace]\nmembers = [\"crates/*\"]\n",
		"Cargo.lock": "[[package]]\nname = \"one\"\n[[package]]\nname = \"two\"\n",
	})
	if d, err = (&FoundryTools{}).Dev(t.Context(), dag.Directory(), "", ""); err != nil || d.Repo != "workspace:one,two" {
		t.Errorf("virtual workspace: %+v, %v", d, err)
	}
	// An explicit --repo reads no manifest.
	engine.reset()
	engine.withTree(devGoTree)
	if d, err = (&FoundryTools{}).Dev(t.Context(), dag.Directory(), "", "mine"); err != nil || d.Repo != "mine" {
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
	_, _ = d.Fmt(t.Context(), nil)
	settledOn(t, "1", "expected 'package'")
}

func TestDevTidyVendorsOnlyWhereThereIsAVendorDirectory(t *testing.T) {
	d := devOver(t, devGoTree, "", "")
	if _, err := d.Tidy(t.Context()); err != nil {
		t.Fatal(err)
	}
	if engine.chain(`"go","mod","tidy"`, "exitCode") == "" {
		t.Fatal("go mod tidy never ran")
	}
	if engine.chain(`"go","mod","vendor"`) != "" {
		t.Error("no vendor/, no go mod vendor")
	}

	vendored := map[string]string{"go.mod": "module m\n", "vendor/modules.txt": "# x\n"}
	d = devOver(t, vendored, "", "")
	if _, err := d.Tidy(t.Context()); err != nil {
		t.Fatal(err)
	}
	c := engine.chain(`"go","mod","vendor"`, "exitCode")
	if c == "" {
		t.Fatal("a vendored tree is re-vendored")
	}
	if tidy, vendor := strings.Index(c, `"go","mod","tidy"`), strings.Index(c, `"go","mod","vendor"`); tidy < 0 || tidy > vendor {
		t.Errorf("tidy runs before vendor:\n%s", c)
	}

	// A tidy that fails stops the sequence: vendor never runs on a broken graph.
	engine.reset()
	engine.withTree(vendored)
	engine.exitCode(`"go","mod","tidy"`, 1)
	engine.stderr(`"go","mod","tidy"`, "go: example.com/x: reading go.sum\n")
	_, _ = d.Tidy(t.Context())
	if engine.chain(`"go","mod","vendor"`) != "" {
		t.Error("vendor ran after a failed tidy")
	}
	settledOn(t, "1", "reading go.sum")
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
	if _, err := (&FoundryTools{}).Dev(t.Context(), dag.Directory(), "", ""); err == nil || !strings.Contains(err.Error(), "could not read the source root") {
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
