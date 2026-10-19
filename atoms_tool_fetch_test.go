package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// failTool makes the asset's own URL unfetchable: the fetch is then an error,
// there being no second source to fall to.
func failTool(url, message string) {
	engine.fail(`url:"`+url+`"`, message)
}

// failToolLeaf is failTool narrowed to one leaf of the fetch.
func failToolLeaf(url, leaf, message string) {
	engine.failLeaf(`url:"`+url+`"`, leaf, message)
}

// sumFile is the line toolFile writes for the in-container verification of url:
// it names the checksum and the file, and appears in no fetch's query.
func sumFile(url string) string { return checks.ToolSHA256[url] + "  /tmp/pkg" }

// pinnedFetch answers the newest chain that fetched the pinned asset url from its
// own URL WITH its sha256 pin and read the named leaf, or "".
func pinnedFetch(url, leaf string) string {
	sum := checks.ToolSHA256[url]
	return engine.chain(`http(`, `url:"`+url+`"`, `checksum:"sha256:`+sum+`"`, leaf)
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

// registryAsked says whether any recorded query fetched from the retired tool
// mirror (zot's foundry/tool/mirror).
func registryAsked() bool {
	for _, q := range engine.chains() {
		if strings.Contains(q, "/foundry/tool/mirror/") {
			return true
		}
	}
	return false
}

const fetchTestURL = "https://upstream.example/tool.tgz"

var fetchTestSums = map[string]string{fetchTestURL: strings.Repeat("ab", 32)}

// MR1: the asset resolves through its own URL, at its pin, and no request goes
// to the retired mirror.
func TestFetchPinnedToolFetchesTheOwnURLAtThePinAndNeverTheMirror(t *testing.T) {
	engine.reset()
	if _, err := fetchPinnedTool(context.Background(), fetchTestURL, fetchTestSums); err != nil {
		t.Fatal(err)
	}
	if engine.chain(`http(`, `url:"`+fetchTestURL+`"`, `checksum:"sha256:`+fetchTestSums[fetchTestURL]+`"`, "sync") == "" {
		t.Errorf("the asset was not fetched at the pin:\n%v", engine.chains())
	}
	if registryAsked() || len(engine.chains()) != 1 {
		t.Errorf("only the asset's own URL is asked:\n%v", engine.chains())
	}
}

// MR1: a fetch that fails, a sha mismatch included (the engine refuses the
// bytes), is the error, naming the URL and the cause; nothing else is tried.
func TestFetchPinnedToolFailureNamesTheURLAndTriesNothingElse(t *testing.T) {
	for _, cause := range []string{"github-cause", "digest mismatch: expected sha256:" + fetchTestSums[fetchTestURL]} {
		engine.reset()
		failTool(fetchTestURL, cause)
		_, err := fetchPinnedTool(context.Background(), fetchTestURL, fetchTestSums)
		if err == nil {
			t.Fatalf("%s: a dead source is an error", cause)
		}
		for _, want := range []string{fetchTestURL, cause, "could not fetch"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the error omits %q: %v", want, err)
			}
		}
		if registryAsked() || len(engine.chains()) != 1 {
			t.Errorf("%s: a second source was asked:\n%v", cause, engine.chains())
		}
	}
}

// NO PIN, NO FETCH: not even the asset's URL is asked.
func TestFetchPinnedToolFailsClosedWithoutAPin(t *testing.T) {
	engine.reset()
	_, err := fetchPinnedTool(context.Background(), fetchTestURL, map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "has no checksum") {
		t.Errorf("an unpinned URL must be refused: %v", err)
	}
	if len(engine.chains()) != 0 {
		t.Errorf("an unpinned URL reached the network:\n%v", engine.chains())
	}
}

// The wit lanes fetch every asset from its own URL at the pin: a green fake
// engine would otherwise hide a lane that kept a mirror fetch.
func TestWitLanesFetchTheirToolsFromTheirOwnURL(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents(witComposeNeedle, witVerdict("rust:wit-compose"))
	wantState(t, runAtom(t, "rust:wit-compose", ""), 0)
	for _, url := range []string{checks.WasmToolsURL, checks.WacURL} {
		if pinnedFetch(url, "sync") == "" {
			t.Errorf("%s was not fetched at its pin:\n%v", url, engine.chains())
		}
	}
	if registryAsked() {
		t.Errorf("the retired mirror was asked:\n%v", engine.chains())
	}
}
