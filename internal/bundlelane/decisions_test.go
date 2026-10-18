package bundlelane

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const table = `{"keys":{"v|s|_|_|_":{"d":{"e":"allow","r":0,"f":"VERB_GRANT"},"c":{"athena":{"task_list":{"e":"deny","r":1}},"chaos":{"a":{"e":"deny","r":1},"b":{"e":"deny","r":1}}}},"e|star:ourea|_|_":{"d":{"e":"deny","r":1},"c":{}}},"reasons":[[],["no"]],"prefix":{"athena":"plan"},"caps":{"athena":{"task_list":1}}}`

func TestDecisionsCountsTheTableItGrades(t *testing.T) {
	s, err := Decisions(table)
	if err != nil {
		t.Fatal(err)
	}
	if s.Keys != 2 || s.Cells != 3 || s.ReasonSets != 2 || s.Bytes != len(table) || s.Gzipped <= 0 {
		t.Errorf("%+v", s)
	}
	if !strings.Contains(s.String(), "2 keys, 3 cells, 2 reason sets") {
		t.Error(s.String())
	}
}

func TestDecisionsRefusesATableThatCouldNeverHit(t *testing.T) {
	for name, tc := range map[string]struct{ raw, want string }{
		"not json":     {`{`, "not one JSON object"},
		"no keys":      {`{"keys":{},"prefix":{"a":"b"},"caps":{"a":{}}}`, "no keys"},
		"no prefix":    {`{"keys":{"k":{"d":{"e":"deny"}}},"caps":{"a":{}}}`, "no star prefix"},
		"no caps":      {`{"keys":{"k":{"d":{"e":"deny"}}},"prefix":{"a":"b"}}`, "no capability"},
		"no default":   {`{"keys":{"k":{"c":{}}},"prefix":{"a":"b"},"caps":{"a":{}}}`, "no default cell"},
		"a json array": {`[]`, "not one JSON object"},
	} {
		if _, err := Decisions(tc.raw); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDecisionsArgsLoadThePolicyTheDeriveAndTheRoster(t *testing.T) {
	got := DecisionsArgs("/work/decisions-stage")
	want := []string{"opa", "eval", "-d", "policy", "-d", "tools/authz_table", "-d", "/work/decisions-stage", "--format", "raw", "data.authz_v2_table.decisions"}
	if !slices.Equal(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if !strings.HasPrefix(DecisionsPath, "policy/grants/") || !strings.HasPrefix(DecisionsDerive, DecisionsDeriveDir+"/") {
		t.Error(DecisionsPath, DecisionsDerive)
	}
}

func TestBuiltDecisionsReadsTheTableOutOfTheArtifact(t *testing.T) {
	if n, why := BuiltDecisions(`{"grants":{"rows":[],"decisions":`+table+`}}`, true); why != "" || n != 2 {
		t.Errorf("%d %s", n, why)
	}
	if n, why := BuiltDecisions(`{"grants":{"rows":[]}}`, false); why != "" || n != 0 {
		t.Errorf("no table, none derived: %d %s", n, why)
	}
	if _, why := BuiltDecisions(`{"grants":{"rows":[]}}`, true); !strings.Contains(why, "carries no data.grants.decisions") {
		t.Errorf("a derived table the die lost: %q", why)
	}
	if _, why := BuiltDecisions(`nope`, false); !strings.Contains(why, "not JSON") {
		t.Errorf("not json: %q", why)
	}
}

// The cache wrapper runs the derive once per key and serves the file after, and
// a failed derive caches nothing and passes its output and exit on.
func TestDecisionsCachedArgsDeriveOncePerKey(t *testing.T) {
	args := DecisionsCachedArgs("/work/decisions-stage")
	if tail := args[len(args)-len(DecisionsArgs("/work/decisions-stage")):]; !slices.Equal(tail, DecisionsArgs("/work/decisions-stage")) {
		t.Fatalf("the derive argv is not carried whole: %q", args)
	}
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	run := func(key string, derive ...string) (string, error) {
		argv := append([]string{"-c", decisionsCacheScript, "sh"}, derive...)
		cmd := exec.Command("sh", argv...)
		cmd.Env = append(os.Environ(), "DECISIONS_CACHE="+dir, "DECISIONS_KEY="+key)
		out, err := cmd.Output()
		return string(out), err
	}
	derive := []string{"sh", "-c", "echo x >> " + runs + "; printf '%s' '{\"keys\":1}'"}
	for i := 0; i < 3; i++ {
		if out, err := run("k1", derive...); err != nil || out != `{"keys":1}` {
			t.Fatalf("run %d: %q %v", i, out, err)
		}
	}
	if b, _ := os.ReadFile(runs); strings.Count(string(b), "x") != 1 {
		t.Errorf("the derive ran %d times for one key", strings.Count(string(b), "x"))
	}
	if _, err := run("k2", derive...); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(runs); strings.Count(string(b), "x") != 2 {
		t.Error("another key did not derive")
	}
	out, err := run("k3", "sh", "-c", "printf 'boom'; exit 3")
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 || out != "boom" {
		t.Errorf("a failed derive: %q %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "k3.json")); err == nil {
		t.Error("a failed derive left a cached table")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
		t.Error("a failed derive left its temp file")
	}
}
