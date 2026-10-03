package bundlelane

import (
	"regexp"
	"slices"
)

// THE TWO ORBIT DIES (Rob, 2026-10-03: "the deleted contracts/ die was for the
// per producer-consumer.toml seams, with the orbits.toml containing a ref to
// the contract details"). Both are built from foundry-dies/orbits on the tip
// that landed, so every contract merge republishes them and neither is a
// committed copy anyone could hand-edit:
//
//	foundry.notusmi.com/data/contracts   the seam contracts, byte for byte
//	foundry.notusmi.com/data/orbits      each star's reference orbit.toml +
//	                                     orbits.json, the whole graph
//
// THEY KEEP THE SHAPE data/orbits WAS FIRST CAST IN (layer_cast, 2026-08-01):
// artifact type application/vnd.hephaestus.die.v1, one octet-stream layer per
// file titled by its name, the source commit on the manifest. A reader that
// pulled the frozen die reads these the same way.
const (
	ContractsDie = "foundry.notusmi.com/data/contracts"
	OrbitsDie    = "foundry.notusmi.com/data/orbits"
	// DieArtifactType is the artifact type hephaestus casts a tree die under.
	DieArtifactType = "application/vnd.hephaestus.die.v1"
	// DieLayerType is each file's layer media type.
	DieLayerType = "application/octet-stream"
	// SourceShaAnnotation names the commit a die was built from.
	SourceShaAnnotation = "org.notusmi.die.source-sha"
)

// orbitsMatch is the contracts directory: what both orbit dies are built from.
var orbitsMatch = regexp.MustCompile(`^orbits/`)

// OrbitsPublish answers whether a landing republishes the orbit dies: it
// touched orbits/, or the previous tip could not be read (no paths), and
// cannot-tell publishes, as Publishes does.
func OrbitsPublish(changed []string) bool {
	if len(changed) == 0 {
		return true
	}
	return slices.ContainsFunc(changed, orbitsMatch.MatchString)
}

// TreePushArgs is the oras argv that pushes a tree die's files, from the
// directory holding them, under ref: one layer per file, in name order, each
// typed DieLayerType, and the source commit annotated twice — the die
// plane's own key and OCI's revision, which the fleet's other dies carry.
func TreePushArgs(ref, sha string, files []string) []string {
	names := slices.Clone(files)
	slices.Sort(names)
	args := []string{"oras", "push", "--registry-config", "/run/docker/config.json", ref,
		"--artifact-type", DieArtifactType,
		"--annotation", SourceShaAnnotation + "=" + sha,
		"--annotation", "org.opencontainers.image.revision=" + sha}
	for _, n := range names {
		args = append(args, n+":"+DieLayerType)
	}
	return args
}
