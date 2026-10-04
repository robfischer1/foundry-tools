package checks

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/BurntSushi/toml"
)

// DoorManifest is the contracts manifest dies:contracts reads, in the tree.
const DoorManifest = "contracts/contracts.toml"

// manifestContract is the slice of one manifest entry the probe reads.
type manifestContract struct {
	Copies []struct {
		Source map[string]any `toml:"source"`
	} `toml:"copies"`
}

// DoorProbe is dies:contracts' provisioning probe, in Go: can this run reach
// the door. It was dies_door_probe.py.
//
// A contract copy is fetched from the door, so a checker that cannot reach it
// has not found a drifted contract; it has found nothing. The probe runs first
// and answers 2 so that unreachability reads as CANNOT RUN rather than as the
// checker's finding. Exit 0 is "reachable, or nothing to reach"; 2 is "the door
// is not there".
//
// THE TARGET IS READ OUT OF THE MANIFEST — the first remote copy of the
// first-named contract that has one — never named, so the probe follows whatever the checker will fetch.
// ANY HTTP ANSWER MEANS REACHABLE, a 404 or a 503 included: what the door said
// about one file is the checker's finding to make. Only a request that got no
// answer at all is the probe's.
func DoorProbe(ctx context.Context, manifest string, door Door) (int, string) {
	var doc struct {
		Contracts map[string]manifestContract `toml:"contracts"`
	}
	_, err := toml.Decode(manifest, &doc)
	if err != nil {
		return 0, fmt.Sprintf("could not read the manifest for the reachability probe (%v); letting the checker be the judge", err)
	}

	repo, path, found := firstRemoteCopy(doc.Contracts)
	if !found {
		return 0, "every declared copy is local; the live check needs no network"
	}

	target := repo + ":" + path
	status, _, err := door.Get(ctx, repo, path)
	switch {
	case err != nil:
		return 2, fmt.Sprintf("the door's archive read is unreachable (%s): %v", target, err)
	}
	return 0, fmt.Sprintf("door archive read reachable (%s -> HTTP %d); what it said about the file is the checker's finding to make, not a provisioning failure", target, status)
}

// firstRemoteCopy answers the first copy whose source names both a repo and a
// path, walking the contracts by name so the target is the same on every run.
func firstRemoteCopy(contracts map[string]manifestContract) (repo, path string, found bool) {
	for _, name := range slices.Sorted(maps.Keys(contracts)) {
		for _, c := range contracts[name].Copies {
			r, rok := c.Source["repo"].(string)
			p, pok := c.Source["path"].(string)
			if rok && pok {
				return r, p, true
			}
		}
	}
	return "", "", false
}
