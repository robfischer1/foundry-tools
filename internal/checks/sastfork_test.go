package checks

import (
	"strings"
	"testing"
)

func TestForkSastRulesetsFollowTheFilesNotTheRootManifest(t *testing.T) {
	// gravity's shape: a Cargo workspace, Go modules down in examples/.
	got := ForkSastRulesets([]string{"Cargo.toml", "cmd/g/src/main.rs", "examples/a/zz_generated.go", "README.md"})
	if len(got) != 2 || got[0].File != "go.yml" || got[1].File != "rust.yml" {
		t.Fatalf("%+v", got)
	}
	if got := ForkSastRulesets([]string{"README.md", "justfile"}); len(got) != 0 {
		t.Errorf("a tree with no covered language: %+v", got)
	}
	if got := ForkSastRulesets([]string{"web/a.tsx", "b.py"}); len(got) != 2 || got[0].File != "python.yml" || got[1].File != "frontend.yml" {
		t.Errorf("%+v", got)
	}
}

// Every built-in ruleset declares the language it is chosen for, so a scan of a
// tree that holds that language cannot be the zero-file scan.
func TestForkSastRulesetsDeclareTheirOwnLanguage(t *testing.T) {
	want := map[string]string{"go.yml": "go", "rust.yml": "rust", "python.yml": "python", "frontend.yml": "typescript"}
	for _, l := range forkSastLangs {
		declared, _ := SastLanesMissing([]string{l.Body}, nil)
		found := false
		for _, d := range declared {
			found = found || d == want[l.File]
		}
		if !found {
			t.Errorf("%s declares %v, want %s", l.File, declared, want[l.File])
		}
		if len(l.Exts) == 0 || !strings.Contains(l.Body, "rules:") {
			t.Errorf("%s is not a ruleset", l.File)
		}
	}
	if g := ForkSastGlobs(); len(g) != 9 || g[0] != "**/*.go" {
		t.Errorf("%v", g)
	}
}

func TestOpengrepFindingCount(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want int
	}{
		{"Ran 12 rules on 80 files: 3 findings.", 3},
		{"Ran 12 rules on 80 files: 1 finding.", 1},
		{"Ran 4 rules on 12 files: 0 findings.", 0},
		{"boom", -1},
		{"Ran 1 rules on 1 files: 99999999999999999999 findings.", -1},
	} {
		if got := OpengrepFindingCount(tc.out); got != tc.want {
			t.Errorf("%q: %d, want %d", tc.out, got, tc.want)
		}
	}
}

func TestForkSastVerdict(t *testing.T) {
	a := AtomDef{ID: "fleet:opengrep-sast", Stage: StagePrecommit, Lane: LaneAny}
	sets := ForkSastRulesets([]string{"a.go", "b.rs"})
	scanned := "Ran 8 rules on 90 files: "

	// Findings in a fork pass, carry the findings, and are not a finding state.
	v := ForkSastVerdict(a, "gravity", sets, "x.go: ssrf\n"+scanned+"2 findings.", 1)
	if v.State != int(StatePass) || v.Result != "pass" || Stops(v) || len(v.Findings) != 0 {
		t.Errorf("findings must not stop a fork: %+v", v)
	}
	for _, want := range []string{"ADVISORY", "2 finding(s)", "NOT blocking", "x.go: ssrf", "fleet/repos/gravity.json is kind vendored", "[go rust]"} {
		if !strings.Contains(v.Reason, want) {
			t.Errorf("reason lacks %q:\n%s", want, v.Reason)
		}
	}
	// A count the output does not carry still passes, and says "some".
	if v := ForkSastVerdict(a, "gravity", sets, "x.go: ssrf", 1); v.State != 0 || !strings.Contains(v.Reason, "some finding(s)") {
		t.Errorf("%+v", v)
	}

	// A clean scan is a plain pass that names the rulesets it used.
	v = ForkSastVerdict(a, "gravity", sets, scanned+"0 findings.", 0)
	if v.State != 0 || !strings.Contains(v.Reason, "PASS") || !strings.Contains(v.Reason, "no findings") || strings.Contains(v.Reason, "ADVISORY") {
		t.Errorf("%+v", v)
	}

	// An unexamined tree and a failed tool are still cannot-run: a fork is
	// excused upstream's findings, not an unexamined scan.
	if v := ForkSastVerdict(a, "gravity", sets, "Ran 8 rules on 0 files: 0 findings.", 0); v.State != int(StateCannotRun) || !strings.Contains(v.Reason, ForkSastRefusal) {
		t.Errorf("zero files: %+v", v)
	}
	if v := ForkSastVerdict(a, "gravity", sets, "Ran 8 rules on 0 files: 0 findings.", 1); v.State != int(StateCannotRun) {
		t.Errorf("zero files with exit 1: %+v", v)
	}
	if v := ForkSastVerdict(a, "gravity", sets, "boom", 7); v.State != int(StateCannotRun) {
		t.Errorf("exit 7: %+v", v)
	}
}
