package rustmut

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// testOnly is a pull that changed only tests/it.rs, an integration test of the
// root crate, and touched no source.
func testOnly(t *testing.T, answers string) *pull {
	t.Helper()
	p := newPull(t, answers)
	putFile(t, p.in.Root, "tests/it.rs", "#[test]\nfn t() {}\n")
	putFile(t, p.in.Root, "src/util.rs", "pub fn g() {}\n")
	p.in.Files = append(p.in.Files, "tests/it.rs", "src/util.rs")
	p.first(rule{"--unified=0 since0 HEAD -- *.rs", "diff --git a/tests/it.rs b/tests/it.rs\n+++ b/tests/it.rs\n@@ -0,0 +2 @@\n+fn t() {}\n", 0})
	p.first(rule{"git diff --relative since0 HEAD", "", 0})
	p.first(rule{"--name-only -z", "", 0})
	return p
}

// A test-only change is mutation-tested by its crate: the toolchain has no line
// coverage, so the source files of the crate the test belongs to join the scope
// (the verdict says so), and the test file itself is never mutated.
func TestCoveredATestOnlyRustChangeMutatesItsCrate(t *testing.T) {
	p := testOnly(t, "")
	v := p.run()
	if strings.Contains(said(v), "nothing to mutate") || !p.ran("cargo mutants --colors") {
		t.Fatalf("a test-only pull took the empty-change-set exit: %s", said(v))
	}
	raw, _ := os.ReadFile(filepath.Join(p.work, "rust", "pr.diff"))
	diff := string(raw)
	if !strings.Contains(diff, "+++ b/src/lib.rs\n@@ -0,0 +1,1 @@\n+fn f() -> i32 { 2 }\n") || !strings.Contains(diff, "+++ b/src/util.rs") || strings.Contains(diff, "tests/it.rs") {
		t.Fatalf("pr.diff = %q", diff)
	}
	if !strings.Contains(said(v), "rust:mutation: changed tests: 2 source line(s) in 2 file(s) of their crate(s) joined the scope") || !strings.Contains(said(v), "no line coverage") {
		t.Errorf("the verdict does not say what the tests added, and why the crate: %s", said(v))
	}
}

// A change inside a #[cfg(test)] module is a test change too.
func TestCoveredARustCfgTestModChangeMutatesItsCrate(t *testing.T) {
	p := newPull(t, "")
	putFile(t, p.in.Root, "src/util.rs", "pub fn g() {}\n\n#[cfg(test)]\nmod tests {\n    #[test]\n    fn t() { super::g(); }\n}\n")
	p.in.Files = append(p.in.Files, "src/util.rs")
	p.first(rule{"--unified=0 since0 HEAD -- *.rs", "+++ b/src/util.rs\n@@ -5,0 +6 @@\n+    fn t() { super::g(); }\n", 0})
	p.first(rule{"git diff --relative since0 HEAD", "diff --git a/src/util.rs b/src/util.rs\n+++ b/src/util.rs\n@@ -5,0 +6 @@\n+    fn t() { super::g(); }", 0})
	v := p.run()
	raw, _ := os.ReadFile(filepath.Join(p.work, "rust", "pr.diff"))
	if !strings.Contains(string(raw), "+fn f() -> i32 { 2 }\n") || !strings.Contains(string(raw), "+pub fn g() {}\n") {
		t.Fatalf("the crate did not join the scope: pr.diff = %q: %s", raw, said(v))
	}
}

// A source change outside any test module is no test change: nothing is added.
func TestCoveredARustSourceChangeAddsNoCrate(t *testing.T) {
	p := newPull(t, "")
	putFile(t, p.in.Root, "src/util.rs", "pub fn g() {}\n")
	p.in.Files = append(p.in.Files, "src/util.rs")
	p.first(rule{"--unified=0 since0 HEAD -- *.rs", "+++ b/src/lib.rs\n@@ -1 +1 @@\n+fn f() -> i32 { 2 }\n", 0})
	v := p.run()
	raw, _ := os.ReadFile(filepath.Join(p.work, "rust", "pr.diff"))
	if strings.Contains(string(raw), "util.rs") || strings.Contains(said(v), "changed tests") {
		t.Fatalf("pr.diff = %q: %s", raw, said(v))
	}
}

// The edges: a diff git refuses and a tree that cannot be listed are said and
// add nothing; a test no package manifest holds adds nothing; the declared
// modules bound the crate's files.
func TestCoveredRustEdges(t *testing.T) {
	p := testOnly(t, "")
	p.first(rule{"--unified=0 since0 HEAD -- *.rs", "fatal", 128})
	if v := p.run(); !strings.Contains(said(v), "git could not diff them") || p.ran("cargo mutants --colors") {
		t.Errorf("%s", said(v))
	}

	p = testOnly(t, "")
	p.in.FilesErr = os.ErrPermission
	if v := p.run(); !strings.Contains(said(v), "the tree could not be listed") {
		t.Errorf("%s", said(v))
	}

	p = testOnly(t, "")
	putFile(t, p.in.Root, "Cargo.toml", "[workspace]\n")
	if v := p.run(); strings.Contains(said(v), "changed tests") || p.ran("cargo mutants --colors") {
		t.Errorf("a test no crate holds joined a scope: %s", said(v))
	}

	p = testOnly(t, "")
	putFile(t, p.in.Root, "tools/gen/Cargo.toml", "[package]\nname='gen'\n")
	putFile(t, p.in.Root, "tools/gen/src/main.rs", "fn main() {}\n")
	putFile(t, p.in.Root, "build.rs", "fn main() {}\n")
	p.in.Files = append(p.in.Files, "tools/gen/Cargo.toml", "tools/gen/src/main.rs", "build.rs")
	p.run()
	raw, _ := os.ReadFile(filepath.Join(p.work, "rust", "pr.diff"))
	if strings.Contains(string(raw), "tools/gen") || strings.Contains(string(raw), "build.rs") || !strings.Contains(string(raw), "src/util.rs") {
		t.Errorf("another crate's files or a build script joined: pr.diff = %q", raw)
	}

	p = testOnly(t, "critical_modules: src/util.rs\n")
	p.run()
	raw, _ = os.ReadFile(filepath.Join(p.work, "rust", "pr.diff"))
	if !strings.Contains(string(raw), "+++ b/src/util.rs") || strings.Contains(string(raw), "src/lib.rs") {
		t.Errorf("pr.diff = %q", raw)
	}
}

// The bound applies to the crate's lines.
func TestCoveredRustLinesAreBoundedByTheCap(t *testing.T) {
	p := testOnly(t, "")
	t.Setenv(checks.EnvMutationCoveredCap, "1")
	v := p.run()
	raw, _ := os.ReadFile(filepath.Join(p.work, "rust", "pr.diff"))
	if !strings.Contains(said(v), "1 line(s) in 1 file(s) cut at the "+checks.EnvMutationCoveredCap+" bound (src/util.rs)") || strings.Contains(string(raw), "util.rs") {
		t.Fatalf("pr.diff %q: %s", raw, said(v))
	}
}
