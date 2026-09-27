package checks

import "strings"

// InertPaths is what the TOOLCHAIN atoms never read: the files a tree carries
// for readers, for the door, and for other tools, which a compile, a vet, a
// lint or a test suite does not open. A mount without them keys an atom's
// exec on the code alone, so an edit to any of them re-runs nothing in the
// language lanes — the README, the changelog the door regenerates at every
// tag, the governance furnace pours, the answers file Renovate bumps, the
// hooks and the justfile (CA master-plan F12, "narrowing").
//
// MEASURED, NOT ASSUMED — AND MEASURED TWICE. Every entry was checked against
// what the fleet's tests actually open: a git grep of every string literal
// naming one of these files, every `os.ReadFile|os.Open|os.ReadDir|
// filepath.Glob|os.Stat`, every `//go:embed`, and every test helper that
// walks up to go.mod and joins a name onto the root, across the 33 Go stars
// on main, 2026-09-18. What they DO read outside their own package — `../../
// Dockerfile` (ananke, blade-runner, cql, hermes, prometheus, nereus, ourea),
// `../../nereus.melt`, `plugins/*/plugin.json` (argus), `../../migrations/
// *.sql` (eros), `main.go`, `.git`, in-package schemas and vectors — is not
// here, and that is why this is an EXCLUDE and not an include: no derivation
// from `go list` could have known about the Dockerfile or the migrations.
//
// THE FIRST MEASUREMENT MISSED ONE, and the shape of the miss is the rule
// the set now follows. The first scan keyed on relative-path literals
// (`"../../Dockerfile"`) and shipped `*.md`; hephaestus' module-map test
// reads `filepath.Join(repoRoot(t), "CLAUDE-INIT.md")` — the name is a
// literal, the root is computed — and hephaestus #113 went red on
// `open /src/CLAUDE-INIT.md: no such file or directory`. So: NO GLOB OVER
// PROSE. Every prose file here is NAMED, and a name goes on the list only
// after the grep above finds nothing opening it. A glob excludes files
// nobody enumerated, and the one nobody enumerated is the one a test reads.
// InertPathsNameNoProseGlob holds it.
//
// ROOT-ANCHORED BY CONSTRUCTION. These are Dagger exclude patterns, and a
// pattern without `**/` matches from the root only: `README.md` drops the
// root README, never a fixture under some package's testdata/. Nothing
// here may reach into a package directory — InertPathsAreRootAnchored holds
// it, because a `**` slipped in would turn a narrowing into a silent
// fixture deletion.
//
// The mutation and witness atoms do NOT mount this narrowing: they run git
// against the tree, and an excluded file reads as deleted to a working-tree
// diff. They keep the whole tree (run.lane); the compile/vet/lint/test atoms
// take run.laneCode.
var InertPaths = []string{
	// Root prose, by name. CLAUDE-INIT.md is deliberately absent: hephaestus'
	// internal/spec/module_map_test.go reads it.
	"README.md", "CHANGELOG.md", "CONTRIBUTING.md", "SECURITY.md", "CODE_OF_CONDUCT.md",
	"AGENTS.md", "CLAUDE.md", "COWORK.md", "GEMINI.md",
	"LICENSE", "LICENSE.*", "NOTICE",
	"docs", "specs",
	".forgejo", ".github", ".gitea",
	"hooks", "justfile", "*.just",
	".pre-commit-config.yaml",
	".copier-answers.yml", ".copier-answers.*.yml",
	".gitignore", ".gitattributes", ".editorconfig",
	".hadolint.yaml", "cliff.toml", "renovate.json",
}

// InertPathsAreRootAnchored is the invariant the exclude set lives under: no
// pattern recurses (`**`), none names a path with a separator, so none can
// match inside a package directory. Answers the offending pattern, or "".
func InertPathsAreRootAnchored(paths []string) string {
	for _, p := range paths {
		if strings.Contains(p, "**") || strings.Contains(p, "/") || p == "" {
			return p
		}
	}
	return ""
}

// InertPathsNameNoProseGlob is the second invariant: no pattern in the set
// is a glob that could match a Markdown file, because the prose a test reads
// (CLAUDE-INIT.md) is exactly the prose a glob would have excluded without
// anyone naming it. Answers the offending pattern, or "".
func InertPathsNameNoProseGlob(paths []string) string {
	for _, p := range paths {
		if strings.ContainsAny(p, "*?[") && strings.HasSuffix(p, ".md") {
			return p
		}
	}
	return ""
}
