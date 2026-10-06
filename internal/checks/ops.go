package checks

import (
	"strings"
)

// IsOpsTree reports whether a tracked file list is the shape the ops lane was
// written for: the trees the cluster and the hosts converge to rather than a
// star — flux/, ansible/ playbooks, a chezmoi source, a compose spec, a rego
// policy, or one of infra's own gate tools. It mirrors ci/lib/ops/ops.sh's
// detect, minus the two facets every repository has (yaml, shell) and minus
// tofu: on those alone the lane would gate every star, which it never did.
func IsOpsTree(files []string) bool {
	for _, f := range files {
		switch {
		case strings.HasPrefix(f, "flux/"):
			return true
		case isRootFluxCluster(f):
			return true
		case strings.HasPrefix(f, "ansible/playbooks/") && (strings.HasSuffix(f, ".yml") || strings.HasSuffix(f, ".yaml")):
			return true
		case f == ".chezmoiignore" || f == ".chezmoiroot" || f == ".chezmoiversion" || strings.HasPrefix(f, "dot_"):
			return true
		case strings.HasSuffix(f, ".rego"):
			return true
		case f == "tools/dup-check" || f == "tools/declaration-integrity" || f == "tools/console-specs" || f == "tools/metric-allowlist":
			return true
		}
		if len(ComposeSpecs([]string{f})) > 0 {
			return true
		}
	}
	return false
}

// OpsUVIndex is the index ops.sh's `uv run --with` resolves ansible-core,
// ansible-lint and pyyaml from: pypi.org by its own name, which inside the
// cluster is the fleet's transparent cache once the engine names the
// intercept face, so the gate and the pre-push hook draw the same wheels
// with no fleet address in either.
const OpsUVIndex = "https://pypi.org/simple"

// ChezmoiVersion / ChezmoiURL fetch the chezmoi client ops:chezmoi renders
// templates with, PINNED for the reason every tool on this page is. The bare
// linux-amd64 binary is a release asset, so it arrives the way the compose
// client does: GitHub's own URL, fetched by the engine.
//
// Spelled out in full rather than composed from the version: a `+` in a const
// block is an arithmetic mutation site the mutation lane reads as NOT COVERED
// (2026-09-13, foundry-tools#47), and TestOpsToolPinsAgree holds the two
// lines to one version instead.
const (
	// renovate: datasource=github-releases depName=twpayne/chezmoi extractVersion=^v(?<version>.*)$
	ChezmoiVersion = "2.73.0"
	ChezmoiURL     = "https://github.com/twpayne/chezmoi/releases/download/v" + ChezmoiVersion + "/chezmoi-linux-amd64"
)

// KubectlVersion / KubectlURL fetch the kubectl ops:flux builds Kustomizations
// with — the cluster's own minor (k3s v1.36 serves it; the retired pipeline
// pinned alpine/k8s:1.34.1 and ca-sweep stages the same series).
const (
	// renovate: datasource=github-releases depName=kubernetes/kubernetes extractVersion=^v(?<version>.*)$
	KubectlVersion = "1.37.1"
	KubectlURL     = "https://dl.k8s.io/release/v" + KubectlVersion + "/bin/linux/amd64/kubectl"
)

// FluxRoot answers where a tree's Flux layout starts: "flux/" for the infra
// shape (flux/clusters/<name>/, flux/<dir>/), or "" for a repository that IS
// the Flux tree (clusters/<name>/kustomization.yaml at its own root) — the
// shape foundry/flux took when it left infra on 2026-09-29. ok is false when
// the tree carries neither.
//
// THE ROOT SHAPE WAS INVISIBLE FOR TWO WEEKS. Every flux atom asked for a
// flux/ prefix, so on foundry/flux ops:flux and ops:kube-linter both read
// ABSENT and no Kustomization was built or linted before it reached the
// cluster (measured 2026-10-02 on flux@dce6833: 9 holds, 50 inert, neither
// flux atom among the holds). clusters/<name>/kustomization.yaml is the
// marker because it is the one file only a Flux root carries: a bare
// clusters/ directory of notes would not have it.
func FluxRoot(files []string) (root string, ok bool) {
	for _, f := range files {
		if strings.HasPrefix(f, "flux/") {
			return "flux/", true
		}
	}
	for _, f := range files {
		if isRootFluxCluster(f) {
			return "", true
		}
	}
	return "", false
}

// isRootFluxCluster is clusters/<name>/kustomization.yaml, exactly.
func isRootFluxCluster(f string) bool {
	parts := strings.Split(f, "/")
	return len(parts) == 3 && parts[0] == "clusters" && parts[1] != "" && parts[2] == "kustomization.yaml"
}
