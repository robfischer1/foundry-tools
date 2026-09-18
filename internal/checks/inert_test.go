package checks

import (
	"path"
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
// Dockerfile, a .melt, migrations, plugins, an answers-free source tree —
// and CLAUDE-INIT.md, which hephaestus' module-map test opens off the repo
// root (hephaestus #113 reddened on its absence). Membership is checked by
// MATCHING, not by name: a glob that would swallow one of these is the same
// regression as naming it.
func TestInertPathsKeepWhatTestsRead(t *testing.T) {
	for _, keep := range []string{"Dockerfile", "nereus.melt", "migrations", "plugins", "go.mod", "go.sum", "vendor", "main.go", "orbit.toml", "copier.yml", ".git", "CLAUDE-INIT.md"} {
		for _, p := range InertPaths {
			if ok, err := path.Match(p, keep); err != nil || ok {
				t.Errorf("%s is read by a test somewhere in the fleet and must not be inert, but %q excludes it", keep, p)
			}
		}
	}
	for _, drop := range []string{"README.md", "CHANGELOG.md", "docs", ".forgejo", "hooks", "justfile", "*.just", ".pre-commit-config.yaml", ".copier-answers.yml"} {
		if !slices.Contains(InertPaths, drop) {
			t.Errorf("%s is the unrelated edit this narrowing exists for and must be inert", drop)
		}
	}
}

// No glob over prose: the first cut shipped `*.md`, and the .md nobody had
// enumerated was the one a test reads. Prose is excluded by name only.
func TestInertPathsNameNoProseGlob(t *testing.T) {
	if p := InertPathsNameNoProseGlob(InertPaths); p != "" {
		t.Fatalf("InertPaths excludes prose by glob %q — name the files, a glob excludes what nobody enumerated", p)
	}
	for _, bad := range []string{"*.md", "CLAUDE-*.md", "READ?E.md", "[A-Z]*.md"} {
		if p := InertPathsNameNoProseGlob([]string{"README.md", bad}); p != bad {
			t.Errorf("want %q refused, got %q", bad, p)
		}
	}
	if p := InertPathsNameNoProseGlob([]string{"README.md", "*.just", "LICENSE.*"}); p != "" {
		t.Errorf("a glob over non-prose is not this invariant's business, got %q", p)
	}
}
