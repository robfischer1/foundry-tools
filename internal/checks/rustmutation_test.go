package checks

import (
	"strings"
	"testing"
)

func TestRustMutationSpecsAreTheDeclarationOrEveryRustSource(t *testing.T) {
	if got := strings.Join(RustMutationSpecs(" src/lib.rs  src/gate.rs "), "|"); got != "src/lib.rs|src/gate.rs" {
		t.Errorf("declared modules: %s", got)
	}
	for _, blank := range []string{"", "   "} {
		if got := strings.Join(RustMutationSpecs(blank), "|"); got != "*.rs|:!tests/" {
			t.Errorf("an empty declaration %q is the whole diff's rust sources, got %s", blank, got)
		}
	}
}

func TestDiffAddsLines(t *testing.T) {
	for diff, want := range map[string]bool{
		"--- a/x.rs\n+++ b/x.rs\n@@ -1 +1 @@\n-a\n+b": true,
		"+b": true,
		"--- a/x.rs\n+++ b/x.rs\n@@ -1,2 +1 @@\n a\n-b": false,
		// The file header alone, and an added blank line, add no mutable code:
		// grep's `^\+[^+]` matched neither.
		"+++ b/x.rs": false,
		"+":          false,
		"":           false,
	} {
		if got := DiffAddsLines(diff); got != want {
			t.Errorf("DiffAddsLines(%q) = %v, want %v", diff, got, want)
		}
	}
}

// A workspace: the root package x at /src and a member gen nested under it.
const rustWorkspace = `{"packages":[
	{"name":"x","id":"x","manifest_path":"/src/Cargo.toml"},
	{"name":"gen","id":"gen","manifest_path":"/src/tools/gen/Cargo.toml"},
	{"name":"dep","id":"dep","manifest_path":"/src/vendor/dep/Cargo.toml"}],
	"workspace_members":["x","gen"]}`

func TestRustTouchedMembersNamesTheDeepestOwner(t *testing.T) {
	for _, c := range []struct {
		name  string
		meta  string
		files []string
		want  string
	}{
		{"both members", rustWorkspace, []string{"src/lib.rs", "tools/gen/src/lib.rs", ""}, "gen x"},
		// tools/gen/ sits inside the root package's directory and belongs to gen.
		{"a nested member only", rustWorkspace, []string{"tools/gen/src/lib.rs"}, "gen"},
		// A package that is not a workspace member owns nothing, so its file
		// falls to the root that holds it.
		{"a non-member's file", rustWorkspace, []string{"vendor/dep/src/lib.rs"}, "x"},
		// A prefix of a directory name is not the directory.
		{"a sibling that shares a prefix", rustWorkspace, []string{"tools/generate.rs"}, "x"},
		{"a file no member holds", rustWorkspace, []string{"../elsewhere.rs"}, ""},
		{"one package", `{"packages":[{"name":"x","id":"x","manifest_path":"/src/Cargo.toml"}],"workspace_members":["x"]}`,
			[]string{"src/lib.rs"}, ""},
	} {
		got, err := RustTouchedMembers([]byte(c.meta), "/src", c.files)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if strings.Join(got, " ") != c.want {
			t.Errorf("%s: members %q, want %q", c.name, strings.Join(got, " "), c.want)
		}
	}
	if _, err := RustTouchedMembers([]byte("warning: not json"), "/src", nil); err == nil || !strings.Contains(err.Error(), "did not parse") {
		t.Errorf("metadata that does not parse must be an error, never the root package: %v", err)
	}
}

func TestRustMutationVerdict(t *testing.T) {
	five := "a\nb\nc\nd\ne\n"
	for _, c := range []struct {
		name   string
		run    RustMutationRun
		state  int
		want   []string
		absent []string
	}{
		{"all caught", RustMutationRun{Status: 0, Caught: five, Unviable: "u\n"}, 0,
			[]string{"every viable mutant was caught (5 of 5)", "| 5 | 0 | 1 | 0 | 100% of 5 viable |"},
			[]string{"**Survivors**", "**Timed out**"}},
		{"nothing to mutate", RustMutationRun{Status: 0}, 0,
			[]string{"(0 of 0)", "| 0 | 0 | 0 | 0 | 0% of 0 viable |"}, nil},
		{"survivors", RustMutationRun{Status: 2, Missed: "src/lib.rs:3: replace f\nsrc/lib.rs:4: replace g", Caught: "c\n\n"}, 1,
			[]string{"2 viable mutant(s) survived the suite", "| 1 | 2 | 0 | 0 | 33% of 3 viable |",
				"**Survivors** — each is a HYPOTHESIS", "src/lib.rs:4: replace g\n```"}, []string{"**Timed out**", "more timed out"}},
		{"timeouts listed", RustMutationRun{Status: 2, Missed: "m\n", Caught: five, Timeout: "src/lib.rs:9: loop"}, 1,
			[]string{"**Timed out** — the suite noticed each", "src/lib.rs:9: loop\n```", "| 5 | 1 | 0 | 1 | 85% of 7 viable |"}, nil},
		// The headline is the first error, cut to 160 runes; the tail follows.
		{"a broken baseline", RustMutationRun{Status: 4, Log: "Found 3 mutants\nwarning: x\nERROR " + strings.Repeat("é", 200) + "\nerror: second"}, 2,
			[]string{"cargo mutants exited 4", "a broken run, not a survivor report", ": ERROR " + strings.Repeat("é", 154) + "\n",
				"the tail of its output:\n```\nFound 3 mutants\n", "\nerror: second\n```"},
			[]string{"| caught |"}},
		{"a usage error with nothing to say", RustMutationRun{Status: 1, Log: "Usage: cargo mutants"}, 2,
			[]string{"cargo mutants exited 1", ": it printed no summary and no error", "```\nUsage: cargo mutants\n```"}, nil},
	} {
		state, reason, _ := RustMutationVerdict(c.run)
		if state != c.state {
			t.Errorf("%s: state %d, want %d\n%s", c.name, state, c.state, reason)
		}
		for _, w := range c.want {
			if !strings.Contains(reason, w) {
				t.Errorf("%s: reason lacks %q:\n%s", c.name, w, reason)
			}
		}
		for _, a := range c.absent {
			if strings.Contains(reason, a) {
				t.Errorf("%s: reason carries %q:\n%s", c.name, a, reason)
			}
		}
	}
}

