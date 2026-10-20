package checks

import (
	"reflect"
	"strings"
	"testing"
)

// MR1: a pinned asset has one source, its own URL, held to its pin.
func TestToolSourcesIsTheAssetsOwnURLAtThePin(t *testing.T) {
	const url = "https://example.invalid/tool.tgz"
	sum := strings.Repeat("ab", 32)
	sources, got, err := ToolSources(url, map[string]string{url: sum})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{url}; !reflect.DeepEqual(sources, want) {
		t.Errorf("sources %v, want only %v", sources, want)
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

// MR1: every pinned asset resolves to its own URL alone, and none to the registry.
func TestEveryPinResolvesToItsOwnURLAlone(t *testing.T) {
	for url, sum := range ToolSHA256 {
		sources, got, err := ToolSources(url, ToolSHA256)
		if err != nil {
			t.Errorf("%s: %v", url, err)
			continue
		}
		if len(sources) != 1 || sources[0] != url || got != sum {
			t.Errorf("%s: sources %v sum %s", url, sources, got)
		}
		if strings.Contains(sources[0], "/foundry/tool/mirror/") {
			t.Errorf("%s resolves to the retired mirror", url)
		}
	}
}

func TestGravityBlobURLIsTheRegistryBlobOfTheSum(t *testing.T) {
	const sum = "db5a9f0bec3976e9fe84ae8e6f4d6afa2409c0fb556d9646f489b44e96afa395"
	if got, want := GravityBlobURL(sum), "https://registry.notusmi.com/v2/foundry/tool/mirror/blobs/sha256:"+sum; got != want {
		t.Errorf("%s, want %s", got, want)
	}
}
