package bundlekit

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/orbitcompose"
)

// The orbit dies' directories in the out directory.
const (
	ContractsDir = "die-contracts"
	OrbitsDir    = "die-orbits"
)

// Orbits composes the two orbit dies from foundry-dies/orbits at the commit that
// landed (bundle_orbits.go orbitPayloads): data/contracts, the seam contracts
// byte for byte, and data/orbits, each star's reference orbit.toml and
// orbits.json. A directory that does not compose is a finding about the tree.
// The payloads are written under the out directory, one die per directory, for
// push-bundle to push when the landing touched orbits/.
func Orbits(ctx context.Context, r Run) *Fault {
	sha, bad := r.Sha(ctx)
	if bad != nil {
		return bad
	}
	changed, bad := r.Changed(ctx, sha)
	if bad != nil {
		return bad
	}
	t := r.In.Tree()
	files := map[string][]byte{}
	for _, p := range t.Glob("orbits/*") {
		body, _ := t.Read(p) // the glob just listed it
		files[path.Base(p)] = []byte(body)
	}
	prefixes, bad := roster(t.Glob, t.Read)
	if bad != nil {
		return bad
	}
	contracts, orbits, err := orbitcompose.Payloads(files, starSet(prefixes))
	if err != nil {
		return Findings("foundry-dies/orbits does not compose, so neither orbit die can be built: %v", err)
	}
	Say("orbits: %d contracts compose to %d files of data/orbits", len(contracts), len(orbits))
	pin, err := bundlelane.Pin(sha)
	if err != nil {
		return CouldNotRun("%v", err)
	}
	publish := bundlelane.OrbitsPublish(changed)
	// A leftover of an earlier run would be pushed with this one; one that cannot
	// be removed fails the writes below.
	_ = os.RemoveAll(filepath.Join(OutDir(), ContractsDir))
	_ = os.RemoveAll(filepath.Join(OutDir(), OrbitsDir))
	for _, d := range []struct {
		name, die, dir string
		files          map[string][]byte
	}{
		{"contracts", bundlelane.ContractsDie, ContractsDir, contracts},
		{"orbits", bundlelane.OrbitsDie, OrbitsDir, orbits},
	} {
		names := make([]string, 0, len(d.files))
		for n, body := range d.files {
			names = append(names, n)
			if err := writeFile(filepath.Join(OutDir(), d.dir), n, string(body)); err != nil {
				return CouldNotRun("%s/%s could not be written: %v", d.dir, n, err)
			}
		}
		slices.Sort(names)
		meta := Meta{Die: d.die, Kind: "tree", Pin: pin, Revision: sha, Sha: sha, Publish: publish, Dir: d.dir, Files: names}
		if err := WriteMeta(d.name, meta); err != nil {
			return CouldNotRun("%v", err)
		}
	}
	return nil
}

// roster answers each repo on the fleet catalog (fleet/repos/<name>.json) and its
// verb prefix: a contract may name any repo the catalog holds.
func roster(glob func(string) []string, read func(string) (string, error)) (map[string]string, *Fault) {
	out := map[string]string{}
	for _, p := range glob("fleet/repos/*.json") {
		body, err := read(p)
		if err != nil {
			return nil, CouldNotRun("the roster beside orbits/ could not be read: %s could not be read: %v", p, err)
		}
		var star struct {
			VerbPrefix string `json:"verb_prefix"`
		}
		// A shard that is not JSON still names a repo by its file name; its
		// prefix is unknown, so no gateway name is attributed through it.
		_ = json.Unmarshal([]byte(body), &star)
		out[strings.TrimSuffix(path.Base(p), ".json")] = star.VerbPrefix
	}
	return out, nil
}

// starSet is a roster as the set of its star names; none at all is nil, the
// two-part file-name rule alone.
func starSet(prefixes map[string]string) map[string]bool {
	if len(prefixes) == 0 {
		return nil
	}
	out := make(map[string]bool, len(prefixes))
	for s := range prefixes {
		out[s] = true
	}
	return out
}
