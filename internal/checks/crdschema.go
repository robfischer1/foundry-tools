package checks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// THE CRD PASS'S TWO HALVES, both pure. CRDSchemas turns the CustomResourceDefinitions a
// built tree carries into the JSON schemas kubeconform reads, so the tree is
// graded against the CRD it installs and not a catalogue copy that can lag it
// (datreeio's ObjectStore schema still enumerates compression bzip2|gzip|snappy;
// the CRD flux installs also allows lz4, flux#25022). CRDReport settles
// kubeconform's JSON over the result: an invalid resource is a finding, a kind
// with no schema anywhere is a finding unless it is named in crdNoSchema, and
// anything kubeconform could not read is could-not-run. There is no warn state.

// CRDSchemaFile is where a generated schema lives under the schema directory,
// and the template kubeconform is handed for it (the datree layout).
const CRDSchemaFile = "{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json"

// crdNoSchema names the kinds that have no schema in the built tree or the
// catalogue, each with why that is the tree's truth and not a gap. Keyed
// "apiVersion/Kind". Each is a custom resource whose CRD a HelmRelease's chart
// installs, so no manifest in the tree carries the CRD to generate from and
// the catalogue has no entry. Adding a kind here is a decision; the reason is
// the receipt.
var crdNoSchema = map[string]string{
	"piraeus.io/v1/LinstorCluster":                "CRD installed by the piraeus-operator chart, not carried in the tree; absent from the catalogue",
	"piraeus.io/v1/LinstorNodeConnection":         "CRD installed by the piraeus-operator chart, not carried in the tree; absent from the catalogue",
	"piraeus.io/v1/LinstorSatelliteConfiguration": "CRD installed by the piraeus-operator chart, not carried in the tree; absent from the catalogue",
	"openclaw.rocks/v1alpha1/OpenClawInstance":    "CRD installed by the openclaw-operator chart, not carried in the tree; absent from the catalogue",
}

// CRDSchemas answers the schemas of every CustomResourceDefinition in the
// built streams, path (under the schema directory) to JSON. A stream that does
// not parse is an error: the tree could not be read, not a tree without CRDs.
func CRDSchemas(streams ...string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, stream := range streams {
		dec := yaml.NewDecoder(strings.NewReader(stream))
		for {
			var doc map[string]any
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("a built tree did not parse: %w", err)
			}
			if doc["kind"] != "CustomResourceDefinition" {
				continue
			}
			if err := crdVersions(doc, out); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// crdVersions adds one schema per version of a CRD document.
func crdVersions(doc map[string]any, out map[string][]byte) error {
	spec, _ := doc["spec"].(map[string]any)
	names, _ := spec["names"].(map[string]any)
	group, _ := spec["group"].(string)
	kind, _ := names["kind"].(string)
	versions, _ := spec["versions"].([]any)
	if group == "" || kind == "" {
		return errors.New("a CustomResourceDefinition names no group or kind")
	}
	for _, v := range versions {
		ver, _ := v.(map[string]any)
		name, _ := ver["name"].(string)
		schema, _ := ver["schema"].(map[string]any)
		root, _ := schema["openAPIV3Schema"].(map[string]any)
		if name == "" || root == nil {
			continue
		}
		// A map of strings to JSON-safe values marshals; yaml.v3 decodes
		// string-keyed maps as map[string]any, so this cannot fail on a CRD.
		body, _ := json.Marshal(crdStrict(root))
		out[group+"/"+strings.ToLower(kind)+"_"+name+".json"] = body
	}
	return nil
}

// crdStrict rewrites an OpenAPI v3 schema node for kubeconform -strict, as
// datree's openapi2jsonschema does: an object with named properties refuses
// others unless it says otherwise, int-or-string is the two types, nullable
// admits null.
func crdStrict(node any) any {
	switch n := node.(type) {
	case []any:
		for i := range n {
			n[i] = crdStrict(n[i])
		}
		return n
	case map[string]any:
		for k, v := range n {
			if k == "properties" {
				if props, ok := v.(map[string]any); ok {
					for pk := range props {
						props[pk] = crdStrict(props[pk])
					}
				}
				continue
			}
			n[k] = crdStrict(v)
		}
		if n["x-kubernetes-int-or-string"] == true {
			delete(n, "type")
			n["anyOf"] = []any{map[string]any{"type": "integer"}, map[string]any{"type": "string"}}
		}
		if t, ok := n["type"].(string); ok && n["nullable"] == true {
			n["type"] = []any{t, "null"}
		}
		_, has := n["additionalProperties"]
		if _, props := n["properties"]; props && !has && n["x-kubernetes-preserve-unknown-fields"] != true {
			n["additionalProperties"] = false
		}
		return n
	}
	return node
}

type kubeconformJSON struct {
	Resources []struct {
		Filename string `json:"filename"`
		Kind     string `json:"kind"`
		Name     string `json:"name"`
		Version  string `json:"version"`
		Status   string `json:"status"`
		Msg      string `json:"msg"`
	} `json:"resources"`
}

// CRDReport settles kubeconform's -output json -verbose over the built trees:
// 1 when a resource is invalid or a kind has no schema and is not named in
// crdNoSchema, 2 when kubeconform could not read a schema or the output is not
// its JSON, 0 otherwise. Invalid and unschemaed are findings even beside a
// fault; the report names every one.
func CRDReport(out string) (int, string) {
	var res kubeconformJSON
	if err := json.NewDecoder(bytes.NewReader([]byte(out))).Decode(&res); err != nil {
		return 2, "CRD pass UNANALYZABLE: kubeconform's report was not JSON (" + err.Error() + "): " + strings.TrimSpace(out) + "\n"
	}
	var b strings.Builder
	counts := map[string]int{}
	var findings, faults int
	unschemaed := map[string]int{}
	allowed := map[string]int{}
	for _, r := range res.Resources {
		counts[r.Status]++
		switch r.Status {
		case "statusInvalid":
			findings++
			fmt.Fprintf(&b, "  INVALID %s %s %s: %s\n", r.Filename, r.Kind, r.Name, r.Msg)
		case "statusError":
			faults++
			fmt.Fprintf(&b, "  UNREADABLE %s %s %s: %s\n", r.Filename, r.Kind, r.Name, r.Msg)
		case "statusSkipped":
			if r.Kind == "CustomResourceDefinition" {
				continue
			}
			key := r.Version + "/" + r.Kind
			if _, ok := crdNoSchema[key]; ok {
				allowed[key]++
			} else {
				unschemaed[key]++
			}
		}
	}
	keys := func(m map[string]int) []string {
		ks := make([]string, 0, len(m))
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return ks
	}
	for _, k := range keys(unschemaed) {
		findings++
		fmt.Fprintf(&b, "  NO SCHEMA %s x%d: no CRD for it in the built trees or the catalogue; carry the CRD in the tree, or name the kind with its reason in crdNoSchema\n", k, unschemaed[k])
	}
	for _, k := range keys(allowed) {
		fmt.Fprintf(&b, "  allowed without a schema %s x%d: %s\n", k, allowed[k], crdNoSchema[k])
	}
	head := fmt.Sprintf("CRD pass: %d valid, %d invalid, %d unreadable, %d kind(s) without a schema\n", counts["statusValid"], counts["statusInvalid"], faults, len(unschemaed))
	switch {
	case findings > 0:
		return 1, head + b.String()
	case faults > 0:
		return 2, head + b.String() + "CRD pass UNANALYZABLE: kubeconform could not read a schema; run it again\n"
	}
	return 0, head + b.String()
}
