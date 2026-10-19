package devgotestlive

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

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

// suites are the `go test` runs that were suites (not the list or download).
func suites(f *fake) []string {
	var out []string
	for _, r := range f.runs {
		if strings.Contains(r, ": go test") {
			out = append(out, r)
		}
	}
	return out
}

func TestTheDeclarationIsTheDevVerbWithTheRaceAtomsNeeds(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != "dev:go-test-live" || Def.Stage != "dev" || Def.Lane != checks.LaneGo || Def.Group != checks.GroupLanguage ||
		Def.Toolchain != "go" || Def.Surface != "root:go.mod" || Def.Timeout != 45*time.Minute || Def.WritesTree {
		t.Errorf("%+v", Def)
	}
	want := append([]string{"dies", "service:postgres", "service:pgvector", "service:redpanda", "service:redis", "service:etcd", "service:kube-apiserver"}, checks.GravityNeeds()...)
	if !slices.Equal(Def.Needs, want) {
		t.Errorf("needs %v, go:test-race's are %v", Def.Needs, want)
	}
	if row.ID != Def.ID || row.Stage != Def.Stage || row.Lane != Def.Lane {
		t.Errorf("%+v", row)
	}
}

// With no arguments it is the push's race run over the tree's tags, less the
// coverage profile and the warm-up build.
func TestNoArgumentsIsTheRaceRunWithoutTheProfile(t *testing.T) {
	f := &fake{tags: "//go:build live_db\n"}
	dir := tree(t, "go.mod")
	v := check(t.Context(), atomkit.In{Root: dir, Files: []string{"go.mod"}, Exec: f.exec})
	if v.State != 0 || !strings.Contains(v.Reason, "test databases: live_db → TEST_DATABASE_URL") {
		t.Fatalf("%+v", v)
	}
	got := suites(f)
	if len(got) != 1 || !strings.HasSuffix(got[0], ": go test -race -tags live_db -p 1 ./...") {
		t.Errorf("ran %v", got)
	}
}

// Every module is run when no package is named.
func TestEveryModuleRunsWhenNoPackageIsNamed(t *testing.T) {
	f := &fake{}
	dir := tree(t, "go.mod", "styx/go.mod")
	v := check(t.Context(), atomkit.In{Root: dir, Files: []string{"go.mod", "styx/go.mod"}, Args: []string{"-run", "TestX"}, Exec: f.exec})
	got := suites(f)
	if v.State != 0 || len(got) != 2 || !strings.HasSuffix(got[0], ": go test -race -run TestX ./...") || !strings.Contains(got[1], "/styx:") {
		t.Fatalf("%+v ran %v", v, got)
	}
}

// A named package replaces ./... and runs in the root module only; the
// caller's tag joins the tree's and, not being the fleet's, is said so.
func TestACallersPackagesAndTagsFoldIntoTheRun(t *testing.T) {
	f := &fake{tags: "//go:build live_db\n"}
	dir := tree(t, "go.mod", "styx/go.mod")
	v := check(t.Context(), atomkit.In{Root: dir, Files: []string{"go.mod", "styx/go.mod"},
		Args: []string{"-tags", "live", "-race", "-run", "TestStore", "-count=1", "./internal/store/"}, Exec: f.exec})
	got := suites(f)
	if v.State != 0 || len(got) != 1 || !strings.HasSuffix(got[0], ": go test -race -tags live_db,live -p 1 -run TestStore -count=1 ./internal/store/") {
		t.Fatalf("%+v ran %v", v, got)
	}
	if !strings.Contains(v.Reason, "caller tags compiled with no server bound: live") {
		t.Errorf("reason does not say the tag is unbound: %q", v.Reason)
	}
}
