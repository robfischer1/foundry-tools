package orbitcompose

import (
	"slices"
	"testing"
)

const twoStarKustomization = `# prime/orbits — MACHINE-OWNED: written whole by foundry-tools orbitcompose.
# One ConfigMap per star, orbit-<star>, key orbit.toml: the star's composed
# orbit sidecar, mounted at /etc/stellar/orbit.toml. Re-run the composer
# against foundry-dies/orbits to change it; never edit this directory by hand.
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: prime
generatorOptions:
  disableNameSuffixHash: true
  labels: {app.kubernetes.io/managed-by: orbitcompose}
configMapGenerator:
  - name: orbit-chaos
    files:
      - orbit.toml=chaos.orbit.toml
  - name: orbit-urania
    files:
      - orbit.toml=urania.orbit.toml
`

func TestKustomizationGeneratesOneConfigMapPerStar(t *testing.T) {
	if got := string(Kustomization("prime", []string{"chaos", "urania"})); got != twoStarKustomization {
		t.Errorf("got:\n%s\nwant:\n%s", got, twoStarKustomization)
	}
}

func TestFilesIsEverySidecarAndTheKustomization(t *testing.T) {
	sidecars := []Sidecar{
		{Star: "chaos", Produces: []Contract{contract("chaos", "urania", "shapes")}},
		{Star: "urania", Consumes: []Contract{contract("chaos", "urania", "shapes")}},
	}
	files := Files("prime", sidecars)
	var got []string
	for name := range files {
		got = append(got, name)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"chaos.orbit.toml", "kustomization.yaml", "urania.orbit.toml"}) {
		t.Fatalf("files %v", got)
	}
	if string(files["chaos.orbit.toml"]) != string(Render(sidecars[0])) {
		t.Error("chaos's file is not its rendered sidecar")
	}
	if string(files["urania.orbit.toml"]) != string(Render(sidecars[1])) {
		t.Error("urania's file is not its rendered sidecar")
	}
	if string(files[KustomizationFile]) != twoStarKustomization {
		t.Errorf("kustomization:\n%s", files[KustomizationFile])
	}
}

func TestOwnedIsSidecarsAndTheMarkedKustomizationOnly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing []byte
		want     bool
	}{
		{"chaos.orbit.toml", []byte("anything"), true},
		{"kustomization.yaml", nil, true},
		{"kustomization.yaml", []byte(OwnerMark + "\nrest"), true},
		{"kustomization.yaml", []byte("# a person wrote this\n"), false},
		{"kustomization.yaml", []byte(OwnerMark), false},
		{"README.md", nil, false},
		{"orbit.toml", nil, false},
	} {
		if got := Owned(tc.name, tc.existing); got != tc.want {
			t.Errorf("Owned(%q, %q) = %v, want %v", tc.name, tc.existing, got, tc.want)
		}
	}
}
