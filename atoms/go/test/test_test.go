package gotest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// fake is an Exec that records every run and answers a go list with one test
// file, a go test with the exit the test sets.
type fake struct {
	testExit int
	testOut  string
	runs     []string
}

func (f *fake) exec(_ context.Context, c atoms.Cmd) (string, int) {
	line := strings.Join(append([]string{c.Name}, c.Args...), " ")
	f.runs = append(f.runs, c.Dir+": "+line)
	switch {
	case strings.HasPrefix(line, "go list"):
		return "11", 0
	case strings.HasPrefix(line, "go test"):
		return f.testOut, f.testExit
	}
	return "", 0
}

func tree(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestUT_485_TheDeclarationIsTheRowItReplaced: the Def carries the identity the
// catalogue row did, and validates.
func TestUT_485_TheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneGo || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "go" || Def.Surface != "any:**/go.mod" || Def.SubsumedBy != "go:test-race" || Def.NotOnForks != "" ||
		strings.Join(Def.Needs, ",") != "dies,tool:gravity" || Def.NeedsManifest {
		t.Errorf("%+v", Def)
	}
}

// The commit's run is the unit suite: no race, no tags, one run per module.
func TestTheUnitSuiteRunsInEveryModule(t *testing.T) {
	f := &fake{}
	dir := tree(t, "go.mod", "styx/go.mod")
	v := check(t.Context(), atomkit.In{Root: dir, Files: []string{"go.mod", "styx/go.mod"}, Exec: f.exec})
	if v.State != 0 || !strings.Contains(v.Reason, "go:test: PASS in 2 Go modules (., styx)") || !strings.Contains(v.Reason, "unit suite") {
		t.Fatalf("%+v", v)
	}
	var tests []string
	for _, r := range f.runs {
		if strings.Contains(r, ": go test") {
			tests = append(tests, r)
		}
	}
	if len(tests) != 2 || !strings.HasSuffix(tests[0], ": go test ./...") || !strings.HasSuffix(tests[1], "/styx: go test ./...") {
		t.Errorf("the unit suite ran as %v", tests)
	}
}

func TestAFailingSuiteIsTheAtomsFinding(t *testing.T) {
	f := &fake{testExit: 1, testOut: "--- FAIL: TestX (0.00s)\nFAIL\n"}
	dir := tree(t, "go.mod")
	v := check(t.Context(), atomkit.In{Root: dir, Files: []string{"go.mod"}, Exec: f.exec})
	if v.State != 1 || !strings.Contains(v.Reason, "go:test: FINDINGS (exit 1)") {
		t.Errorf("%+v", v)
	}
}
