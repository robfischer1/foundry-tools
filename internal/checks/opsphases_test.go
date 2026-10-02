package checks

import (
	"errors"
	"strings"
	"testing"
)

func TestOpsSettle(t *testing.T) {
	if state, line := OpsSettle("shell", 0, "dial tcp: connection refused"); state != 0 || line != "" {
		t.Errorf("a pass is a pass whatever it printed: (%d, %q)", state, line)
	}
	if state, line := OpsSettle("flux", 1, "Error: FAILED TO FETCH github.com/x"); state != 2 ||
		line != "flux failed on a fault of the substrate (rc=1): FAILED TO FETCH — did not look; run it again" {
		t.Errorf("a fault, any case: (%d, %q)", state, line)
	}
	if state, line := OpsSettle("ansible", 4, "syntax error"); state != 1 || line != "ansible failed (rc=4) — findings" {
		t.Errorf("findings: (%d, %q)", state, line)
	}
}

func TestOpsTracked(t *testing.T) {
	ls := "100644 " + strings.Repeat("a", 40) + " 0\tflux/a b.yaml\x00100755 " + strings.Repeat("b", 40) + " 0\ttools/dup-check\x00garbage\x00"
	files := OpsTracked(ls)
	if len(files) != 2 || files[0] != (OpsFile{"flux/a b.yaml", "100644"}) || files[1] != (OpsFile{"tools/dup-check", "100755"}) {
		t.Errorf("tracked %+v", files)
	}
	if !OpsHasTool(files, "dup-check", true) || OpsHasTool([]OpsFile{{"tools/dup-check", "100644"}}, "dup-check", true) ||
		!OpsHasTool([]OpsFile{{"tools/dup-check", "100644"}}, "dup-check", false) || OpsHasTool(files, "yaml-strict", false) {
		t.Error("OpsHasTool")
	}
}

func TestOpsShellFiles(t *testing.T) {
	files := []OpsFile{
		{"a.sh", ""}, {"frag.bash", ""}, {"bin/tool", ""}, {"bin/dash", ""}, {"bin/plain-sh", ""}, {"bin/env-sh", ""},
		{"bin/py", ""}, {"bin/zshy", ""}, {"x.zsh", ""}, {"y.sh.tmpl", ""}, {"README", ""}, {"bin/shx", ""},
	}
	lines := OpsFirstLines(strings.Join([]string{
		"a.sh\x001\x00#!/bin/bash",
		"bin/tool\x001\x00#!/usr/bin/env bash",
		"bin/dash\x001\x00#!/bin/dash -e",
		"bin/plain-sh\x001\x00#!/bin/sh",
		"bin/env-sh\x001\x00#!/usr/bin/env  sh",
		"bin/py\x001\x00#!/usr/bin/env python3",
		"bin/zshy\x001\x00#!/usr/bin/env zsh",
		"x.zsh\x001\x00#!/bin/zsh",
		"y.sh.tmpl\x001\x00#!/bin/bash",
		"bin/shx\x001\x00#!/bin/shx",
		"README\x003\x00#!/bin/sh is how it starts",
		"malformed",
	}, "\n"))
	if lines["README"] != "" || lines["a.sh"] != "#!/bin/bash" {
		t.Errorf("first lines %v", lines)
	}
	shebang, fragments := OpsShellFiles(files, lines)
	if strings.Join(shebang, ",") != "a.sh,bin/tool,bin/dash,bin/plain-sh,bin/env-sh" {
		t.Errorf("shebang %v", shebang)
	}
	if strings.Join(fragments, ",") != "frag.bash" {
		t.Errorf("fragments %v", fragments)
	}
}

func TestOpsCounts(t *testing.T) {
	if n := OpsDebt("a.sh:1:1: warning: x\n\nno colon\nb.sh:2:3: note: y\n"); n != 2 {
		t.Errorf("OpsDebt %d", n)
	}
	if n := OpsAnsibleDebt("yaml[truthy]: a\nname[missing]: b\nrisky-file-permissions: c\nWARNING: d\n  yaml: e\nNot-a: f\n"); n != 3 {
		t.Errorf("OpsAnsibleDebt %d", n)
	}
	if n := OpsKinds("kind: A\n  kind: nested\n---\nkind: B\nkinds: no"); n != 2 {
		t.Errorf("OpsKinds %d", n)
	}
}

