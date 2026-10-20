package catalogue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// PLAN SNAPSHOTS (monorepo migration, runbook D-B3/D-B4/D-B5). The lane tooling
// learned to read a virtual Cargo workspace and a bare npm workspace root. It must
// be a no-op for every repository that is neither, so the plan the whole
// catalogue makes over today's three kit layouts is held to files recorded from
// the tooling BEFORE the change (testdata/plan-snapshots/): the lanes, the atoms
// that run, and the atoms that stand down. A layout whose plan moves is a
// tooling change that was not inert.
//
// The root entries and counts are those of rob/stellar-core-rust, -go and -ts at
// origin/main on 2026-10-19.
var snapshotLayouts = map[string]checks.Tree{
	"stellar-core-rust": {
		Entries:     strings.Fields(".cargo .copier-answers.speckit.yml .copier-answers.yml .editorconfig .gitignore CLAUDE-INIT.md CODE_OF_CONDUCT.md Cargo.lock Cargo.toml LICENSE README.md SECURITY.md cliff.toml cosign.pub crates dagger.json dev.just hooks justfile rules src stages.just tests tools wit-guest-budgets.toml wit"),
		PythonFiles: 4,
	},
	"stellar-core-go": {
		Entries:     strings.Fields(".copier-answers.speckit.yml .copier-answers.yml .editorconfig .gitignore .release-ignore CODE_OF_CONDUCT.md LICENSE README.md SECURITY.md alarm cli cliff.toml cmd conformance cosign.pub dagger.json dev.just go.mod go.sum hooks internal justfile ruff.toml rules specs stages.just tools"),
		GoModules:   1,
		PythonFiles: 8,
	},
	"stellar-core-ts": {
		Entries: strings.Fields(".copier-answers.speckit.yml .copier-answers.yml .editorconfig .gitignore .npmrc .nvmrc .prettierignore CODE_OF_CONDUCT.md CONTRIBUTING.md LICENSE MASTER-PLAN.md PARITY.md README.md SECURITY.md __tests__ bun.lock cliff.toml cosign.pub dagger.json dev.just eslint.config.mjs hooks justfile package-budget.json package.json prettier.config.mjs rules scripts src stages.just stryker.config.json tsconfig.build.json tsconfig.json vitest.config.ts"),
	},
}

func planText(tree checks.Tree) string {
	p := checks.PlanAtoms(checks.Atoms(), tree)
	ids := func(as []checks.AtomDef) string {
		var out []string
		for _, a := range as {
			out = append(out, a.ID)
		}
		return strings.Join(out, "\n  ")
	}
	var lanes []string
	for _, l := range p.Lanes {
		lanes = append(lanes, string(l))
	}
	return "lanes: " + strings.Join(lanes, " ") + "\nrun:\n  " + ids(p.Run) + "\nabsent:\n  " + ids(p.Absent) + "\nfork:\n  " + ids(p.Fork) + "\n"
}

func TestTodaysKitLayoutsPlanExactlyAsTheyDidBeforeTheMonorepoTooling(t *testing.T) {
	for name, tree := range snapshotLayouts {
		t.Run(name, func(t *testing.T) {
			got := planText(tree)
			want, err := os.ReadFile(filepath.Join("testdata", "plan-snapshots", name+".txt"))
			if err != nil {
				t.Fatalf("no snapshot for %s (%v); the plan is:\n<<<%s\n>>>", name, err, got)
			}
			if got != string(want) {
				t.Errorf("%s plans differently:\n--- got\n%s\n--- recorded\n%s", name, got, want)
			}
		})
	}
}

// The layouts' manifests are not the opt-in shapes: the Rust root is a package
// (its own wit facade) and the ts root is a package with scripts (its own gate).
func TestTodaysManifestsAreNotTheOptInShapes(t *testing.T) {
	rustRoot := "[package]\nname = \"stellar-core\"\nversion = \"0.89.0\"\n\n[lib]\ncrate-type = [\"rlib\", \"cdylib\"]\n\n[workspace]\nmembers = [\"crates/*\"]\n\n[workspace.dependencies]\nserde = \"1\"\n"
	if checks.IsVirtualWorkspace(rustRoot) {
		t.Error("stellar-core-rust's root is a package and its own facade")
	}
	tsRoot := `{"name": "@forge/stellar-core-ts", "version": "0.80.0", "scripts": {"build": "tsc", "gate": "bun run lint", "test": "vitest"}}`
	if members, bare := checks.TSBareWorkspaceMembers(tsRoot, []string{"package.json", "src/index.ts"}); bare || members != nil {
		t.Errorf("stellar-core-ts's root is a package with scripts: %v %v", members, bare)
	}
	// and the Rust kit is the monorepo's rust/ (D-B5); the Go kit has not moved
	if checks.RustKit.GitURL() != "https://git.notusmi.com/stellar-core.git" || checks.RustKit.Subdir != "rust" || checks.GoKit.Repo != "rob/stellar-core-go" || checks.GoKit.Subdir != "" {
		t.Errorf("%+v %+v", checks.RustKit, checks.GoKit)
	}
}
