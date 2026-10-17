package atomkit

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

func landedTools(t *testing.T) ToolsFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot, "tools.toml"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := ParseTools(raw)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestUT_621_IT_174_TheLandedToolsFile: it parses, holds the key sets of
// IT-174 and exactly the two preflights.
func TestUT_621_IT_174_TheLandedToolsFile(t *testing.T) {
	f := landedTools(t)
	for _, k := range []string{"go", "python", "rust", "rust-wasm", "bun", "node", "renovate"} {
		if f.Toolchains[k] == "" {
			t.Errorf("toolchain %s missing", k)
		}
	}
	svc := make([]string, 0, len(f.Services))
	for k := range f.Services {
		svc = append(svc, k)
	}
	slices.Sort(svc)
	if !slices.Equal(svc, []string{"etcd", "kube-apiserver", "pgvector", "postgres", "redpanda"}) {
		t.Errorf("services %v", svc)
	}
	if f.Base.Go == "" || !strings.Contains(f.Base.Go, "base-images/go") {
		t.Errorf("base go %q", f.Base.Go)
	}
	want := map[string][]string{"go": {"go", "mod", "download"}, "rust": {"cargo", "fetch", "--locked"}}
	if len(f.Preflights) != 2 || !slices.Equal(f.Preflights["go"], want["go"]) || !slices.Equal(f.Preflights["rust"], want["rust"]) {
		t.Errorf("preflights %v", f.Preflights)
	}
	for k, img := range f.Toolchains {
		if !strings.Contains(img, "@sha256:") {
			t.Errorf("toolchain %s: %s", k, img)
		}
	}
}

// urlPins are the tools fetched from a release URL: the URL and version
// constants in internal/checks each is held to, and ToolSHA256's line.
var urlPins = []struct {
	name, url, version string
}{
	{"opa", checks.OpaURL, checks.OpaVersion},
	{"opengrep", checks.OpengrepURL, checks.OpengrepVersion},
	{"hadolint", checks.HadolintURL, checks.HadolintVersion},
	{"kubectl", checks.KubectlURL, checks.KubectlVersion},
	{"chezmoi", checks.ChezmoiURL, checks.ChezmoiVersion},
	{"compose", checks.ComposeURL, checks.ComposeVersion},
	{"wasm-tools", checks.WasmToolsURL, checks.WasmToolsVersion},
	{"wac", checks.WacURL, checks.WacVersion},
	{"just", checks.JustURL, checks.JustVersion},
	{"shellcheck", checks.ShellcheckURL, checks.ShellcheckVersion},
	{"oras", checks.OrasURL, checks.OrasVersion},
	{"cargo-audit", checks.CargoAuditURL, checks.CargoAuditVersion},
	{"cargo-nextest", checks.CargoNextestURL, checks.CargoNextestVersion},
	{"sccache", checks.SccacheURL, checks.SccacheVersion},
	{"python-standalone", checks.PythonStandaloneURL, checks.FleetPythonVersion},
}

// goPins are the `go install` tools, as the module@version constants.
var goPins = map[string]string{
	"staticcheck": checks.StaticcheckModule, "govulncheck": checks.GovulncheckModule,
	"gomutants": checks.GomutantsModule, "mutation-gate": checks.MutationGateModule,
}

// imagePins are the tools that are an image, and the toolchains and services.
var imagePins = map[string]string{
	"uv": checks.ImageUV, "trivy": checks.ImageTrivy, "syft": checks.ImageSyft, "cosign": checks.ImageCosign,
	"signing-tools": checks.ImageSigningTools, "kubeconform": checks.ImageKubeconform,
	"kube-linter": checks.ImageKubeLinter, "static": checks.ImageStatic, "debian": checks.ImageTools,
}

var toolchainPins = map[string]string{
	"go": checks.ImageGo, "python": checks.ImagePython, "rust": checks.ImageRust, "rust-wasm": checks.ImageRustWasm,
	"bun": checks.ImageTS, "node": checks.ImageNode, "renovate": checks.ImageRenovate,
}

var servicePins = map[string]string{
	"postgres": checks.ImagePostgres, "pgvector": checks.ImagePgvector, "redpanda": checks.ImageRedpanda,
	"kube-apiserver": checks.ImageKubeAPIServer, "etcd": checks.ImageEtcd,
}

// TestToolsTomlMatchesGo holds tools.toml equal to the Go constants it was
// written from, in both directions, so a pin moved in one and not the other
// fails here. The constants stay the source until the adapter slice.
func TestToolsTomlMatchesGo(t *testing.T) {
	f := landedTools(t)
	if !mapsEqual(f.Toolchains, toolchainPins) {
		t.Errorf("toolchains %v, want %v", f.Toolchains, toolchainPins)
	}
	if !mapsEqual(f.Services, servicePins) {
		t.Errorf("services %v, want %v", f.Services, servicePins)
	}
	seen := map[string]bool{}
	for _, p := range urlPins {
		seen[p.name] = true
		got := f.Tools[p.name]
		if got.Kind != "url" || got.Spec != p.url || got.Version != p.version || got.SHA256 != checks.ToolSHA256[p.url] {
			t.Errorf("tool %s: %+v, want url %s %s %s", p.name, got, p.url, p.version, checks.ToolSHA256[p.url])
		}
	}
	for name, module := range goPins {
		seen[name] = true
		got := f.Tools[name]
		if got.Kind != "go" || got.Spec+"@"+got.Version != module {
			t.Errorf("tool %s: %+v, want %s", name, got, module)
		}
	}
	for _, u := range checks.PythonLaneTools {
		seen[u.Name] = true
		if got := f.Tools[u.Name]; got.Kind != "uv" || got.Spec != u.Name || got.Version != u.Version {
			t.Errorf("tool %s: %+v, want uv %s", u.Name, got, u.Spec())
		}
	}
	for name, img := range imagePins {
		seen[name] = true
		if got := f.Tools[name]; got.Kind != "image" || got.Image != img {
			t.Errorf("tool %s: %+v, want image %s", name, got, img)
		}
	}
	for name := range f.Tools {
		if !seen[name] {
			t.Errorf("tool %s is in tools.toml and in no Go constant", name)
		}
	}
	all := map[string]bool{}
	for _, img := range f.Toolchains {
		all[img] = true
	}
	for _, img := range f.Services {
		all[img] = true
	}
	for _, tool := range f.Tools {
		all[tool.Image] = true
	}
	for _, img := range checks.LaneImages {
		if !all[img] {
			t.Errorf("checks.LaneImages holds %s, which tools.toml does not", img)
		}
	}
}
