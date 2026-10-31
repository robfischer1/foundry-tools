package checks

import (
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// THE STAR-GRANTS RATCHET. When repo_create stood hecate up, the prime/hecate
// Deployment went live at 23:08Z and grants-hecate did not land until 01:17Z
// (flux d65f8e2f, then d5550c88): for two hours prime's default-deny fence
// dropped every hades call to it (~5.2k drops, daedalus#25923).
//
// A pull that ADDS prime/star-<n>.yaml holding a Deployment must leave the tree
// with a CiliumNetworkPolicy named grants-<n> among prime/'s manifests, or carry
// the opt-out StarNoGrantsAnnotation on that Deployment with its reason.
//
// IT IS A RATCHET. Only a star file the pull adds is graded, so the stars already
// on main without a grants policy (charon, daedalus-green, ouranos, theia, tron)
// stay green; they are not this pull's to fix. The base is the merge base, the
// same resolve ops:immutable uses.

// StarNoGrantsAnnotation is the opt-out a Deployment carries when its star
// genuinely needs no grants-<n> (the value is the reason, and must not be empty).
// No annotation of this kind existed in flux when it was named.
const StarNoGrantsAnnotation = "forge.notusmi.com/no-grants"

var starFile = regexp.MustCompile(`^prime/star-([^/]+)\.ya?ml$`)

// StarFiles answers, from the paths a pull added, the star manifests (prime/
// star-<n>.yaml) with the star's name, sorted by path.
func StarFiles(added []string) (files, names []string) {
	sort.Strings(added)
	for _, f := range added {
		if m := starFile.FindStringSubmatch(f); m != nil {
			files, names = append(files, f), append(names, m[1])
		}
	}
	return files, names
}

// StarGrants grades the added star manifests against prime, every tracked
// prime/ manifest by path. It answers the report and whether any star is
// ungranted. A manifest that does not parse is an error: whether a grants
// policy exists could not be known.
func StarGrants(files, names []string, prime map[string]string) (string, bool, error) {
	grants := map[string]bool{}
	type deploy struct{ name, optOut string }
	deploys := map[string][]deploy{}
	paths := make([]string, 0, len(prime))
	for p := range prime {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		dec := yaml.NewDecoder(strings.NewReader(prime[p]))
		for {
			var doc struct {
				Kind     string `yaml:"kind"`
				Metadata struct {
					Name        string            `yaml:"name"`
					Annotations map[string]string `yaml:"annotations"`
				} `yaml:"metadata"`
			}
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return "", false, fmt.Errorf("%s did not parse: %w", p, err)
			}
			switch doc.Kind {
			case "CiliumNetworkPolicy":
				grants[doc.Metadata.Name] = true
			case "Deployment":
				deploys[p] = append(deploys[p], deploy{doc.Metadata.Name, strings.TrimSpace(doc.Metadata.Annotations[StarNoGrantsAnnotation])})
			}
		}
	}
	var b strings.Builder
	bad, graded := false, 0
	for i, f := range files {
		ds := deploys[f]
		if len(ds) == 0 {
			fmt.Fprintf(&b, "  %s adds no Deployment; not graded\n", f)
			continue
		}
		graded++
		want := "grants-" + names[i]
		reasons := ""
		for _, d := range ds {
			if d.optOut != "" {
				reasons += d.name + `: "` + d.optOut + `" `
			}
		}
		switch {
		case grants[want]:
			fmt.Fprintf(&b, "  %s: %s is in prime/\n", path.Base(f), want)
		case reasons != "":
			fmt.Fprintf(&b, "  %s: opted out of %s by %s (%s)\n", path.Base(f), want, StarNoGrantsAnnotation, strings.TrimSpace(reasons))
		default:
			bad = true
			fmt.Fprintf(&b, "  STAR %s adds a Deployment but prime/ has no CiliumNetworkPolicy %s: the default-deny fence drops every call to it until one lands (hecate, flux d65f8e2f); add %s in this pull, or annotate the Deployment %s: \"<reason>\"\n", f, want, want, StarNoGrantsAnnotation)
		}
	}
	return fmt.Sprintf("Star grants: %d star manifest(s) added, %d with a Deployment graded\n", len(files), graded) + b.String(), bad, nil
}
