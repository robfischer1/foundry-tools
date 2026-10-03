package orbitcompose

import (
	"fmt"
	"strings"
)

// SidecarSuffix names a star's composed sidecar file in the Flux directory.
const SidecarSuffix = ".orbit.toml"

// KustomizationFile is the directory's generated kustomization.
const KustomizationFile = "kustomization.yaml"

// OwnerMark is the first line of every kustomization this package writes. A
// kustomization.yaml without it was written by a person, and the writer
// refuses to replace it — the principle prime/images holds for the image
// automation: the generator owns its file whole and never rewrites one it
// did not write.
const OwnerMark = "# prime/orbits — MACHINE-OWNED: written whole by foundry-tools orbitcompose."

// ConfigMapPrefix names each star's ConfigMap: orbit-<star>.
const ConfigMapPrefix = "orbit-"

// Kustomization renders the directory's kustomization: one ConfigMap per
// star, key orbit.toml, from the star's sidecar file. Names carry no hash
// suffix so each star's manifest can name its ConfigMap; a content change
// still rolls the pod, because every prime Deployment carries
// reloader.stakater.com/auto (prime/kustomization.yaml), and the reloader
// is also what a subPath mount needs, since kubelet never refreshes one.
func Kustomization(namespace string, stars []string) []byte {
	var b strings.Builder
	b.WriteString(OwnerMark + "\n")
	b.WriteString("# One ConfigMap per star, orbit-<star>, key orbit.toml: the star's composed\n")
	b.WriteString("# orbit sidecar, mounted at /etc/stellar/orbit.toml. Re-run the composer\n")
	b.WriteString("# against foundry-dies/orbits to change it; never edit this directory by hand.\n")
	b.WriteString("apiVersion: kustomize.config.k8s.io/v1beta1\n")
	b.WriteString("kind: Kustomization\n")
	fmt.Fprintf(&b, "namespace: %s\n", namespace)
	b.WriteString("generatorOptions:\n")
	b.WriteString("  disableNameSuffixHash: true\n")
	b.WriteString("  labels: {app.kubernetes.io/managed-by: orbitcompose}\n")
	b.WriteString("configMapGenerator:\n")
	for _, s := range stars {
		fmt.Fprintf(&b, "  - name: %s%s\n", ConfigMapPrefix, s)
		b.WriteString("    files:\n")
		fmt.Fprintf(&b, "      - orbit.toml=%s%s\n", s, SidecarSuffix)
	}
	return []byte(b.String())
}

// Files is the whole directory the composer owns, by file name: each star's
// sidecar and the kustomization that mounts them.
func Files(namespace string, sidecars []Sidecar) map[string][]byte {
	files := map[string][]byte{}
	stars := make([]string, len(sidecars))
	for i, s := range sidecars {
		stars[i] = s.Star
		files[s.Star+SidecarSuffix] = Render(s)
	}
	files[KustomizationFile] = Kustomization(namespace, stars)
	return files
}

// Owned reports whether name is a file the composer may write or delete in
// its directory: a sidecar, or a kustomization carrying OwnerMark. existing
// is the file's current bytes, or nil when it is absent.
func Owned(name string, existing []byte) bool {
	if strings.HasSuffix(name, SidecarSuffix) {
		return true
	}
	if name != KustomizationFile {
		return false
	}
	return existing == nil || strings.HasPrefix(string(existing), OwnerMark+"\n")
}
