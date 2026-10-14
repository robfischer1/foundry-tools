package checks

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// THE PER-CRATE SCOPE (stellar-core-rust F26a). A Rust workspace split per core
// can grade a pull on the crates it can break instead of on the whole
// workspace: the crates whose files the diff touched, and everything that
// depends on them. WHICH crates that is, is the REPO's declaration, not this
// package's: a repo that scopes carries tools/scope/scope.py (RustScopeScript),
// which names the crates a change reaches and the conformance tests that belong
// to them, because "belongs to" there is a naming convention only the repo
// knows. This file decides the other half, from the diff alone: whether the
// scope is worth asking at all (RustScope), and which crates to ask about.
//
// THE DEFAULT IS THE WHOLE WORKSPACE, and every doubt resolves to it: a file in
// the root package (the facade, the manifest, the lock, the vendored tapes), a
// file no member holds, a workspace of one. A scope that was not sure is a
// partial scan reporting a clean one.

// RustScopeScript is the path whose presence in a tree is the repo's opt-in.
const RustScopeScript = "tools/scope/scope.py"

// rustInert is the files at any depth whose change no cargo command can feel:
// prose and repo furniture. A pull that changes only these (and files in
// members) does not widen the scope to the whole workspace.
//
// THE ASSUMPTION: no Rust source include_str!s a root-owned .md. A member that
// did (`#![doc = include_str!("../../X.md")]`) would make that file load-bearing
// for the crate, and this list would call it inert; such a repo keeps the file
// inside the crate that reads it, where the owner rule already counts it.
func rustInert(file string) bool {
	base := path.Base(file)
	switch {
	case strings.HasSuffix(base, ".md"):
		return true
	case base == "LICENSE" || base == "cosign.pub" || base == "cliff.toml" || base == "CHANGELOG.md":
		return true
	case base == ".gitignore" || base == ".editorconfig" || base == "justfile" || base == "stages.just" || base == "dev.just":
		return true
	case strings.HasPrefix(file, "hooks/") || strings.HasPrefix(file, ".claude/"):
		return true
	}
	return false
}

// RustScope names the workspace members a pull's files belong to, for the
// repo's scope script, or says the pull must be graded whole. metadata is
// `cargo metadata --no-deps --format-version 1`; files are relative to root,
// the directory cargo ran in. The deepest member holding a file owns it
// (RustTouchedMembers' rule); the member AT root owns every file nobody deeper
// holds, and a file it owns is a reason to grade whole, not a crate to name.
// An empty crate list with whole false is a pull that touched nothing cargo
// can feel: there is nothing to grade.
func RustScope(metadata []byte, root string, files []string) (crates []string, whole bool, err error) {
	var meta struct {
		Packages []struct {
			Name         string `json:"name"`
			ID           string `json:"id"`
			ManifestPath string `json:"manifest_path"`
		} `json:"packages"`
		WorkspaceMembers []string `json:"workspace_members"`
	}
	if err := json.Unmarshal(metadata, &meta); err != nil {
		return nil, true, fmt.Errorf("cargo metadata did not parse: %w", err)
	}
	members := map[string]bool{}
	for _, id := range meta.WorkspaceMembers {
		members[id] = true
	}
	byDir := map[string]string{}
	for _, p := range meta.Packages {
		if members[p.ID] {
			byDir[path.Dir(path.Clean(p.ManifestPath))] = p.Name
		}
	}
	root = path.Clean(root)
	if len(byDir) < 2 {
		return nil, true, nil
	}
	if _, ok := byDir[root]; !ok {
		// No member sits at the root: a file there belongs to no crate, so the
		// scope cannot account for it.
		for _, rel := range files {
			if rel != "" && !rustInert(rel) && ownerOf(byDir, root, rel) == "" {
				return nil, true, nil
			}
		}
	}
	touched := map[string]bool{}
	for _, rel := range files {
		if rel == "" {
			continue
		}
		owner := ownerOf(byDir, root, rel)
		switch {
		case owner == "" && rustInert(rel):
		case owner == "":
			return nil, true, nil
		case byDir[root] == owner:
			if rustInert(rel) {
				continue
			}
			return nil, true, nil
		default:
			touched[owner] = true
		}
	}
	for n := range touched {
		crates = append(crates, n)
	}
	sort.Strings(crates)
	return crates, false, nil
}

// ownerOf is the member whose directory is the nearest ancestor of file.
func ownerOf(byDir map[string]string, root, file string) string {
	for d := path.Dir(path.Join(root, file)); ; d = path.Dir(d) {
		if name, ok := byDir[d]; ok {
			return name
		}
		if d == "/" || d == "." || len(d) < len(root) {
			return ""
		}
	}
}

// RustScopeArgs reads the scope script's answer: one cargo argument set per
// line, blank lines dropped. A line is `-p a -p b` or `-p c --test t`; the
// arguments keep their boundaries as the script printed them.
func RustScopeArgs(out string) [][]string {
	var sets [][]string
	for _, ln := range strings.Split(out, "\n") {
		if f := strings.Fields(ln); len(f) > 0 {
			sets = append(sets, f)
		}
	}
	return sets
}
