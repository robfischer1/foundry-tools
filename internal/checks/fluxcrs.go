package checks

import (
	"bytes"
	"errors"
	"io"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// fluxCR is one Kustomization CR that applies a tree of this repository:
// the file and name it is declared under, its spec.path made relative, and
// its spec.targetNamespace ("" when it sets none).
type fluxCR struct {
	file, name, path, targetNamespace string
}

// fluxCRs reads every Kustomization CR among the manifests that applies a
// tree of this repository, in file order, and names each file that did not
// parse. OpsFluxPaths and OpsFluxUnscoped both read through it.
func fluxCRs(manifests map[string]string) ([]fluxCR, []string) {
	var crs []fluxCR
	var problems []string
	names := make([]string, 0, len(manifests))
	for n := range manifests {
		names = append(names, n)
	}
	sort.Strings(names)

	// A GitRepository DECLARED IN THIS TREE names some OTHER repository.
	// This tree's own source is the bootstrap one, seeded outside git — on
	// this fleet, k3s writes it from server-manifests/flux-sync.yaml, so it
	// is never a document here. Anything committed under flux/clusters/ is
	// therefore an additional source, and a Kustomization pointing at it
	// carries a path into THAT repository's tree, not this one.
	//
	// MEASURED 2026-09-25 on infra: adding a second source for
	// foundry/casper-stacks with `path: ./flux` made this atom run
	// `kustomize build flux` against infra, which has no kustomization.yaml
	// at flux/, and the atom reported a findings red for a tree that builds
	// perfectly well in the repository that actually owns it.
	//
	// TWO PASSES, because a GitRepository may be declared after the
	// Kustomization that names it — they are ordinary documents in an
	// unordered set of files, and one pass would miss a source declared in
	// a later file or a later document.
	foreign := map[string]bool{}
	for _, name := range names {
		dec := yaml.NewDecoder(bytes.NewReader([]byte(manifests[name])))
		for {
			var doc struct {
				APIVersion string `yaml:"apiVersion"`
				Kind       string `yaml:"kind"`
				Metadata   struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
			}
			// The end of the file or a parse error ends this file's first
			// pass alike: the second pass reports the error, and staying
			// silent here keeps one bad file from being named twice.
			if err := dec.Decode(&doc); err != nil {
				break
			}
			if doc.Kind == "GitRepository" && strings.HasPrefix(doc.APIVersion, "source.toolkit.fluxcd.io") && doc.Metadata.Name != "" {
				foreign[doc.Metadata.Name] = true
			}
		}
	}

	for _, name := range names {
		dec := yaml.NewDecoder(bytes.NewReader([]byte(manifests[name])))
		for {
			var doc struct {
				APIVersion string `yaml:"apiVersion"`
				Kind       string `yaml:"kind"`
				Metadata   struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
				Spec struct {
					Path            string `yaml:"path"`
					TargetNamespace string `yaml:"targetNamespace"`
					SourceRef       struct {
						Name string `yaml:"name"`
					} `yaml:"sourceRef"`
				} `yaml:"spec"`
			}
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				problems = append(problems, name+": "+err.Error())
				break
			}
			if doc.Kind != "Kustomization" || !strings.HasPrefix(doc.APIVersion, "kustomize.toolkit.fluxcd.io") {
				continue
			}
			if foreign[doc.Spec.SourceRef.Name] {
				continue
			}
			p := doc.Spec.Path
			for strings.HasPrefix(p, "./") {
				p = p[2:]
			}
			if p = strings.Trim(p, "/"); p != "" {
				crs = append(crs, fluxCR{file: name, name: doc.Metadata.Name, path: p, targetNamespace: doc.Spec.TargetNamespace})
			}
		}
	}
	return crs, problems
}
