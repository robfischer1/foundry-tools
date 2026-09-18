package checks

import (
	"slices"
	"testing"
)

// The narrowing is an exclude of root-level prose and tool files, never a
// pattern that could reach a package's fixtures.
func TestInertPathsAreRootAnchored(t *testing.T) {
	if p := InertPathsAreRootAnchored(InertPaths); p != "" {
		t.Fatalf("InertPaths carries a pattern that is not root-anchored: %q", p)
	}
	for name, bad := range map[string][]string{
		"a recursive glob":      {"*.md", "**/*.md"},
		"a path into a package": {"internal/checks/README.md"},
		"an empty pattern":      {""},
	} {
		if p := InertPathsAreRootAnchored(bad); p != bad[len(bad)-1] {
			t.Errorf("%s: want %q refused, got %q", name, bad[len(bad)-1], p)
		}
	}
}

// What the fleet's tests were measured to read stays in the mount: a
// Dockerfile, a .melt, migrations, plugins, an answers-free source tree.
func TestInertPathsKeepWhatTestsRead(t *testing.T) {
	for _, keep := range []string{"Dockerfile", "nereus.melt", "migrations", "plugins", "go.mod", "go.sum", "vendor", "main.go", "orbit.toml", "copier.yml", ".git"} {
		if slices.Contains(InertPaths, keep) {
			t.Errorf("%s is read by a test somewhere in the fleet and must not be inert", keep)
		}
	}
	for _, drop := range []string{"*.md", "docs", ".forgejo", "hooks", "justfile", "*.just", ".pre-commit-config.yaml", ".copier-answers.yml"} {
		if !slices.Contains(InertPaths, drop) {
			t.Errorf("%s is the unrelated edit this narrowing exists for and must be inert", drop)
		}
	}
}
