package checks

import (
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// WHICH LINT TARGETS A PULL OWES (ops:kube-linter).
//
// The full tree is the default and the answer to every doubt. A narrower run is
// taken only when the change PROVABLY cannot move a finding outside the targets
// it lints, which holds for exactly two kinds of change:
//
//   - a path no lint target reads (docs, tooling): nothing to lint;
//   - an images-only edit to a Kustomize Component/Kustomization (the pin bumps
//     flux's image automation writes): it rewrites image strings and nothing
//     else, so object identity, labels, selectors, ports, service accounts and
//     PDB selectors - everything the cross-object checks resolve - are what they
//     were. Only the per-object checks (latest-tag) can read it, and they read
//     it in the targets that BUILD the file. That is every target whose
//     kustomization reaches the file's directory, found by following the
//     references, not by the path: prime/images is taken by prime, aiws and blades.
//
// Any other change - a manifest edited, added or deleted, a kustomization that
// holds more than images, the kube-linter config or its justifications - keeps
// the full tree, because a file's findings can depend on objects in other
// targets (the enabled cross-object checks are KubeLinterCrossObject).
// A narrow run is given those checks switched off (KubeLinterConfigWith): run
// over a subset of the objects they would report references that resolve in the
// whole tree as dangling. Their inputs did not change, so neither did they.

// KubeLinterCrossObject is every kube-linter check that resolves a reference
// to, or a selection over, another object (kube-linter v0.8.3 checks list, the
// ones marked as checking another object, plus the RBAC and PDB readers).
var KubeLinterCrossObject = []string{
	"access-to-create-pods", "access-to-secrets",
	"dangling-horizontalpodautoscaler", "dangling-ingress", "dangling-networkpolicy",
	"dangling-networkpolicypeer-podselector", "dangling-service", "dangling-servicemonitor",
	"env-value-from", "hpa-minimum-three-replicas", "invalid-target-ports", "mismatching-selector",
	"non-existent-service-account", "non-isolated-pod",
	"pdb-max-unavailable", "pdb-min-available", "pdb-unhealthy-pod-eviction-policy",
	"priority-class-name",
}

// kubeLinterConfigFiles are the files whose change keeps the full tree.
var kubeLinterConfigFiles = []string{".kube-linter.yaml", ".kube-linter.yml", ".kube-justifications.json"}

// KubeChange is one entry of `git diff --name-status --no-renames`.
type KubeChange struct{ Status, Path string }

// ParseKubeNameStatus reads `git diff -z --name-status --no-renames`: NUL
// separated status and path pairs. A status with no path is a torn record, and
// the second result says so.
func ParseKubeNameStatus(out string) ([]KubeChange, bool) {
	parts := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	if len(parts) == 1 && parts[0] == "" {
		return nil, true
	}
	if len(parts)%2 != 0 {
		return nil, false
	}
	var changes []KubeChange
	for i := 0; i < len(parts); i += 2 {
		changes = append(changes, KubeChange{Status: parts[i], Path: parts[i+1]})
	}
	return changes, true
}

// KubeScopeIn is what the scope decides over. Head and Base read a file at the
// pull's head and at the merge base.
type KubeScopeIn struct {
	Targets, Files []string
	Changes        []KubeChange
	Head, Base     func(string) (string, error)
}

// KubeScope is the decision. Full keeps every target, with Why naming the
// reason; otherwise Targets are the ones to lint, in the order they came, and
// none is nothing to lint.
type KubeScope struct {
	Targets []string
	Full    bool
	Why     string
}

// ScopeKubeLinter decides which of the targets a pull must lint.
func ScopeKubeLinter(in KubeScopeIn) KubeScope {
	full := func(why string) KubeScope { return KubeScope{Targets: in.Targets, Full: true, Why: why} }
	g := newKubeGraph(in.Files, in.Head)
	reach := make([]kubeReach, len(in.Targets))
	for i, t := range in.Targets {
		r, err := g.reach(strings.TrimSuffix(t, "/"))
		if err != nil {
			return full(err.Error())
		}
		reach[i] = r
	}
	affected := map[int]bool{}
	for _, c := range in.Changes {
		if slices.Contains(kubeLinterConfigFiles, path.Base(c.Path)) {
			return full(c.Path + " changed")
		}
		var hit []int
		for i, r := range reach {
			if r.covers(path.Dir(c.Path)) {
				hit = append(hit, i)
			}
		}
		if len(hit) == 0 {
			continue // no target reads it, so no target lints it
		}
		if c.Status != "M" || !inertEdit(c.Path, in.Head, in.Base) {
			return full(c.Path + " changes what the targets build")
		}
		for _, i := range hit {
			affected[i] = true
		}
	}
	var out []string
	for i, t := range in.Targets {
		if affected[i] {
			out = append(out, t)
		}
	}
	return KubeScope{Targets: out}
}

// inertEdit: the file was an images-only Kustomization/Component before and
// is after, so the edit is confined to image overrides.
func inertEdit(p string, head, base func(string) (string, error)) bool {
	if !strings.HasSuffix(p, "/kustomization.yaml") && p != "kustomization.yaml" {
		return false
	}
	for _, read := range []func(string) (string, error){head, base} {
		src, err := read(p)
		if err != nil || !ImagesOnlyKustomization(src) {
			return false
		}
	}
	return true
}

// ImagesOnlyKustomization reports whether src is one YAML document of kind
// Kustomization or Component holding nothing but apiVersion, kind, metadata and
// images.
func ImagesOnlyKustomization(src string) bool {
	dec := yaml.NewDecoder(strings.NewReader(src))
	var doc map[string]any
	if dec.Decode(&doc) != nil {
		return false
	}
	var more any
	if !errors.Is(dec.Decode(&more), io.EOF) {
		return false
	}
	if k, _ := doc["kind"].(string); k != "Kustomization" && k != "Component" {
		return false
	}
	for key := range doc {
		if !slices.Contains([]string{"apiVersion", "kind", "metadata", "images"}, key) {
			return false
		}
	}
	return true
}

// kubeGraph is the tree's files and directories, and a reader for the
// kustomizations that tie them together.
type kubeGraph struct {
	files, dirs map[string]bool
	read        func(string) (string, error)
}

func newKubeGraph(files []string, read func(string) (string, error)) kubeGraph {
	g := kubeGraph{files: map[string]bool{}, dirs: map[string]bool{"": true}, read: read}
	for _, f := range files {
		g.files[f] = true
		for d := path.Dir(f); d != "." && !g.dirs[d]; d = path.Dir(d) {
			g.dirs[d] = true
		}
	}
	return g
}

// kubeReach is what one lint target reads: the directories its kustomizations
// reach (exact), and the directories read whole because they hold no
// kustomization (a plain directory, which kube-linter lints recursively).
type kubeReach struct {
	exact map[string]bool
	plain []string
}

func (r kubeReach) covers(dir string) bool {
	if r.exact[dir] {
		return true
	}
	return slices.ContainsFunc(r.plain, func(p string) bool { return dir == p || strings.HasPrefix(dir, p+"/") })
}

var kustomizationNames = []string{"kustomization.yaml", "kustomization.yml", "Kustomization"}

// reach follows a target's kustomizations. Every string a kustomization holds
// that names a tracked file or directory next to it is a reference - more
// than kustomize reads, which only widens the scope. One that does not parse
// or cannot be read fails the whole decision toward the full tree.
func (g kubeGraph) reach(target string) (kubeReach, error) {
	r := kubeReach{exact: map[string]bool{}}
	var visit func(dir string) error
	visit = func(dir string) error {
		if r.exact[dir] {
			return nil
		}
		r.exact[dir] = true
		var kfile string
		for _, n := range kustomizationNames {
			if g.files[path.Join(dir, n)] {
				kfile = path.Join(dir, n)
				break
			}
		}
		if kfile == "" {
			r.plain = append(r.plain, dir)
			return nil
		}
		src, err := g.read(kfile)
		if err != nil {
			return fmt.Errorf("%s could not be read: %w", kfile, err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
			return fmt.Errorf("%s does not parse: %w", kfile, err)
		}
		for _, s := range yamlScalars(&doc, nil) {
			p := path.Join(dir, s)
			switch {
			case strings.HasPrefix(p, "..") || strings.Contains(s, "://") || p == dir:
			case g.dirs[p]:
				if err := visit(p); err != nil {
					return err
				}
			case g.files[p]:
				r.exact[path.Dir(p)] = true
			}
		}
		return nil
	}
	return r, visit(target)
}

// yamlScalars is every scalar of a node tree.
func yamlScalars(n *yaml.Node, acc []string) []string {
	if n.Kind == yaml.ScalarNode {
		return append(acc, n.Value)
	}
	for _, c := range n.Content {
		acc = yamlScalars(c, acc)
	}
	return acc
}

// KubeLinterConfigWith is src (a kube-linter config; "" for none) with the
// named checks added to checks.exclude. Every other key, and every exclude
// already there, is kept: the flag form would replace the config's list.
func KubeLinterConfigWith(src string, exclude []string) (string, error) {
	var doc map[string]any
	if strings.TrimSpace(src) != "" {
		if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
			return "", fmt.Errorf("the kube-linter config does not parse: %w", err)
		}
	}
	if doc == nil {
		doc = map[string]any{}
	}
	checks, ok := doc["checks"].(map[string]any)
	if v, present := doc["checks"]; present && v != nil && !ok {
		return "", errors.New("the kube-linter config's checks key is not a mapping")
	}
	if checks == nil {
		checks = map[string]any{}
	}
	var list []any
	switch cur := checks["exclude"].(type) {
	case nil:
	case []any:
		list = cur
	default:
		return "", errors.New("the kube-linter config's checks.exclude is not a list")
	}
	for _, e := range exclude {
		if !slices.Contains(list, any(e)) {
			list = append(list, e)
		}
	}
	checks["exclude"] = list
	doc["checks"] = checks
	// doc is what Unmarshal yielded plus strings: Marshal fails only on values
	// (channels, funcs) that neither can hold, so there is no error to branch on.
	out, _ := yaml.Marshal(doc)
	return string(out), nil
}
