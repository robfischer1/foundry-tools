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
				"**Survivors** — each is a HYPOTHESIS", "src/lib.rs:4: replace g\n```"}, []string{"**Timed out**"}},
		{"timeouts listed", RustMutationRun{Status: 2, Missed: "m\n", Timeout: "src/lib.rs:9: loop"}, 1,
			[]string{"**Timed out** — neither caught nor survived", "src/lib.rs:9: loop\n```"}, nil},
		{"a broken baseline", RustMutationRun{Status: 4, Log: "Found 3 mutants\nwarning: x\nERROR " + strings.Repeat("é", 200) + "\nerror: second"}, 2,
			[]string{"cargo mutants exited 4", "a broken run, not a survivor report", ": ERROR " + strings.Repeat("é", 154)},
			[]string{strings.Repeat("é", 155), "second", "| caught |"}},
		{"a usage error with nothing to say", RustMutationRun{Status: 1, Log: "Usage: cargo mutants"}, 2,
			[]string{"cargo mutants exited 1"}, nil},
	} {
		state, reason := RustMutationVerdict(c.run)
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
