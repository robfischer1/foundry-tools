package checks

import (
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
)

// THE RUST LANE'S CLOSURES. cargo-mutants tests a mutant with its own
// crate's tests only — measured on cargo-mutants 27.1.0, under both cargo
// test and nextest, on a workspace where only a DEPENDENT crate's test could
// kill the mutant: every mutant MISSED with `-p a` and with `-p a -p b`, every
// one caught with --test-workspace=true (the control). So a crate's unit is
// the crate, and its closure is the crate and every path dependency it builds
// against, transitively. A repository that widens the test scope in its
// mutants config gets one closure for every unit: the whole workspace.

// RustClosures reads `cargo metadata --no-deps --format-version 1`, run at
// root, into each workspace member's closure: its directory and the
// directories of its path dependencies, transitively, relative to root. A
// path dependency outside root is not the tree's and is left out.
func RustClosures(metadata []byte, root string) (map[string][]string, error) {
	var meta struct {
		Packages []struct {
			ManifestPath string `json:"manifest_path"`
			Dependencies []struct {
				Path string `json:"path"`
			} `json:"dependencies"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(metadata, &meta); err != nil {
		return nil, fmt.Errorf("cargo metadata did not parse: %v", err)
	}
	deps := map[string][]string{}
	for _, p := range meta.Packages {
		dir, ok := relTo(root, path.Dir(path.Clean(p.ManifestPath)))
		if !ok {
			continue
		}
		deps[dir] = []string{}
		for _, d := range p.Dependencies {
			if dep, ok := relTo(root, path.Clean(d.Path)); ok {
				deps[dir] = append(deps[dir], dep)
			}
		}
	}
	closures := map[string][]string{}
	for unit := range deps {
		seen := map[string]bool{}
		var visit func(d string)
		visit = func(d string) {
			if seen[d] {
				return
			}
			seen[d] = true
			for _, next := range deps[d] {
				visit(next)
			}
		}
		visit(unit)
		closures[unit] = slices.Sorted(maps.Keys(seen))
	}
	return closures, nil
}

// RustTestScopeWide reports a mutants config that tests a mutant with more
// than its own crate's tests — `test_workspace` or `test_package` — under
// which a unit's closure is the whole workspace.
func RustTestScopeWide(config string) bool {
	return strings.Contains(config, "test_workspace") || strings.Contains(config, "test_package")
}

// Widened answers every unit's closure as the union of all of them.
func Widened(closures map[string][]string) map[string][]string {
	all := map[string]bool{}
	for _, c := range closures {
		for _, d := range c {
			all[d] = true
		}
	}
	union := slices.Sorted(maps.Keys(all))
	wide := map[string][]string{}
	for unit := range closures {
		wide[unit] = union
	}
	return wide
}
