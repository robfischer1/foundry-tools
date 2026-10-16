package main

import (
	"testing"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/fluxpin"
)

const (
	laneContract = "version = \"1\"\nstatus = \"approved\"\nverbs = [\"neighbors\"]\n"
	themisRoster = `{"name":"themis","verb_prefix":"themis"}`
	uraniaRoster = `{"name":"urania","verb_prefix":"urania"}`
)

// narcissus as flux pins it live.
const (
	narcDigest = "sha256:0774e298540cdf764d5ed20e57241db84ea1af9c09f8aac16219bed017a05921"
	fluxPins   = "apiVersion: kustomize.config.k8s.io/v1alpha1\nkind: Component\nimages:\n" +
		"  - name: " + NarcissusImage + "\n    newTag: 1791061288-a2c1393 # {\"$imagepolicy\": \"flux-system:narcissus:tag\"}\n" +
		"    digest: " + narcDigest + " # {\"$imagepolicy\": \"flux-system:narcissus:digest\"}\n"
)

// laneDies is foundry-dies at main: one contract and its two stars.
func laneDies(extra map[string]string) map[string]string {
	tree := map[string]string{
		"/dies/orbits/urania-themis.toml": laneContract,
		"/dies/orbits/README.md":          "not a contract",
		"/dies/fleet/repos/urania.json":   uraniaRoster,
		"/dies/fleet/repos/themis.json":   themisRoster,
		"/dies/fleet/repos/broken.json":   "not json",
		"/flux/" + fluxpin.File:           fluxPins,
	}
	for k, v := range extra {
		tree[k] = v
	}
	return tree
}

func TestOrbitAtomsAreTheOrbitStageOnly(t *testing.T) {
	for _, id := range []string{"orbit:contracts", "orbit:surface", "orbit:sidecars", "orbit:repo"} {
		a := checks.AtomByID(id)
		if a.Stage != checks.StageOrbit {
			t.Errorf("%s stage %q", id, a.Stage)
		}
		for _, p := range checks.PullPathAtoms() {
			if p.ID == id {
				t.Errorf("%s is in the gate's vector", id)
			}
		}
	}
	if laneOf(checks.StageOrbit) != "orbit" {
		t.Errorf("the orbit stage's record is labelled %q", laneOf(checks.StageOrbit))
	}
}

func TestStarSetIsTheRostersNamesOrNilForNone(t *testing.T) {
	if starSet(nil) != nil || starSet(map[string]string{}) != nil {
		t.Error("an empty roster must be nil, the two-part rule alone")
	}
	got := starSet(map[string]string{"blade-runner": "blade", "themis": ""})
	if len(got) != 2 || !got["blade-runner"] || !got["themis"] {
		t.Errorf("got %v", got)
	}
}
