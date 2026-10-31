package checks

import (
	_ "embed"
	"path"
	"regexp"
	"strings"
)

// WHERE A KIT'S SOURCE LIVES, ONE SETTING (monorepo migration, runbook D-B5).
//
// The regen and provenance code reads the Rust kit's source (ts:regen and
// dev:go-regen unpack the core at its recorded commit; dev:go-regen --new
// resolves a tag and writes build-guest.sh and provenance.json) and the Go kit's
// topic constants (fleet:kafka-flows reads them through the door). Each used to
// carry its own copy of the repository's name. They read it from here.
//
// A source is a repository and an optional subdirectory of it. The Rust kit
// moved into rob/stellar-core under rust/ (M2c, the import merge 98ef50d), and new
// Rust work lands only there, so RustKit names the monorepo. The Go kit has
// moved too (2026-10-20, stellar-core's go/): rob/stellar-core-go now 404s, so the
// topic constants fleet:kafka-flows resolves are read from stellar-core's go/.
var (
	// RustKit is the Rust kit: the guest sources ts:regen and dev:go-regen build.
	RustKit = KitSource{Repo: "rob/stellar-core", Subdir: "rust", Moved: rustCommitMap}
	// GoKit is the Go kit: the topic constants fleet:kafka-flows resolves.
	GoKit = KitSource{Repo: "rob/stellar-core", Subdir: "go"}
)

// KitSource is where a kit's files are: the door's owner/name of the repository,
// the directory inside it ("" when the kit is the repository), and the commits of
// the repository the kit used to live in, each translated to its commit here.
type KitSource struct {
	Repo   string
	Subdir string
	Moved  map[string]string
}

// Name is the repository's bare name, the form git's clone URL uses.
func (k KitSource) Name() string { return path.Base(k.Repo) }

// GitURL is what `git fetch` and `git ls-remote` are given: the bare form the
// door serves for every repository (the owner-qualified form 404s for a retired
// one, runbook P12).
func (k KitSource) GitURL() string { return "https://git.notusmi.com/" + k.Name() + ".git" }

// Path is a kit-relative file as the repository names it.
func (k KitSource) Path(file string) string { return path.Join(k.Subdir, file) }

// TagRef is the ref a kit tag lives at. A kit in a subdirectory tags with the
// subdirectory as its prefix (rust/v0.89.0), as Go requires of a module in one
// and as the monorepo's tags continue today's numbers.
func (k KitSource) TagRef(tag string) string {
	if k.Subdir == "" {
		return "refs/tags/" + tag
	}
	return "refs/tags/" + k.Subdir + "/" + tag
}

// Commit is the commit of this repository a recorded commit names. A PIN NAMING
// THE OLD REPOSITORY KEEPS RESOLVING: every provenance.json and build-guest.sh in
// stellar-core-go and -ts records a stellar-core-rust commit, and the import
// rewrote each of those commits to one of stellar-core's (filter-repo, rust/ as
// the subdirectory: the same tree, one directory down). A commit the map does not
// name is already this repository's and answers itself.
func (k KitSource) Commit(commit string) string {
	if c, ok := k.Moved[commit]; ok {
		return c
	}
	return commit
}

// THE COMMIT MAP IS EMBEDDED, NOT FETCHED. It is stellar-core's
// docs/import/rust-commit-map.txt as it landed (4937cffb, sha256 335ad7e2...), and
// it never changes: it maps the commits of stellar-core-rust's main at the import,
// which are fixed. Held here, a regeneration resolves an old pin with no fetch of
// the map and no dependence on stellar-core-rust, which is to be retired, or on the
// map's path in stellar-core staying where it is.
//
//go:embed rustcommitmap.txt
var rustCommitMapText string

var rustCommitMap = parseCommitMap(rustCommitMapText)

var commitMapLine = regexp.MustCompile(`^([0-9a-f]{40})\s+([0-9a-f]{40})$`)

// parseCommitMap reads `<old> <new>` lines; comments and the header are skipped.
func parseCommitMap(text string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(text, "\n") {
		if f := commitMapLine.FindStringSubmatch(strings.TrimSpace(l)); f != nil {
			m[f[1]] = f[2]
		}
	}
	return m
}
