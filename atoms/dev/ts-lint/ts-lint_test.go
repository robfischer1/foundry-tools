package devtslint

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/rulesets"
)

type bunRun struct {
	root    string
	calls   []string
	seen    string // the eslint config at the root when the lint script ran
	install int
	lint    int
}

func (b *bunRun) exec(_ context.Context, c atoms.Cmd) (string, int) {
	b.calls = append(b.calls, strings.TrimPrefix(c.Dir, b.root)+": "+c.Name+" "+strings.Join(c.Args, " "))
	if slices.Equal(c.Args[:min(2, len(c.Args))], []string{"install", "--frozen-lockfile"}) {
		return "install said", b.install
	}
	body, _ := os.ReadFile(filepath.Join(b.root, eslintConfig))
	b.seen = string(body)
	return "lint said", b.lint
}

func tree(t *testing.T, b *bunRun, pkg string, args ...string) atomkit.In {
	t.Helper()
	b.root = t.TempDir()
	if err := os.WriteFile(filepath.Join(b.root, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}
	return atomkit.In{Root: b.root, Args: args, Files: []string{"package.json"}, Exec: b.exec}
}

const pkgWithScripts = `{"name":"x","scripts":{"lint":"eslint ."}}`

func TestTheDeclarationIsTheDevVerbOfTheBunGate(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != "dev:ts-lint" || Def.Stage != "dev" || Def.Lane != checks.LaneTS || Def.Toolchain != "bun" || Def.Surface != "root:package.json" ||
		!slices.Equal(Def.Needs, []string{"tool:node", "tool:curl"}) || !Def.WritesTree {
		t.Errorf("%+v", Def)
	}
}

// The fleet's config is what the lint script sees, over the repo's own, and the
// repo's file is back as it was when the verb is done (the delivery diffs the
// tree copy).
func TestLintRunsUnderTheFleetsEslintConfigAndLeavesTheReposOwn(t *testing.T) {
	b := &bunRun{}
	in := tree(t, b, pkgWithScripts, "--max-warnings=0")
	own := filepath.Join(b.root, eslintConfig)
	if err := os.WriteFile(own, []byte("export default []; // the repo's\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	v := check(t.Context(), in)
	if v.State != int(checks.StatePass) {
		t.Fatalf("%+v", v)
	}
	if b.seen != rulesets.ESLint {
		t.Errorf("the script saw %q, not the fleet's config", b.seen)
	}
	want := []string{": bun install --frozen-lockfile", ": bun run lint --max-warnings=0"}
	if !slices.Equal(b.calls, want) {
		t.Errorf("ran %v, want %v", b.calls, want)
	}
	got, _ := os.ReadFile(own)
	fi, _ := os.Stat(own)
	if string(got) != "export default []; // the repo's\n" || fi.Mode().Perm() != 0o444 {
		t.Errorf("the repo's config was not put back: %q %v", got, fi.Mode())
	}
}

// A repo with no config of its own is left with none.
func TestLintLeavesNoConfigWhereTheRepoHadNone(t *testing.T) {
	b := &bunRun{}
	in := tree(t, b, pkgWithScripts)
	check(t.Context(), in)
	if _, err := os.Stat(filepath.Join(b.root, eslintConfig)); !os.IsNotExist(err) {
		t.Errorf("the fleet's config was left behind: %v", err)
	}
}

func TestLintSettlesFindingsAndAFailedInstall(t *testing.T) {
	b := &bunRun{lint: 1}
	if v := check(t.Context(), tree(t, b, pkgWithScripts)); v.State != int(checks.StateFindings) {
		t.Errorf("the script's exit 1 is the finding: %+v", v)
	}
	b = &bunRun{install: 1}
	v := check(t.Context(), tree(t, b, pkgWithScripts))
	if v.State != int(checks.StateCannotRun) || !strings.Contains(v.Reason, "preflight `bun install --frozen-lockfile` exited 1: install said") || len(b.calls) != 1 {
		t.Errorf("a lockfile that will not install is could-not-run and the script never ran: %+v %v", v, b.calls)
	}
}

// A bare workspace root has no lint script: each member's runs, and a bare root
// that matches no member is could-not-run, never a pass.
func TestLintRunsEachMemberOfABareWorkspaceRoot(t *testing.T) {
	bare := `{"private":true,"workspaces":["packages/*"]}`
	b := &bunRun{}
	in := tree(t, b, bare)
	in.Files = []string{"package.json", "packages/a/package.json", "packages/b/package.json"}
	if v := check(t.Context(), in); v.State != int(checks.StatePass) {
		t.Fatalf("%+v", v)
	}
	want := []string{"/packages/a: bun install --frozen-lockfile", "/packages/a: bun run lint", "/packages/b: bun install --frozen-lockfile", "/packages/b: bun run lint"}
	if !slices.Equal(b.calls, want) {
		t.Errorf("ran %v, want %v", b.calls, want)
	}
	b = &bunRun{}
	if v := check(t.Context(), tree(t, b, bare)); v.State != int(checks.StateCannotRun) || len(b.calls) != 0 {
		t.Errorf("%+v %v", v, b.calls)
	}
}
