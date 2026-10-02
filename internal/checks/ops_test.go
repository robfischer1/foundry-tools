package checks

import (
	"strings"
	"testing"
)

// IsOpsTree reads six markers, each sufficient alone, and nothing a star
// carries. The table is in this package because the mutation lane scores a
// package by its own tests: the same cases in package main covered the
// function for the race lane and read NOT COVERED for mutation
// (foundry-tools#47).
func TestIsOpsTreeReadsEachMarkerAlone(t *testing.T) {
	yes := map[string][]string{
		"flux":                {"flux/apps/x.yaml"},
		"ansible playbook":    {"ansible/playbooks/site.yml"},
		"ansible yaml suffix": {"ansible/playbooks/site.yaml"},
		"chezmoiignore":       {".chezmoiignore"},
		"chezmoiroot":         {".chezmoiroot"},
		"chezmoiversion":      {".chezmoiversion"},
		"dotfile":             {"dot_bashrc.tmpl"},
		"rego":                {"policy/x.rego"},
		"dup-check":           {"tools/dup-check"},
		"declaration":         {"tools/declaration-integrity"},
		"console-specs":       {"tools/console-specs"},
		"metric-allowlist":    {"tools/metric-allowlist"},
		"compose":             {"compose.yaml"},
		"compose yml":         {"stacks/compose.yml"},
		"buried in a star":    {"go.mod", "main.go", "flux/x.yaml"},
		"a flux root":         {"clusters/pantheon/kustomization.yaml"},
	}
	for name, files := range yes {
		if !IsOpsTree(files) {
			t.Errorf("%s: IsOpsTree(%v) = false, want true", name, files)
		}
	}
	no := map[string][]string{
		"empty":                        {},
		"a go star":                    {"go.mod", "main.go", "ci/run.sh", "k8s/deploy.yaml"},
		"ansible without playbook":     {"ansible/README.md", "ansible/inventory/hosts.yml"},
		"a playbook that is not yaml":  {"ansible/playbooks/notes.md"},
		"flux-named file at root":      {"fluxion.yaml"},
		"dot in the middle":            {"src/dot_x.tmpl"},
		"a tool by another name":       {"tools/other"},
		"rego-ish":                     {"policy/x.rego.md"},
		"chezmoi marker elsewhere":     {"docs/.chezmoiroot"},
		"clusters without a cluster":   {"clusters/kustomization.yaml"},
		"a cluster file, not the root": {"clusters/pantheon/prime.yaml"},
		"too deep to be a cluster":     {"clusters/pantheon/x/kustomization.yaml"},
		"nested clusters":              {"docs/clusters/pantheon/kustomization.yaml"},
		"not a clusters dir":           {"stacks/pantheon/kustomization.yaml"},
	}
	for name, files := range no {
		if IsOpsTree(files) {
			t.Errorf("%s: IsOpsTree(%v) = true, want false", name, files)
		}
	}
}

// The tool pins are spelled in full; this holds them to one version each so
// a bump that touches one line and not the other is caught.
func TestOpsToolPinsAgree(t *testing.T) {
	if !strings.Contains(ChezmoiURL, "/v"+ChezmoiVersion+"/") || !strings.HasSuffix(ChezmoiURL, "/chezmoi-linux-amd64") {
		t.Errorf("chezmoi pin %q does not carry v%s", ChezmoiURL, ChezmoiVersion)
	}
	if !strings.HasPrefix(ChezmoiURL, "https://github.com/twpayne/chezmoi/releases/download/") {
		t.Errorf("chezmoi must come from its own release URL, not a fleet address: %s", ChezmoiURL)
	}
	if !strings.Contains(KubectlURL, "/v"+KubectlVersion+"/") || !strings.HasSuffix(KubectlURL, "/bin/linux/amd64/kubectl") {
		t.Errorf("kubectl pin %q does not carry v%s", KubectlURL, KubectlVersion)
	}
	if !strings.HasPrefix(KubectlURL, "https://dl.k8s.io/") {
		t.Errorf("kubectl must come from dl.k8s.io: %s", KubectlURL)
	}
}

// FluxRoot answers flux/ for the infra layout, "" for a repository that is the
// Flux tree, and not-ok for neither. flux/ wins when both appear.
func TestFluxRoot(t *testing.T) {
	cases := map[string]struct {
		files []string
		root  string
		ok    bool
	}{
		"infra layout": {[]string{"README.md", "flux/apps/x.yaml"}, "flux/", true},
		"root layout":  {[]string{"prime/x.yaml", "clusters/pantheon/kustomization.yaml"}, "", true},
		"both":         {[]string{"clusters/pantheon/kustomization.yaml", "flux/a.yaml"}, "flux/", true},
		"neither":      {[]string{"go.mod", "clusters/notes.md", "clusters//kustomization.yaml"}, "", false},
		"empty":        {nil, "", false},
	}
	for name, c := range cases {
		root, ok := FluxRoot(c.files)
		if root != c.root || ok != c.ok {
			t.Errorf("%s: FluxRoot = (%q, %v), want (%q, %v)", name, root, ok, c.root, c.ok)
		}
	}
}
