package checks

import (
	"bytes"
	_ "embed"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// A NAMESPACED OBJECT WITH NO NAMESPACE, UNDER A CR THAT SUPPLIES NONE.
// kustomize-controller applies a rendered tree as one server-side apply, and an
// object it cannot place refuses the WHOLE tree: "CiliumNetworkPolicy/x
// namespace not specified". Measured 2026-10-02, flux #264: five
// CiliumNetworkPolicies in prime/ carried no metadata.namespace, prime's CR
// sets no targetNamespace (foundry's does, and its copy applied fine), and
// prime applied nothing until #269. kustomize builds such a tree without a
// word, and kubeconform validates schema, not placement, so ops:flux passed it.
//
// WHICH KINDS ARE NAMESPACED is not in the manifest. It comes from two places:
// the CRDs the tree itself renders, and kindscope.txt, the fleet API server's
// discovery written down (the kinds charts and k3s install are never in a
// render: #264's own CiliumNetworkPolicy is one). A kind in neither is NOT a
// finding: it is noted as unchecked, because a false red would stop every pull
// over a kind this check simply does not know.
//
// Regenerate kindscope.txt against the cluster when an operator adds kinds:
//
//	{ kubectl get --raw /api/v1 | jq -r '.resources[] | select(.name|contains("/")|not) | "/\(.kind) \(if .namespaced then "n" else "c" end)"'
//	  for gv in $(kubectl get --raw /apis | jq -r '.groups[].versions[].groupVersion'); do
//	    kubectl get --raw /apis/$gv | jq -r --arg g "${gv%/*}" '.resources[] | select(.name|contains("/")|not) | "\($g)/\(.kind) \(if .namespaced then "n" else "c" end)"'
//	  done; } | sort -u   # under the header kept in the file

//go:embed kindscope.txt
var kindScopeData string

// kindScopeCatalog is kindscope.txt read into group/Kind -> namespaced.
func kindScopeCatalog() map[string]bool { return parseKindScopes(kindScopeData) }

// parseKindScopes reads "group/Kind n|c" lines, skipping comments and any
// line that is not exactly two fields.
func parseKindScopes(data string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(data, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if f := strings.Fields(line); len(f) == 2 {
			out[f[0]] = f[1] == "n"
		}
	}
	return out
}

// OpsFluxUnscoped is every tree a Kustomization CR applies with no
// targetNamespace, mapped to the names of those CRs, sorted. A tree applied by
// several CRs is listed if any one of them sets none: that one is the CR whose
// apply fails.
func OpsFluxUnscoped(manifests map[string]string) map[string][]string {
	crs, _ := fluxCRs(manifests)
	out := map[string][]string{}
	for _, cr := range crs {
		if cr.targetNamespace == "" {
			out[cr.path] = append(out[cr.path], cr.name)
		}
	}
	for p := range out {
		sort.Strings(out[p])
	}
	return out
}

// kubeObject is what placement needs from one rendered document.
type kubeObject struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		Group string `yaml:"group"`
		Scope string `yaml:"scope"`
		Names struct {
			Kind string `yaml:"kind"`
		} `yaml:"names"`
	} `yaml:"spec"`
}

// kubeObjects reads a rendered stream, skipping empty documents.
func kubeObjects(stream string) ([]kubeObject, error) {
	var out []kubeObject
	dec := yaml.NewDecoder(bytes.NewReader([]byte(stream)))
	for {
		var o kubeObject
		err := dec.Decode(&o)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if o.Kind != "" {
			out = append(out, o)
		}
	}
}

// groupOf is an apiVersion's group: "" for the core group's bare "v1".
func groupOf(apiVersion string) string {
	if g, _, ok := strings.Cut(apiVersion, "/"); ok {
		return g
	}
	return ""
}

// addCRDScopes overlays scopes with every CRD the rendered stream declares.
func addCRDScopes(scopes map[string]bool, stream string) error {
	objs, err := kubeObjects(stream)
	if err != nil {
		return err
	}
	for _, o := range objs {
		if o.Kind == "CustomResourceDefinition" && o.Spec.Names.Kind != "" {
			scopes[o.Spec.Group+"/"+o.Spec.Names.Kind] = o.Spec.Scope != "Cluster"
		}
	}
	return nil
}

// Namespaceless answers, for one rendered stream, each namespaced object that
// names no namespace (Kind/name), and each group/Kind whose scope is unknown
// and so went unchecked, both in stream order and each named once.
func Namespaceless(stream string, scopes map[string]bool) (missing, unknown []string, err error) {
	objs, err := kubeObjects(stream)
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	for _, o := range objs {
		if o.Metadata.Namespace != "" {
			continue
		}
		key := groupOf(o.APIVersion) + "/" + o.Kind
		namespaced, known := scopes[key]
		if !known {
			if !seen[key] {
				seen[key] = true
				unknown = append(unknown, key)
			}
			continue
		}
		if namespaced {
			missing = append(missing, o.Kind+"/"+o.Metadata.Name)
		}
	}
	return missing, unknown, nil
}

// OpsNamespacelessReport checks each built tree that a CR applies with no
// targetNamespace, and answers ops:flux's lines for it and whether any object
// was missing its namespace. streams maps a tree to what kubectl built for it;
// a tree that did not build is not here and is not checked. A stream that does
// not parse is the error.
func OpsNamespacelessReport(paths []string, streams map[string]string, unscoped map[string][]string) (string, bool, error) {
	// Scopes come from the catalog and every built tree's CRDs, not only the
	// checked trees': a CRD in operators/ scopes a kind used in prime/.
	scopes := kindScopeCatalog()
	for _, p := range paths {
		if stream, built := streams[p]; built {
			if err := addCRDScopes(scopes, stream); err != nil {
				return "", false, errors.New(p + ": " + err.Error())
			}
		}
	}
	var b strings.Builder
	checked, bad := 0, false
	for _, p := range paths {
		crs, ok := unscoped[p]
		stream, built := streams[p]
		if !ok || !built {
			continue
		}
		checked++
		// Every built stream parsed in the CRD scan above, so this cannot fail.
		missing, unknown, _ := Namespaceless(stream, scopes)
		for _, m := range missing {
			bad = true
			b.WriteString(p + ": " + m + " names no namespace, and Kustomization " + strings.Join(crs, ", ") +
				" sets no targetNamespace; kustomize-controller refuses the whole tree (namespace not specified). Give it metadata.namespace.\n")
		}
		for _, u := range unknown {
			b.WriteString(p + ": " + u + " has no known scope (not in kindscope.txt, no CRD in the tree), so its objects went unchecked\n")
		}
	}
	b.WriteString(strconv.Itoa(checked) + " tree(s) applied with no targetNamespace checked for objects that name none\n")
	return b.String(), bad, nil
}
