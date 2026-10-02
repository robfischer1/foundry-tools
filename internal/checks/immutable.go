package checks

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ImmutableEdits compares one Kustomization path's rendered stream at the
// pull's base and at its head, and answers every edit to a field the API
// server refuses to change on a live object. Flux's dry-run hits that refusal,
// the Kustomization goes Ready=False, and NOTHING else in that path applies
// until someone reverts — measured 2026-10-02, flux #206: a CPU-limit sweep
// touched Job/devpi-provision-3's pod template, foundry froze, and an
// unrelated pull behind it stalled. kubeconform validates schema, not
// mutability against the live object, so nothing in the gate could see it.
//
// STATIC ON PURPOSE. A live server dry-run would catch every refusal, CRD
// webhooks included, but needs a credential that can create and patch every
// kind, and gate pods hold no cluster identity (Rob, 2026-10-02). This knows
// the core rules below and nothing else; a CRD's own immutability is out of
// its reach and says so in the atom's description.
//
// An object only in one render is not an edit (added or removed). An object
// whose head carries kustomize.toolkit.fluxcd.io/force: enabled is skipped:
// Flux deletes and recreates it, which is the sanctioned way to change one.
func ImmutableEdits(base, head string) ([]string, error) {
	b, err := immutableObjects(base)
	if err != nil {
		return nil, fmt.Errorf("base: %w", err)
	}
	h, err := immutableObjects(head)
	if err != nil {
		return nil, fmt.Errorf("head: %w", err)
	}
	keys := make([]string, 0, len(h))
	for key := range h {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out []string
	for _, key := range keys {
		ho := h[key]
		bo, ok := b[key]
		if !ok || forceRecreate(ho) {
			continue
		}
		for _, field := range immutableFields(ho["kind"], bo) {
			if !reflect.DeepEqual(dig(bo, field), dig(ho, field)) {
				out = append(out, key+": "+strings.Join(field, "."))
			}
		}
	}
	return out, nil
}

// immutableFields is the field paths the API server will not change once an
// object of kind exists. base is the live (base) object, for the rules that
// depend on its own state (an immutable ConfigMap, a set clusterIP).
func immutableFields(kind any, base map[string]any) [][]string {
	switch kind {
	case "Job":
		return [][]string{{"spec", "template"}, {"spec", "selector"}}
	case "Deployment", "DaemonSet", "ReplicaSet":
		return [][]string{{"spec", "selector"}}
	case "StatefulSet":
		return [][]string{{"spec", "selector"}, {"spec", "volumeClaimTemplates"}, {"spec", "serviceName"}, {"spec", "podManagementPolicy"}}
	case "RoleBinding", "ClusterRoleBinding":
		return [][]string{{"roleRef"}}
	case "StorageClass":
		return [][]string{{"provisioner"}, {"parameters"}, {"reclaimPolicy"}, {"volumeBindingMode"}}
	case "PersistentVolumeClaim":
		// Everything in spec but the storage request (growth is allowed) and
		// the attributes class (mutable since VolumeAttributesClass).
		return [][]string{{"spec", "accessModes"}, {"spec", "selector"}, {"spec", "storageClassName"},
			{"spec", "volumeMode"}, {"spec", "volumeName"}, {"spec", "dataSource"}, {"spec", "dataSourceRef"}}
	case "Service":
		// A clusterIP the tree pins cannot move; one it leaves unset is the
		// API server's to assign and is not an edit.
		if s, _ := dig(base, []string{"spec", "clusterIP"}).(string); s != "" && s != "None" {
			return [][]string{{"spec", "clusterIP"}}
		}
	case "ConfigMap", "Secret":
		if dig(base, []string{"immutable"}) == true {
			return [][]string{{"data"}, {"binaryData"}, {"stringData"}}
		}
	}
	return nil
}

// immutableObjects reads a rendered stream into kind/namespace/name -> object.
func immutableObjects(stream string) (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	dec := yaml.NewDecoder(bytes.NewReader([]byte(stream)))
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		// A kindless document keys under "" and matches no rule; an unnamed
		// one has no identity to compare.
		kind, _ := doc["kind"].(string)
		name, _ := dig(doc, []string{"metadata", "name"}).(string)
		if name == "" {
			continue
		}
		ns, _ := dig(doc, []string{"metadata", "namespace"}).(string)
		out[kind+"/"+ns+"/"+name] = doc
	}
}

// forceRecreate is the head object asking Flux to delete and recreate it.
func forceRecreate(obj map[string]any) bool {
	v, _ := dig(obj, []string{"metadata", "annotations", "kustomize.toolkit.fluxcd.io/force"}).(string)
	return v == "enabled"
}

// dig walks a path of map keys, answering nil where it runs out.
func dig(obj map[string]any, path []string) any {
	var cur any = obj
	for _, k := range path {
		m, _ := cur.(map[string]any) // indexing a nil map answers nil
		cur = m[k]
	}
	return cur
}
