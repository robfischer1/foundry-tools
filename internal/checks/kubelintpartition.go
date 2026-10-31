package checks

import (
	"path"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// A FLUX PATH THAT ONLY DELETES FROM ONE SIBLING TREE LINTS AS THAT TREE
// (ops:kube-linter, flux fx1).
//
// kube-linter resolves its cross-object checks inside one kustomize build: a
// Deployment's serviceAccountName is looked for among the objects of the same
// target. foundry/flux applies prime in two halves, prime-core and
// prime-workloads, each `resources: [../prime]` plus `$patch: delete` patches,
// so that an image pin re-applies only the workloads. Linted as two targets,
// every Deployment reports its ServiceAccount missing, because the
// ServiceAccount is in the other half.
//
// Such a path renders a subset of its base's objects, each unchanged: a delete
// removes an object and touches nothing else. So linting the base finds every
// per-object finding the path has, and resolves cross-object references against
// the whole set the cluster gets. The base replaces the path in the target
// list. Anything else in the kustomization (a second resource, a component, a
// patch that is not a delete, a generator, a transformer field) keeps the path
// as it is.

// KubeLintTargets answers the lint targets for the given Flux paths: each path
// that is a delete-only partition of a single sibling directory is replaced by
// that directory, and the list is sorted and each target named once. read
// answers a directory's kustomization.yaml, or "" when it has none.
func KubeLintTargets(paths []string, read func(dir string) string) []string {
	seen := map[string]bool{}
	for _, p := range paths {
		if base, ok := deleteOnlyBase(p, read(p)); ok && read(base) != "" {
			p = base
		}
		seen[p] = true
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// deleteOnlyBase answers the one directory dir's kustomization reads, when that
// kustomization is resources: [<one ../ dir>] and patches that are all
// `$patch: delete`, and nothing else.
func deleteOnlyBase(dir, src string) (string, bool) {
	if src == "" {
		return "", false
	}
	var k map[string]any
	if yaml.Unmarshal([]byte(src), &k) != nil || k == nil {
		return "", false
	}
	for key := range k {
		switch key {
		case "apiVersion", "kind", "resources", "patches":
		default:
			return "", false
		}
	}
	res, _ := k["resources"].([]any)
	if len(res) != 1 {
		return "", false
	}
	ref, _ := res[0].(string)
	if !strings.HasPrefix(ref, "../") || strings.HasSuffix(ref, ".yaml") || strings.HasSuffix(ref, ".yml") {
		return "", false
	}
	patches, _ := k["patches"].([]any)
	if len(patches) == 0 {
		return "", false
	}
	for _, p := range patches {
		m, _ := p.(map[string]any)
		body, _ := m["patch"].(string)
		if m == nil || m["path"] != nil || body == "" {
			return "", false
		}
		var doc map[string]any
		if yaml.Unmarshal([]byte(body), &doc) != nil || doc["$patch"] != "delete" {
			return "", false
		}
	}
	return path.Clean(path.Join(dir, ref)), true
}
