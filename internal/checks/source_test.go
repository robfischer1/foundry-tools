package checks

import (
	"strings"
	"testing"
)

// ONE VOLUME PER URL, STABLE, OPAQUE. The same URL names the same volume on
// every call (or the history is never warm); two URLs never share one (or a
// repository's checkout could be built from another's objects); and the key
// carries no URL punctuation.
func TestSourceCacheKeyIsStablePerURLAndOpaque(t *testing.T) {
	a := SourceCacheKey("http://ourea.prime.svc.cluster.local:8215/infra.git")
	if a != SourceCacheKey("http://ourea.prime.svc.cluster.local:8215/infra.git") {
		t.Fatalf("the same URL must name the same volume")
	}
	if a == SourceCacheKey("http://ourea.prime.svc.cluster.local:8215/chaos.git") {
		t.Fatalf("two repositories must never share a volume: %s", a)
	}
	if !strings.HasPrefix(a, "foundry-source-") || len(a) != len("foundry-source-")+16 {
		t.Errorf("want foundry-source- and 16 hex digits, got %q", a)
	}
	if strings.ContainsAny(a, ":/.") {
		t.Errorf("the key must carry no URL punctuation: %q", a)
	}
	// The digest of the empty string, pinned: a key derived from anything but
	// the URL's own bytes would move this.
	if got := SourceCacheKey(""); got != "foundry-source-e3b0c44298fc1c14" {
		t.Errorf("SourceCacheKey(\"\") = %q", got)
	}
}