// A nextest baseline log, as cargo mutants keeps it under mutants.out/log/:
// cargo's build lines, then a status line per test with its wall time, then
// the summary. The SLOW line carries a threshold, not a time.
const nextestBaseline = `   Compiling bellows v0.4.0 (/tmp/mutation/tmp/cargo-mutants-src-x)
    Finished ` + "`test`" + ` profile [unoptimized] target(s) in 37.02s
    Starting 154 tests across 3 binaries
        PASS [   0.004s] bellows::corpus tests::an_empty_corpus_has_no_members
        SLOW [> 60.000s] bellows::mcp tests::witness_waits_for_the_broker
        PASS [  12.311s] bellows::mcp tests::witness_waits_for_the_broker
        FAIL [   3.250s] bellows::mcp tests::hot_set_orders_by_heat
        PASS [   0.900s] bellows::member tests::as_str_names_every_match
     Summary [  28.1s] 154 tests run: 153 passed, 1 failed, 0 skipped
`

func TestSlowestTestsNamesTheBaselinesLongestFirst(t *testing.T) {
	got := SlowestTests(nextestBaseline, 3)
	for _, w := range []string{
		"**Where the test half of each mutant goes**",
		"Summary [  28.1s] 154 tests run: 153 passed, 1 failed, 0 skipped\n",
		"  12.31s  bellows::mcp tests::witness_waits_for_the_broker\n" +
			"   3.25s  bellows::mcp tests::hot_set_orders_by_heat\n" +
			"   0.90s  bellows::member tests::as_str_names_every_match\n```",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("lacks %q:\n%s", w, got)
		}
	}
	if strings.Contains(got, "0.004s") || strings.Contains(got, "an_empty_corpus") {
		t.Errorf("the cut kept a fourth test:\n%s", got)
	}
	if strings.Contains(got, "> 60") {
		t.Errorf("a SLOW threshold was read as a time:\n%s", got)
	}
	// Ten of four is four; no nextest lines is nothing — a libtest baseline
	// grows no section, and neither does a baseline that never built.
	if n := strings.Count(SlowestTests(nextestBaseline, 10), "s  bellows::"); n != 4 {
		t.Errorf("want all 4 tests under a wide cut, got %d", n)
	}
	if got := SlowestTests("running 5 tests\ntest a ... ok\ntest result: ok.", 10); got != "" {
		t.Errorf("a libtest log grew a section:\n%s", got)
	}
	if got := SlowestTests("error[E0425]: cannot find value", 10); got != "" {
		t.Errorf("a broken baseline grew a section:\n%s", got)
	}
	// A tie keeps nextest's order (stable, strictly greater) — the mutation
	// lane's >= survivor: with it, equal times would swap.
	tie := "        PASS [   2.000s] x::a first\n        PASS [   2.000s] x::b second\n        PASS [   1.000s] x::c third\n"
	got = SlowestTests(tie, 2)
	first, second := strings.Index(got, "x::a first"), strings.Index(got, "x::b second")
	if first < 0 || second < 0 || first > second {
		t.Errorf("a tie must keep the printed order:\n%s", got)
	}
	if strings.Contains(got, "x::c third") {
		t.Errorf("the cut of 2 kept a third:\n%s", got)
	}
	if n := strings.Count(SlowestTests(tie, 3), "s  x::"); n != 3 {
		t.Errorf("a cut equal to the count keeps them all, got %d", n)
	}
}

func TestRustMutationVerdictCarriesTheSlowestTests(t *testing.T) {
	_, reason, _ := RustMutationVerdict(RustMutationRun{Status: 0, Caught: "a\n", Baseline: nextestBaseline})
	if !strings.Contains(reason, "  12.31s  bellows::mcp tests::witness_waits_for_the_broker") {
		t.Errorf("the verdict lacks the slowest test:\n%s", reason)
	}
	_, reason, _ = RustMutationVerdict(RustMutationRun{Status: 2, Missed: "m\n", Baseline: nextestBaseline})
	if !strings.Contains(reason, "**Survivors**") || !strings.Contains(reason, "**Where the test half") {
		t.Errorf("a survivor verdict should carry both sections:\n%s", reason)
	}
	_, reason, _ = RustMutationVerdict(RustMutationRun{Status: 0, Caught: "a\n"})
	if strings.Contains(reason, "**Where the test half") {
		t.Errorf("no baseline, no section:\n%s", reason)
	}
}
