package checks

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"
)

// THE FACADE OF A VIRTUAL WORKSPACE (monorepo migration, runbook D-B3).
//
// rust:wit-guest and rust:wit-compose read the ROOT Cargo.toml's [features]
// table, and resolve tests/tapes, wit-guest-budgets.toml and the compose script
// beside it. That holds while the repository IS the Rust crate. When a
// repository's root Cargo.toml is a VIRTUAL workspace (a [workspace] and no
// [package], the shape stellar-core takes once the rust kit lives under rust/),
// the root declares no feature at all: both atoms would read an empty table and
// report ABSENT, which is a pass that checked nothing.
//
// So a virtual root asks cargo which member is the facade: the one declaring the
// wit-guest features. A root that is a package is its own facade and nothing
// here runs: every repository the fleet has today takes that path.

// IsVirtualWorkspace reports whether a Cargo.toml is a virtual workspace: it has
// a [workspace] table and no [package] table. Sub-tables ([workspace.package],
// [workspace.dependencies]) do not make a package; only [package] does.
func IsVirtualWorkspace(cargoToml string) bool {
	workspace, pkg := false, false
	for _, line := range strings.Split(cargoToml, "\n") {
		m := sectionHeader.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		switch table := m[1]; {
		case table == "package":
			pkg = true
		case table == "workspace" || strings.HasPrefix(table, "workspace."):
			workspace = true
		}
	}
	return workspace && !pkg
}

// WitGuestWorldsOf is WitGuestWorlds over a list of feature names (cargo
// metadata's keys) instead of a manifest's [features] table: the same rule, one
// implementation.
func WitGuestWorldsOf(features []string) []string {
	var worlds []string
	for _, name := range features {
		if strings.HasPrefix(name, WitGuestPrefix) && len(name) > len(WitGuestPrefix) {
			worlds = append(worlds, name)
		}
	}
	slices.Sort(worlds)
	if len(worlds) == 0 && slices.Contains(features, WitGuestAlias) {
		return []string{WitGuestAlias}
	}
	return worlds
}

// WitFacadeDir answers the directory, relative to the workspace root and
// slash-separated ("" for a member AT the root), of the member that is the wit
// facade, from `cargo metadata --no-deps --format-version 1`. found is false when
// no workspace member declares a wit-guest feature: that is the one answer that
// may be ABSENT, and it is not an error.
//
// SEVERAL MEMBERS MAY DECLARE wit-guest-* (a per-Class crate declares its own
// world; the facade forwards them all). The facade is the member whose worlds
// are a superset of every other candidate's; where none is, the one with the
// most worlds; ties go to the shallowest directory, then the sorted one, so the
// answer never depends on the order cargo printed the packages in.
func WitFacadeDir(metadata []byte) (dir string, found bool, err error) {
	var meta struct {
		Packages []struct {
			ID           string              `json:"id"`
			ManifestPath string              `json:"manifest_path"`
			Features     map[string][]string `json:"features"`
		} `json:"packages"`
		WorkspaceMembers []string `json:"workspace_members"`
		WorkspaceRoot    string   `json:"workspace_root"`
	}
	if err := json.Unmarshal(metadata, &meta); err != nil {
		return "", false, fmt.Errorf("cargo metadata did not parse: %w", err)
	}
	type candidate struct {
		dir    string
		worlds []string
	}
	var cands []candidate
	root := path.Clean(meta.WorkspaceRoot)
	for _, p := range meta.Packages {
		if !slices.Contains(meta.WorkspaceMembers, p.ID) {
			continue
		}
		names := make([]string, 0, len(p.Features))
		for n := range p.Features {
			names = append(names, n)
		}
		worlds := WitGuestWorldsOf(names)
		if len(worlds) == 0 {
			continue
		}
		member := path.Dir(path.Clean(p.ManifestPath))
		rel := ""
		switch {
		case member == root:
		case strings.HasPrefix(member, root+"/"):
			rel = strings.TrimPrefix(member, root+"/")
		default:
			return "", false, fmt.Errorf("cargo metadata: member %s lies outside the workspace root %s", member, root)
		}
		cands = append(cands, candidate{rel, worlds})
	}
	if len(cands) == 0 {
		return "", false, nil
	}
	covers := func(a, b candidate) bool {
		for _, w := range b.worlds {
			if !slices.Contains(a.worlds, w) {
				return false
			}
		}
		return true
	}
	depth := func(d string) int {
		if d == "" {
			return 0
		}
		return strings.Count(d, "/") + 1
	}
	best := cands[0]
	for _, c := range cands[1:] {
		switch {
		case covers(c, best) && !covers(best, c):
			best = c
		case covers(best, c) && !covers(c, best):
		case len(c.worlds) != len(best.worlds):
			if len(c.worlds) > len(best.worlds) {
				best = c
			}
		case depth(c.dir) != depth(best.dir):
			if depth(c.dir) < depth(best.dir) {
				best = c
			}
		case c.dir < best.dir:
			best = c
		}
	}
	return best.dir, true, nil
}
