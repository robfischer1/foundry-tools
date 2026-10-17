package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// failBoth makes BOTH sources of a pinned asset unfetchable, the mirror's blob
// and the asset's own URL: the fetch is then an error, as the lone upstream
// failure was before the mirror.
func failBoth(url, message string) {
	engine.fail(`url:"`+checks.ToolMirrorURL(checks.ToolSHA256[url])+`"`, message)
	engine.fail(`url:"`+url+`"`, message)
}

// failBothLeaf is failBoth narrowed to one leaf of the fetch.
func failBothLeaf(url, leaf, message string) {
	engine.failLeaf(`url:"`+checks.ToolMirrorURL(checks.ToolSHA256[url])+`"`, leaf, message)
	engine.failLeaf(`url:"`+url+`"`, leaf, message)
}

// sumFile is the line toolFile writes for the in-container verification of url:
// it names the checksum and the file, and appears in no fetch's query.
func sumFile(url string) string { return checks.ToolSHA256[url] + "  /tmp/pkg" }

// pinnedFetch answers the newest chain that fetched the pinned asset url from
// the MIRROR (the first source) WITH its sha256 pin and read the named leaf, or "".
func pinnedFetch(url, leaf string) string {
	sum := checks.ToolSHA256[url]
	return engine.chain(`http(`, `url:"`+checks.ToolMirrorURL(sum)+`"`, `checksum:"sha256:`+sum+`"`, leaf)
}

// indexOfFetch is the position, among every recorded query, of the first one
// that fetched src, or -1.
func indexOfFetch(src string) int {
	for i, q := range engine.chains() {
		if strings.Contains(q, `url:"`+src+`"`) {
			return i
		}
	}
	return -1
}

const mirrorTestURL = "https://upstream.example/tool.tgz"

var mirrorTestSums = map[string]string{mirrorTestURL: strings.Repeat("ab", 32)}

func mirrorTestMirror() string { return checks.ToolMirrorURL(mirrorTestSums[mirrorTestURL]) }

// THE MIRROR ANSWERS: upstream is never asked, and the fetch carries the pin.
func TestFetchPinnedToolTakesTheMirrorFirstAndNeverAsksUpstreamWhenItAnswers(t *testing.T) {
	engine.reset()
	if _, err := fetchPinnedTool(context.Background(), mirrorTestURL, mirrorTestSums); err != nil {
		t.Fatal(err)
	}
	if engine.chain(`http(`, `url:"`+mirrorTestMirror()+`"`, `checksum:"sha256:`+mirrorTestSums[mirrorTestURL]+`"`, "sync") == "" {
		t.Errorf("the mirror was not fetched at the pin:\n%v", engine.chains())
	}
	if indexOfFetch(mirrorTestURL) != -1 {
		t.Errorf("upstream was asked although the mirror answered:\n%v", engine.chains())
	}
}

// THE MIRROR IS DOWN: GitHub is the fallback, asked second, at the SAME pin.
func TestFetchPinnedToolFallsBackToUpstreamAtTheSamePin(t *testing.T) {
	engine.reset()
	engine.fail(mirrorTestMirror(), "502 from mirror")
	if _, err := fetchPinnedTool(context.Background(), mirrorTestURL, mirrorTestSums); err != nil {
		t.Fatalf("the fallback answered, so the fetch must: %v", err)
	}
	m, u := indexOfFetch(mirrorTestMirror()), indexOfFetch(mirrorTestURL)
	if m < 0 || u < 0 || m >= u {
		t.Errorf("the mirror must be asked first (at %d) and upstream second (at %d):\n%v", m, u, engine.chains())
	}
	if engine.chain(`http(`, `url:"`+mirrorTestURL+`"`, `checksum:"sha256:`+mirrorTestSums[mirrorTestURL]+`"`, "sync") == "" {
		t.Errorf("the upstream fetch must carry the same sha256 pin:\n%v", engine.chains())
	}
}

// A MIRROR THAT SERVES THE WRONG BYTES is a failed source, not an install: the
// engine's checksum refuses it and upstream is tried.
func TestFetchPinnedToolTreatsAChecksumMismatchAsAFailedSource(t *testing.T) {
	engine.reset()
	engine.fail(mirrorTestMirror(), "digest mismatch: expected sha256:"+mirrorTestSums[mirrorTestURL])
	if _, err := fetchPinnedTool(context.Background(), mirrorTestURL, mirrorTestSums); err != nil {
		t.Fatalf("upstream answers: %v", err)
	}
	if indexOfFetch(mirrorTestURL) < 0 {
		t.Errorf("a mirror that failed its checksum must hand over to upstream:\n%v", engine.chains())
	}
}

// BOTH DOWN: the error names both addresses and both causes.
func TestFetchPinnedToolNamesBothSourcesWhenBothFail(t *testing.T) {
	engine.reset()
	engine.fail(mirrorTestMirror(), "mirror-cause")
	engine.fail(mirrorTestURL, "github-cause")
	_, err := fetchPinnedTool(context.Background(), mirrorTestURL, mirrorTestSums)
	if err == nil {
		t.Fatal("two dead sources are an error")
	}
	for _, want := range []string{mirrorTestMirror(), "mirror-cause", mirrorTestURL, "github-cause", "could not fetch"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error omits %q: %v", want, err)
		}
	}
}

// NO PIN, NO FETCH: not even the mirror is asked.
func TestFetchPinnedToolFailsClosedWithoutAPin(t *testing.T) {
	engine.reset()
	_, err := fetchPinnedTool(context.Background(), mirrorTestURL, map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "has no checksum") {
		t.Errorf("an unpinned URL must be refused: %v", err)
	}
	if len(engine.chains()) != 0 {
		t.Errorf("an unpinned URL reached the network:\n%v", engine.chains())
	}
}

// The wit lanes fetch every asset through the mirror first: a green fake engine
// would otherwise hide a lane that kept the bare upstream fetch.
func TestWitLanesFetchTheirToolsMirrorFirst(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents(witComposeNeedle, witVerdict("rust:wit-compose"))
	wantState(t, runAtom(t, "rust:wit-compose", ""), 0)
	for _, url := range []string{checks.WasmToolsURL, checks.WacURL} {
		m := indexOfFetch(checks.ToolMirrorURL(checks.ToolSHA256[url]))
		if m < 0 {
			t.Errorf("%s was not asked of the mirror:\n%v", url, engine.chains())
		}
		if indexOfFetch(url) != -1 {
			t.Errorf("%s asked upstream with the mirror up", url)
		}
	}
}
