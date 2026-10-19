package atomkit

import (
	"reflect"
	"strings"
	"testing"
)

const toolsOK = `schema = "tools@1"

[base]
go = "golang:1.26.9-bookworm@sha256:` + "4444444444444444444444444444444444444444444444444444444444444444" + `"

[toolchain.go]
image = "golang:1.26.9-bookworm@sha256:` + "4444444444444444444444444444444444444444444444444444444444444444" + `"
preflight = ["go", "mod", "download"]

[toolchain.python]
image = "python:3.14-slim-bookworm@sha256:` + "5555555555555555555555555555555555555555555555555555555555555555" + `"

[service.postgres]
image = "postgres:17@sha256:` + "8888888888888888888888888888888888888888888888888888888888888888" + `"

[tool.kubeconform]
kind = "url"
spec = "https://example.test/kubeconform.tar.gz"
version = "0.7.0"
sha256 = "` + "9999999999999999999999999999999999999999999999999999999999999999" + `"

[tool.trivy]
kind = "image"
image = "ghcr.io/aquasecurity/trivy@sha256:` + "7777777777777777777777777777777777777777777777777777777777777777" + `"

[tool.staticcheck]
kind = "go"
spec = "honnef.co/go/tools/cmd/staticcheck"
version = "2026.1"
`

func TestUT_616_ParseTools(t *testing.T) {
	got, err := ParseTools([]byte(toolsOK))
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != "tools@1" || got.Base.Go == "" {
		t.Fatalf("%+v", got)
	}
	if len(got.Toolchains) != 2 || len(got.Services) != 1 || len(got.Tools) != 3 {
		t.Fatalf("maps: %+v", got)
	}
	k := got.Tools["kubeconform"]
	if k.Kind != "url" || k.Spec == "" || k.Version != "0.7.0" || len(k.SHA256) != 64 || k.Name != "kubeconform" {
		t.Fatalf("kubeconform: %+v", k)
	}
	if got.Tools["trivy"].Kind != "image" || got.Tools["trivy"].Image == "" {
		t.Fatalf("trivy: %+v", got.Tools["trivy"])
	}
	if s := got.Tools["staticcheck"]; s.Kind != "go" || s.Spec == "" || s.Version != "2026.1" {
		t.Fatalf("staticcheck: %+v", s)
	}
}

