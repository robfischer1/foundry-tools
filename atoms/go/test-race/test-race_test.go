package gotestrace

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

// fake is an Exec that records every run: a go list counts one test file, a
// grep finds a tagged suite, and everything else exits 0.
type fake struct {
	tags string
	runs []string
}

func (f *fake) exec(_ context.Context, c atoms.Cmd) (string, int) {
	line := strings.Join(append([]string{c.Name}, c.Args...), " ")
	f.runs = append(f.runs, c.Dir+": "+line)
	switch {
	case strings.HasPrefix(line, "go list"):
		return "11", 0
	case strings.HasPrefix(line, "grep"):
		return f.tags, 0
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
	if Def.ID != "go:test-race" || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneGo || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "go" || Def.Surface != "any:**/go.mod" || Def.SubsumedBy != "" || Def.NotOnForks != "" ||
		strings.Join(Def.Needs, ",") != "dies,service:postgres,service:pgvector,service:redpanda,tool:gravity" {
		t.Errorf("%+v", Def)
	}
	if atoms.GoRaceRow.ID != Def.ID || atoms.GoRaceRow.Stage != Def.Stage || atoms.GoRaceRow.Lane != Def.Lane {
		t.Errorf("go:diff-coverage names this atom's suite by %+v", atoms.GoRaceRow)
	}
}

// The push's run is the race run against the databases the tree's tags name,
// once per module, each in its own directory.
func TestTheRaceSuiteRunsInEveryModuleAgainstTheTreesDatabases(t *testing.T) {
	f := &fake{tags: "//go:build live_db\n"}
	dir := tree(t, "go.mod", "styx/go.mod")
	v := check(t.Context(), atomkit.In{Root: dir, Files: []string{"go.mod", "styx/go.mod"}, Exec: f.exec})
	if v.State != 0 || !strings.Contains(v.Reason, "go:test-race: PASS in 2 Go modules (., styx)") || !strings.Contains(v.Reason, "test databases: live_db → TEST_DATABASE_URL") {
		t.Fatalf("%+v", v)
	}
	var tests []string
	for _, r := range f.runs {
		// the build-only pass before each suite (DEF-B4-PERF-3) is not a suite
		if strings.Contains(r, ": go test") && !strings.Contains(r, "-exec true") {
			tests = append(tests, r)
		}
	}
	want := ": go test -race -tags live_db -p 1 -coverprofile " + checks.GoCoverProfile + " ./..."
	if len(tests) != 2 || !strings.HasSuffix(tests[0], want) || !strings.HasSuffix(tests[1], want) || !strings.Contains(tests[1], "/styx:") {
		t.Errorf("the race suite ran as %v", tests)
	}
}
