package checks

import (
	"slices"
	"testing"
)

func TestOrbitSidecarsAreTheStarNamedTomlsSorted(t *testing.T) {
	got := OrbitSidecars([]string{
		"prime/orbits/urania.orbit.toml",
		"prime/orbits/kustomization.yaml",
		"orbit.toml",
		"prime/orbits/.orbit.toml",
		"chaos.orbit.toml",
		"prime/orbits/athena.orbit.toml",
		"prime/orbits/athena.orbit.toml.bak",
	})
	want := []string{"chaos.orbit.toml", "prime/orbits/athena.orbit.toml", "prime/orbits/urania.orbit.toml"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if OrbitSidecars([]string{"README.md"}) != nil {
		t.Error("a tree with none answered some")
	}
}
