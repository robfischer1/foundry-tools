package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE INTERPRETER STAGE NAMES WHAT STOPPED IT: a pin with no checksum, an asset
// that will not fetch, an archive that will not verify or unpack. A stage that
// ran is a container.
func TestThePythonInterpreterStageNamesWhatStoppedIt(t *testing.T) {
	url := checks.PythonStandaloneURL
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T)
		want  string
	}{
		{"a pin with no checksum", func(t *testing.T) {
			sum := checks.ToolSHA256[url]
			delete(checks.ToolSHA256, url)
			t.Cleanup(func() { checks.ToolSHA256[url] = sum })
		}, "has no checksum"},
		{"an asset that will not fetch", func(*testing.T) { engine.failLeaf(url, "sync", "dial tcp: i/o timeout") }, "could not fetch"},
		{"an archive that will not unpack", func(*testing.T) { engine.failLeaf(`"/opt/python/bin/python3","--version"`, "sync", "bad archive") }, "did not verify and unpack"},
		{"a stage that ran", func(*testing.T) {}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine.reset()
			tc.setup(t)
			ctr, err := pythonInterpreter(context.Background(), toolsOS())
			switch {
			case tc.want == "" && (err != nil || ctr == nil):
				t.Errorf("container %v, err %v", ctr, err)
			case tc.want != "" && (err == nil || ctr != nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("container %v, err %v, want one containing %q", ctr, err, tc.want)
			}
		})
	}
}

// A STAGE ABOVE THE INTERPRETER THAT FAILS IS LEFT OUT WITH ITS CAUSE: the venv
// that will not build names the python interpreter and stops there.
func TestTheVenvThatWillNotBuildIsLoggedAsTheInterpreterStage(t *testing.T) {
	engine.reset()
	log := capturedLog(t)
	engine.failLeaf(`"uv","venv"`, "sync", "no interpreter")
	if got := buildPython(context.Background(), toolsOS()); got.venv != nil || got.interpreter != nil {
		t.Errorf("layers built past a failed venv: %+v", got)
	}
	if !strings.Contains(log(), "the python interpreter left out of the container") {
		t.Errorf("log %q", log())
	}
}
