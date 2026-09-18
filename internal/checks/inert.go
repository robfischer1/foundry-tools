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
// MEASURED, NOT ASSUMED. Every entry was checked against what the fleet's
// tests actually open, 2026-09-18: a git grep of `os.ReadFile|os.Open|
// os.ReadDir|filepath.Glob|os.Stat` and every `//go:embed` across the 33 Go
// stars on main. What they DO read outside their own package — `../../
// Dockerfile` (ananke, blade-runner, cql, hermes, prometheus, nereus),
// `../../nereus.melt`, `plugins/*/plugin.json` (argus), `../../migrations/
// *.sql` (eros), `main.go`, in-package schemas and vectors — is not here, and
// that is why this is an EXCLUDE and not an include: no derivation from
// `go list` could have known about the Dockerfile or the migrations. What
// they never read is root prose, licences, docs/, specs/, the CI directories,
// the hooks, the justfiles, and pre-commit's and copier's files.
//
// ROOT-ANCHORED BY CONSTRUCTION. These are Dagger exclude patterns, and a
// pattern without `**/` matches from the root only: `*.md` drops README.md
// and CHANGELOG.md, never a fixture under some package's testdata/. Nothing
// here may reach into a package directory — InertPathsAreRootAnchored holds
// it, because a `**` slipped in would turn a narrowing into a silent
// fixture deletion.
//
// The mutation and witness atoms do NOT mount this narrowing: they run git
// against the tree, and an excluded file reads as deleted to a working-tree
// diff. They keep the whole tree (run.lane); the compile/vet/lint/test atoms
// take run.laneCode.
var InertPaths = []string{
	"*.md",
	"LICENSE", "LICENSE.*", "NOTICE",
	"docs", "specs",
	".forgejo", ".github", ".gitea",
	"hooks", "justfile", "*.just",
	".pre-commit-config.yaml",
	".copier-answers.yml", ".copier-answers.*.yml",
	".gitignore", ".gitattributes", ".editorconfig",
	".secrets.baseline", ".hadolint.yaml", "cliff.toml", "renovate.json",
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