func TestIT_175_176_ToolsErrors(t *testing.T) {
	d64 := strings.Repeat("c", 64)
	for _, c := range []struct{ row, src, want string }{
		{"UT-617", "schema = \"tools@1\"\n[toolchain.go]\nimage = \"golang:1.26\"\n", "toolchain.go: image golang:1.26 is not pinned by digest"},
		{"UT-617", "schema = \"tools@1\"\n[base]\ngo = \"golang:1.26\"\n", "base.go: image golang:1.26 is not pinned by digest"},
		{"FN-108 · 2", "schema = \"tools@1\"\n[service.pg]\nimage = \"pg:17\"\n", "service.pg: image pg:17 is not pinned by digest"},
		{"FN-108 · 2", "schema = \"tools@1\"\n[tool.t]\nkind = \"image\"\nimage = \"t:1\"\n", "tool.t: image t:1 is not pinned by digest"},
		{"UT-618", "schema = \"tools@1\"\n[tool.kubeconform]\nkind = \"url\"\nspec = \"https://x\"\n", "tool.kubeconform: url tool needs sha256"},
		{"UT-618", "schema = \"tools@1\"\n[tool.k]\nkind = \"url\"\nspec = \"https://x\"\nsha256 = \"abc\"\n", "tool.k: url tool needs sha256"},
		{"UT-619", "schema = \"tools@1\"\n[tool.x]\nkind = \"apt\"\n", "tool.x: kind apt is not go, uv, url or image"},
		{"UT-619", "schema = \"tools@1\"\n[tool.trivy]\nkind = \"image\"\n", "tool.trivy: image tool needs image"},
		{"UT-619", "schema = \"tools@1\"\n[tool.staticcheck]\nkind = \"go\"\nspec = \"x\"\n", "tool.staticcheck: go tool needs spec and version"},
		{"UT-619", "schema = \"tools@1\"\n[tool.u]\nkind = \"uv\"\nversion = \"1\"\n", "tool.u: uv tool needs spec and version"},
		{"UT-620", "[base]\ngo = \"golang:1@sha256:" + d64 + "\"\n", "schema is missing, want tools@1"},
		{"FN-108 · 1", "schema = \"tools@2\"\n", "schema is tools@2, want tools@1"},
		{"FN-108 · 1", "schema = \"tools@1\"\nzz = 1\n", "unknown key zz"},
		{"FN-108 · 1", "schema = \"tools@1\"\n[toolchain.go]\nimage = \"g@sha256:" + d64 + "\"\nbogus = 1\n", "unknown key toolchain.go.bogus"},
		{"UT-696", "schema = \"tools@1\"\n[toolchain.go]\nimage = \"g@sha256:" + d64 + "\"\npreflight = []\n", "toolchain.go: preflight must be a non-empty list of strings"},
		{"UT-696", "schema = \"tools@1\"\n[toolchain.go]\nimage = \"g@sha256:" + d64 + "\"\npreflight = \"go mod download\"\n", "toolchain.go: preflight must be a non-empty list of strings"},
		{"UT-696", "schema = \"tools@1\"\n[toolchain.go]\nimage = \"g@sha256:" + d64 + "\"\npreflight = [1]\n", "toolchain.go: preflight must be a non-empty list of strings"},
	} {
		_, err := ParseTools([]byte(c.src))
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: want %q, got %v", c.row, c.want, err)
		}
	}
}

func TestUT_695_Preflights(t *testing.T) {
	got, err := ParseTools([]byte(toolsOK))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"go": {"go", "mod", "download"}}
	if !reflect.DeepEqual(got.Preflights, want) {
		t.Fatalf("Preflights = %v", got.Preflights)
	}
}

// DEF-B4-git-node: an image tool names the programs it puts on an atom's PATH
// (bins), so a whole root filesystem (the golang image, for its git) does not
// shadow the container's own tar, ps and sh.
func TestDEF_B4_git_node_ImageToolBins(t *testing.T) {
	const img = "docker.io/library/golang:1.26.9-bookworm@sha256:" + "4444444444444444444444444444444444444444444444444444444444444444"
	pin := func(bins string) string {
		return "schema = \"tools@1\"\n[tool.git]\nkind = \"image\"\nimage = \"" + img + "\"\n" + bins
	}
	f, err := ParseTools([]byte(pin("bins = [\"usr/bin/git\"]\n")))
	if err != nil || !reflect.DeepEqual(f.Tools["git"].Bins, []string{"usr/bin/git"}) {
		t.Fatalf("%+v %v", f.Tools["git"], err)
	}
	for name, bad := range map[string]string{
		"absolute": "bins = [\"/usr/bin/git\"]\n", "parent": "bins = [\"../git\"]\n", "empty": "bins = [\"\"]\n",
		"unclean": "bins = [\"usr//bin/git\"]\n", "dir": "bins = [\"usr/bin/\"]\n",
	} {
		if _, err := ParseTools([]byte(pin(bad))); err == nil || !strings.Contains(err.Error(), "tool.git: bins entry") {
			t.Errorf("%s: %v", name, err)
		}
	}
	url := "schema = \"tools@1\"\n[tool.k]\nkind = \"url\"\nspec = \"https://x.test/k\"\nsha256 = \"" + strings.Repeat("9", 64) + "\"\nbins = [\"k\"]\n"
	if _, err := ParseTools([]byte(url)); err == nil || !strings.Contains(err.Error(), "tool.k: bins belong to an image tool") {
		t.Errorf("a url tool with bins: %v", err)
	}
}
