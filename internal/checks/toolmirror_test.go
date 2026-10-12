package checks

import (
	"reflect"
	"strings"
	"testing"
)

func TestToolSourcesIsMirrorThenUpstreamBothAtTheSamePin(t *testing.T) {
	const url = "https://example.invalid/tool.tgz"
	sum := strings.Repeat("ab", 32)
	sources, got, err := ToolSources(url, map[string]string{url: sum})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://registry.notusmi.com/v2/foundry/tool/mirror/blobs/sha256:" + sum, url}
	if !reflect.DeepEqual(sources, want) {
		t.Errorf("sources %v, want mirror then upstream %v", sources, want)
	}
	if got != sum {
		t.Errorf("verified against %q, want the pin %q", got, sum)
	}
}

func TestToolSourcesFailsClosed(t *testing.T) {
	const url = "https://example.invalid/tool.tgz"
	for name, tc := range map[string]struct {
		sums map[string]string
		want string
	}{
		"no pin":          {map[string]string{}, "has no checksum"},
		"short pin":       {map[string]string{url: "abc"}, "not a sha256"},
		"uppercase pin":   {map[string]string{url: strings.Repeat("AB", 32)}, "not a sha256"},
		"path in the pin": {map[string]string{url: strings.Repeat("a", 60) + "/../"}, "not a sha256"},
	} {
		sources, sum, err := ToolSources(url, tc.sums)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want %q", name, err, tc.want)
		}
		if sources != nil || sum != "" {
			t.Errorf("%s: answered sources %v sum %q with the error", name, sources, sum)
		}
	}
}

// Every asset the lane pins is mirrorable: a sha256 pin gives a well-formed blob URL.
func TestEveryPinHasAMirrorURL(t *testing.T) {
	for url, sum := range ToolSHA256 {
		sources, _, err := ToolSources(url, ToolSHA256)
		if err != nil {
			t.Errorf("%s: %v", url, err)
			continue
		}
		if sources[0] != ToolMirrorBlobBase+sum || sources[1] != url {
			t.Errorf("%s: sources %v", url, sources)
		}
	}
}

func TestToolMirrorRepoIsInTheBlobBase(t *testing.T) {
	if !strings.Contains(ToolMirrorBlobBase, "/v2/"+ToolMirrorRepo+"/blobs/sha256:") {
		t.Errorf("repo %q and blob base %q name different repositories", ToolMirrorRepo, ToolMirrorBlobBase)
	}
}
