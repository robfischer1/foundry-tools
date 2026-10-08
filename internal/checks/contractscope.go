package checks

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// ContractCopyFiles are what dies:contract-copies fetches from foundry-dies'
// main to grade a vendored copy in another repo: the checker, the one module it
// imports (schema_stamp) and the manifest.
func ContractCopyFiles() []string {
	return []string{"tools/check_contracts.py", "tools/schema_stamp.py", DoorManifest}
}

// ContractTreeNames answers which repos of the manifest THIS checkout is,
// read off the copies it holds rather than a list of names to maintain.
//
// A repo's copies are the paths the manifest declares it carries; a checkout is
// that repo when it carries ALL of them. The first path (sorted) is asked first,
// so a tree that holds no copy costs one lookup per repo, not one per copy.
//
// THE HOLE, NAMED: a star that deletes a vendored copy stops matching and goes
// unscoped, ABSENT. foundry-dies still sees the unreadable copy on its next pull;
// this atom only grades copies a tree holds.
//
// TWO REPOS CAN MATCH one tree (stellar-core's lone wit/aiws-result.wit is a
// subset of stellar-core-rust's set); a repo whose set is a strict subset of
// another candidate's is dropped, so the tree is graded as the larger.
func ContractTreeNames(manifest string, has func(path string) (bool, error)) ([]string, error) {
	var doc struct {
		Contracts map[string]manifestContract `toml:"contracts"`
	}
	if _, err := toml.Decode(manifest, &doc); err != nil {
		return nil, fmt.Errorf("the manifest would not parse: %w", err)
	}
	sets := map[string]map[string]bool{}
	for _, c := range doc.Contracts {
		for _, copy := range c.Copies {
			repo, rok := copy.Source["repo"].(string)
			path, pok := copy.Source["path"].(string)
			if !rok || !pok {
				continue
			}
			name := repo[strings.LastIndex(repo, "/")+1:]
			if sets[name] == nil {
				sets[name] = map[string]bool{}
			}
			sets[name][path] = true
		}
	}
	candidates := map[string][]string{}
	for _, name := range slices.Sorted(maps.Keys(sets)) {
		paths := slices.Sorted(maps.Keys(sets[name]))
		held := true
		for _, p := range paths {
			ok, err := has(p)
			if err != nil {
				return nil, err
			}
			if !ok {
				held = false
				break
			}
		}
		if held {
			candidates[name] = paths
		}
	}
	var names []string
	for _, name := range slices.Sorted(maps.Keys(candidates)) {
		paths := candidates[name]
		dominated := false
		for _, other := range candidates {
			// A strictly larger candidate that holds all of this one's paths.
			if len(other) > len(paths) && isSubset(paths, other) {
				dominated = true
			}
		}
		if !dominated {
			names = append(names, name)
		}
	}
	return names, nil
}

func isSubset(paths, of []string) bool {
	return !slices.ContainsFunc(paths, func(p string) bool { return !slices.Contains(of, p) })
}
