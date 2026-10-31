package checks

import (
	"strings"
	"testing"
)

const deletePatch = "  - target: {kind: Deployment}\n    patch: |\n      $patch: delete\n      apiVersion: v1\n      kind: X\n      metadata: {name: x}\n"

func TestKubeLintTargetsLintsAPartitionAsItsBase(t *testing.T) {
	tree := map[string]string{
		"prime":           "resources: [a.yaml]\ncomponents: [images]\n",
		"prime-core":      "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - ../prime\npatches:\n" + deletePatch,
		"prime-workloads": "resources: [../prime]\npatches:\n" + deletePatch + deletePatch,
		// Not partitions: a second resource, a non-delete patch, a component,
		// a file reference, a base with no kustomization, no patch at all.
		"two":       "resources: [../prime, ../spire]\npatches:\n" + deletePatch,
		"strategic": "resources: [../prime]\npatches:\n  - target: {kind: Deployment}\n    patch: |\n      apiVersion: apps/v1\n      kind: Deployment\n      metadata: {name: x, labels: {a: b}}\n",
		"comp":      "resources: [../prime]\ncomponents: [../prime/images]\npatches:\n" + deletePatch,
		"file":      "resources: [../prime/a.yaml]\npatches:\n" + deletePatch,
		"orphan":    "resources: [../nowhere]\npatches:\n" + deletePatch,
		"plain":     "resources: [../prime]\n",
		"spire":     "resources: [v.yaml]\n",
	}
	read := func(d string) string { return tree[d] }
	got := KubeLintTargets([]string{"spire", "prime-workloads", "prime-core", "two", "strategic", "comp", "file", "orphan", "plain"}, read)
	want := "comp,file,orphan,plain,prime,spire,strategic,two"
	if strings.Join(got, ",") != want {
		t.Errorf("targets %v, want %s", got, want)
	}
}

func TestKubeLintTargetsKeepsAPathFromAJSON6902Patch(t *testing.T) {
	read := func(d string) string {
		if d == "p" {
			return "resources: [../b]\npatches:\n  - path: del.yaml\n"
		}
		return "resources: [x.yaml]\n"
	}
	if got := KubeLintTargets([]string{"p"}, read); strings.Join(got, ",") != "p" {
		t.Errorf("a patch from a file is not known to be a delete: %v", got)
	}
}
