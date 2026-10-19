package checks

import "path"

// WHERE A KIT'S SOURCE LIVES, ONE SETTING (monorepo migration, runbook D-B5).
//
// The regen and provenance code reads the Rust kit's source (ts:regen and
// dev:go-regen unpack the core at its recorded commit; dev:go-regen --new
// resolves a tag and writes build-guest.sh and provenance.json) and the Go kit's
// topic constants (fleet:kafka-flows reads them through the door). Each used to
// carry its own copy of the repository's name. They read it from here.
//
// A source is a repository and an optional subdirectory of it. The defaults are
// today's: each kit has a repository of its own and no subdirectory, so every
// URL, ref and path below is byte-identical to the literal it replaced
// (TestKitSourcesDefaultToTodaysRepositories holds that). When the kits move into
// rob/stellar-core, THIS is the edit: RustKit becomes {Repo: "rob/stellar-core",
// Subdir: "rust"} and GoKit {Repo: "rob/stellar-core", Subdir: "go"}, and the
// pinned commits recorded in provenance files become the monorepo's
// (docs/import/rust-commit-map.txt there).
var (
	// RustKit is the Rust kit: the guest sources ts:regen and dev:go-regen build.
	RustKit = KitSource{Repo: "rob/stellar-core-rust"}
	// GoKit is the Go kit: the topic constants fleet:kafka-flows resolves.
	GoKit = KitSource{Repo: "rob/stellar-core-go"}
)

// KitSource is where a kit's files are: the door's owner/name of the repository
// and the directory inside it ("" when the kit is the repository).
type KitSource struct {
	Repo   string
	Subdir string
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

// Tree is the tree-ish whose root is the kit: the commit itself, or the commit's
// subdirectory, which `git archive` unpacks with the kit at its root.
func (k KitSource) Tree(commit string) string {
	if k.Subdir == "" {
		return commit
	}
	return commit + ":" + k.Subdir
}
