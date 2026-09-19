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
		"compose":             {"compose.yaml"},
		"compose yml":         {"stacks/compose.yml"},
		"buried in a star":    {"go.mod", "main.go", "flux/x.yaml"},
	}
	for name, files := range yes {
		if !IsOpsTree(files) {
			t.Errorf("%s: IsOpsTree(%v) = false, want true", name, files)
		}
	}
	no := map[string][]string{
		"empty":                       {},
		"a go star":                   {"go.mod", "main.go", "ci/run.sh", "k8s/deploy.yaml"},
		"ansible without playbook":    {"ansible/README.md", "ansible/inventory/hosts.yml"},
		"a playbook that is not yaml": {"ansible/playbooks/notes.md"},
		"flux-named file at root":     {"fluxion.yaml"},
		"dot in the middle":           {"src/dot_x.tmpl"},
		"a tool by another name":      {"tools/other"},
		"rego-ish":                    {"policy/x.rego.md"},
		"chezmoi marker elsewhere":    {"docs/.chezmoiroot"},
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
