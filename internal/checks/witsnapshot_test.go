package checks

import (
	"slices"
	"testing"
)

const witRec = `{"tool":"wasm-tools 1.261.0","command":"wasm-tools component wit --json {pkg}/guest.wasm","cores":{}}`

func TestWitSnapshotsAreTheDirectoriesWhoseRecordNamesTheExtraction(t *testing.T) {
	got := WitSnapshots(map[string]string{
		"go/genregistry/wit/SOURCE.json": witRec,
		"go/other/wit/SOURCE.json":       `{"tool":"x","command":"something else"}`, // another command
		"go/bad/wit/SOURCE.json":         "{",                                       // not JSON
		"go/elsewhere/SOURCE.json":       witRec,                                    // not under wit/
		"wit/SOURCE.json":                witRec,
	})
	want := []WitSnapshot{
		{Dir: "go/genregistry", Env: "GENREGISTRY_WIT_REQUIRED", Tool: "wasm-tools 1.261.0"},
		{Dir: ".", Env: "WIT_REQUIRED", Tool: "wasm-tools 1.261.0"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if s := WitSnapshotScope(got[:1]); s != "wit: GENREGISTRY_WIT_REQUIRED=1 makes a missing wasm-tools a failure, not a skip (wasm-tools 1.261.0 recorded)" {
		t.Errorf("scope %q", s)
	}
}

func TestWitSnapshotEnvNamesThePackage(t *testing.T) {
	for dir, want := range map[string]string{"go/genregistry": "GENREGISTRY_WIT_REQUIRED", "a/b-c": "B_C_WIT_REQUIRED", ".": "WIT_REQUIRED"} {
		if got := WitSnapshotEnv(dir); got != want {
			t.Errorf("%s: %s, want %s", dir, got, want)
		}
	}
}
