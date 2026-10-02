package checks

import (
	"strings"
	"testing"
)

func crPaths(crs []fluxCR) string {
	var out []string
	for _, c := range crs {
		out = append(out, c.path)
	}
	return strings.Join(out, ",")
}

// Only a GitRepository of source.toolkit.fluxcd.io with a name makes a
// sourceRef foreign; a HelmRepository, a GitRepository of another group, and
// an unnamed one leave the Kustomizations that point at them applied.
func TestFluxCRsForeignNeedsANamedFluxGitRepository(t *testing.T) {
	ks := func(path, src string) string {
		return "---\napiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nmetadata: {name: " + path + "}\nspec: {path: ./" + path + ", sourceRef: {name: \"" + src + "\"}}\n"
	}
	crs, problems := fluxCRs(map[string]string{
		"a.yaml": "apiVersion: source.toolkit.fluxcd.io/v1\nkind: GitRepository\nmetadata: {name: other}\n" +
			"---\napiVersion: source.toolkit.fluxcd.io/v1\nkind: HelmRepository\nmetadata: {name: charts}\n" +
			"---\napiVersion: example.com/v1\nkind: GitRepository\nmetadata: {name: lookalike}\n" +
			"---\napiVersion: source.toolkit.fluxcd.io/v1\nkind: GitRepository\nmetadata: {}\n",
		"b.yaml": ks("far", "other") + ks("charted", "charts") + ks("alike", "lookalike") + ks("own", ""),
	})
	if len(problems) != 0 || crPaths(crs) != "charted,alike,own" {
		t.Errorf("crs %q problems %q", crPaths(crs), problems)
	}
}

// A document that is not a Kustomization is skipped and the file read on; a
// Kustomization-group document of another kind applies nothing; a file that
// does not parse is named once and the next file is still read.
func TestFluxCRsReadPastOtherDocumentsAndNameABadFile(t *testing.T) {
	crs, problems := fluxCRs(map[string]string{
		"a.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: c}\n" +
			"---\napiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Receiver\nspec: {path: ./not-a-tree}\n" +
			"---\napiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nmetadata: {name: y}\nspec: {path: ./y, targetNamespace: n}\n",
		"b.yaml": ": [bad\n",
		"c.yaml": "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nmetadata: {name: z}\nspec: {path: z/}\n",
	})
	if crPaths(crs) != "y,z" {
		t.Errorf("crs %q", crPaths(crs))
	}
	if len(crs) == 2 && (crs[0] != fluxCR{file: "a.yaml", name: "y", path: "y", targetNamespace: "n"} || crs[1].file != "c.yaml" || crs[1].targetNamespace != "") {
		t.Errorf("crs %+v", crs)
	}
	if len(problems) != 1 || !strings.HasPrefix(problems[0], "b.yaml: ") {
		t.Errorf("problems %q", problems)
	}
}