func TestOpsPlaybooksAndChezmoi(t *testing.T) {
	files := []OpsFile{{"ansible/playbooks/b.yml", ""}, {"ansible/playbooks/a.yml", ""}, {"ansible/playbooks/x/c.yml", ""},
		{"ansible/playbooks/d.yaml", ""}, {"ansible/site.yml", ""}}
	if got := strings.Join(OpsPlaybooks(files), ","); got != "playbooks/a.yml,playbooks/b.yml" {
		t.Errorf("playbooks %s", got)
	}
	for name, c := range map[string]struct {
		files []OpsFile
		want  string
	}{
		"chezmoiignore":  {[]OpsFile{{".chezmoiignore", ""}, {"a.tmpl", ""}, {"x/b.tmpl", ""}, {"c.txt", ""}}, "a.tmpl,x/b.tmpl"},
		"chezmoiroot":    {[]OpsFile{{".chezmoiroot", ""}, {"a.tmpl", ""}}, "a.tmpl"},
		"chezmoiversion": {[]OpsFile{{".chezmoiversion", ""}, {"a.tmpl", ""}}, "a.tmpl"},
		"dot_ naming":    {[]OpsFile{{"dot_config/x", ""}, {"dot_bashrc.tmpl", ""}}, "dot_bashrc.tmpl"},
		"not a source":   {[]OpsFile{{"templates/a.tmpl", ""}, {"sub/dot_x", ""}}, ""},
		"no template":    {[]OpsFile{{".chezmoiroot", ""}}, ""},
	} {
		if got := strings.Join(OpsChezmoiTemplates(c.files), ","); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	if got := strings.Join(OpsYAMLFiles([]OpsFile{{"a.yml", ""}, {"b.yaml", ""}, {"c.yaml.j2", ""}, {"yaml", ""}}), ","); got != "a.yml,b.yaml" {
		t.Errorf("yaml files %s", got)
	}
}

func TestOpsStrictYAML(t *testing.T) {
	for src, bad := range map[string]bool{
		"a: 1\nb: 2\n":                     false,
		"a: 1\n---\na: 2\n":                false, // each document its own
		"a: !include x.yaml\n":             false, // a custom tag is not a fact about the YAML
		"":                                 false,
		"a: 1\na: 2\n":                     true,
		"a:\n  b: 1\n  b: 2\n":             true,
		"ok: 1\n---\nx: 1\nx: 2\n":         true, // the second document
		"a: [1\n":                          true,
		"x: 1\n---\n- [unclosed\n---\nz\n": true,
	} {
		if err := OpsStrictYAML(src); (err != nil) != bad {
			t.Errorf("OpsStrictYAML(%q) = %v, want bad %v", src, err, bad)
		}
	}
	report, bad := OpsYAMLReport([]string{"a.yaml", "b.yaml", "c.yaml"}, map[string]error{
		"b.yaml": errors.New("yaml: unmarshal errors:\n  line 2: mapping key \"x\" already defined at line 1"),
	})
	if !bad || report != "b.yaml: yaml: unmarshal errors: line 2: mapping key \"x\" already defined at line 1\n3 yaml file(s), 1 with a duplicate key or a parse error\n" {
		t.Errorf("report %q %v", report, bad)
	}
	if report, bad := OpsYAMLReport([]string{"a.yaml"}, map[string]error{}); bad || report != "1 yaml file(s), 0 with a duplicate key or a parse error\n" {
		t.Errorf("clean report %q %v", report, bad)
	}
}

func TestOpsFluxPaths(t *testing.T) {
	paths, problems := OpsFluxPaths(map[string]string{
		"flux/clusters/home/b.yaml": "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nspec:\n  path: ././flux/apps/\n" +
			"---\napiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nspec:\n  path: ./not-flux\n" +
			"---\napiVersion: source.toolkit.fluxcd.io/v1\nkind: GitRepository\nspec:\n  path: ./nope\n" +
			"---\napiVersion: kustomize.toolkit.fluxcd.io/v1beta2\nkind: Kustomization\nspec:\n  path: /\n",
		"flux/clusters/home/a.yaml": "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nspec:\n  path: flux/infrastructure\n" +
			"---\napiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nspec:\n  path: ./flux/apps\n",
		"flux/clusters/home/c.yaml": "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nspec:\n  path: ./flux/late\n---\n: [bad\n---\nkind: Kustomization\napiVersion: kustomize.toolkit.fluxcd.io/v1\nspec: {path: ./never}\n",
	})
	if strings.Join(paths, ",") != "flux/apps,flux/infrastructure,flux/late" {
		t.Errorf("paths %v", paths)
	}
	if len(problems) != 1 || !strings.HasPrefix(problems[0], "flux/clusters/home/c.yaml: ") {
		t.Errorf("problems %v", problems)
	}
	files := []OpsFile{{"flux/b/kustomization.yaml", ""}, {"flux/a/kustomization.yaml", ""}, {"flux/a/x/kustomization.yaml", ""},
		{"flux/kustomization.yaml", ""}, {"other/c/kustomization.yaml", ""}, {"flux/clusters/home/x.yaml", ""},
		{"flux/clusters/home/deep/y.yaml", ""}, {"flux/clusters/home/z.yml", ""}, {"flux/apps/w.yaml", ""}}
	if got := strings.Join(OpsFluxFallback(files, "flux/"), ","); got != "flux/a,flux/b" {
		t.Errorf("fallback %s", got)
	}
	if got := strings.Join(OpsFluxClusterManifests(files, "flux/"), ","); got != "flux/clusters/home/x.yaml,flux/clusters/home/deep/y.yaml" {
		t.Errorf("cluster manifests %s", got)
	}
}

// A Kustomization pointing at a GitRepository DECLARED IN THIS TREE carries a
// path into that OTHER repository, and building it here is meaningless.
// Measured on infra 2026-09-25: a second source for foundry/casper-stacks with
// `path: ./flux` made the atom run `kustomize build flux` against infra, which
// has no kustomization.yaml there, and report findings for a tree that builds
// fine in the repository that owns it.
func TestOpsFluxPathsSkipsAForeignSourceRef(t *testing.T) {
	local := "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nmetadata:\n  name: apps\nspec:\n  path: ./flux/apps\n  sourceRef:\n    kind: GitRepository\n    name: flux-system\n"
	foreignSrc := "apiVersion: source.toolkit.fluxcd.io/v1\nkind: GitRepository\nmetadata:\n  name: casper-stacks\nspec:\n  url: http://ourea.prime.svc.cluster.local:8215/casper-stacks.git\n"
	foreignKus := "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nmetadata:\n  name: casper-stacks\nspec:\n  path: ./flux\n  sourceRef:\n    kind: GitRepository\n    name: casper-stacks\n"

	paths, problems := OpsFluxPaths(map[string]string{
		"flux/clusters/home/a.yaml": local,
		"flux/clusters/home/b.yaml": foreignSrc + "---\n" + foreignKus,
	})
	if got := strings.Join(paths, ","); got != "flux/apps" {
		t.Errorf("a foreign sourceRef must not contribute a path: %q", got)
	}
	if len(problems) != 0 {
		t.Errorf("problems %v", problems)
	}

	// THE SOURCE MAY BE DECLARED AFTER the Kustomization that names it —
	// these are ordinary documents in an unordered set of files, so one pass
	// would miss it. Same inputs, opposite file order and split across
	// documents.
	paths, _ = OpsFluxPaths(map[string]string{
		"flux/clusters/home/a.yaml": foreignKus + "---\n" + local,
		"flux/clusters/home/z.yaml": foreignSrc,
	})
	if got := strings.Join(paths, ","); got != "flux/apps" {
		t.Errorf("a source declared later must still be recognised: %q", got)
	}

	// AND THE BOOTSTRAP SOURCE IS NOT DECLARED IN THE TREE, so a Kustomization
	// naming it is local and its path is still built. With no GitRepository
	// documents at all, nothing is foreign.
	paths, _ = OpsFluxPaths(map[string]string{"flux/clusters/home/a.yaml": local})
	if got := strings.Join(paths, ","); got != "flux/apps" {
		t.Errorf("a local sourceRef must still build: %q", got)
	}
}

func TestOpsBatches(t *testing.T) {
	if OpsBatches(nil) != nil {
		t.Error("no files, no batch")
	}
	long := strings.Repeat("x", argvBudget/2)
	got := OpsBatches([]string{"a", long, long, "b"})
	if len(got) != 2 || strings.Join(got[0], ",") != "a,"+long || strings.Join(got[1], ",") != long+",b" {
		t.Errorf("batches %d: %d, %d", len(got), len(got[0]), len(got[len(got)-1]))
	}
	// Exactly at the budget still fits one batch.
	exact := strings.Repeat("y", argvBudget-1)
	if got := OpsBatches([]string{exact}); len(got) != 1 {
		t.Errorf("a file alone is always its own batch")
	}
	if got := OpsBatches([]string{strings.Repeat("y", argvBudget/2-1), strings.Repeat("z", argvBudget/2-1)}); len(got) != 1 {
		t.Errorf("two halves that fit are one batch: %d", len(got))
	}
	// One byte over is two batches, and a file bigger than the budget alone is
	// still one batch, never an empty one before it.
	if got := OpsBatches([]string{strings.Repeat("y", argvBudget/2-1), strings.Repeat("z", argvBudget/2)}); len(got) != 2 {
		t.Errorf("one byte over the budget is two batches: %d", len(got))
	}
	if got := OpsBatches([]string{strings.Repeat("y", argvBudget+10)}); len(got) != 1 || len(got[0]) != 1 {
		t.Errorf("an oversized file is its own batch: %v", len(got))
	}
}

// A repository that IS the Flux tree (foundry/flux since 2026-09-29) keeps
// clusters/ and one directory per Kustomization at its own root. Discovery
// takes root "" and answers the same shapes the flux/ layout did, minus
// clusters/ as a fallback tree (it holds the CRs, not a tree they apply).
func TestOpsFluxDiscoveryAtTheRoot(t *testing.T) {
	files := []OpsFile{{"prime/kustomization.yaml", ""}, {"data/kustomization.yaml", ""},
		{"clusters/kustomization.yaml", ""}, {"clusters/pantheon/kustomization.yaml", ""},
		{"clusters/pantheon/prime.yaml", ""}, {"clusters/pantheon/deep/x.yaml", ""},
		{"prime/sub/kustomization.yaml", ""}, {"docs/notes.yaml", ""}, {"flux.yaml", ""}}
	if got := strings.Join(OpsFluxFallback(files, ""), ","); got != "data,prime" {
		t.Errorf("fallback %s, want data,prime: clusters/ is never a fallback tree at the root", got)
	}
	if got := strings.Join(OpsFluxClusterManifests(files, ""), ","); got != "clusters/kustomization.yaml,clusters/pantheon/kustomization.yaml,clusters/pantheon/prime.yaml,clusters/pantheon/deep/x.yaml" {
		t.Errorf("cluster manifests %s", got)
	}
	// The flux/ layout reads only under flux/: a root-level x/kustomization.yaml
	// is not one of its trees.
	if got := strings.Join(OpsFluxFallback([]OpsFile{{"x/kustomization.yaml", ""}, {"flux/a/kustomization.yaml", ""}}, "flux/"), ","); got != "flux/a" {
		t.Errorf("flux/ layout fallback reads outside flux/: %s", got)
	}
	// The flux/ layout keeps clusters/ as a fallback tree, as before.
	if got := strings.Join(OpsFluxFallback([]OpsFile{{"flux/clusters/kustomization.yaml", ""}}, "flux/"), ","); got != "flux/clusters" {
		t.Errorf("flux/ layout fallback %s", got)
	}
	// Paths from root-layout CRs are already repo-root relative.
	paths, problems := OpsFluxPaths(map[string]string{
		"clusters/pantheon/prime.yaml": "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nspec:\n  path: ./prime\n  sourceRef: {name: flux-system}\n",
	})
	if strings.Join(paths, ",") != "prime" || len(problems) != 0 {
		t.Errorf("paths %v problems %v", paths, problems)
	}
	if got := OpsFiles([]string{"a", "b/c"}); len(got) != 2 || got[1].Path != "b/c" || got[1].Mode != "" {
		t.Errorf("OpsFiles %v", got)
	}
}
