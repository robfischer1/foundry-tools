package checks

import (
	"encoding/json"
	"path"
	"slices"
	"strings"
)

// WitSnapshotSource is the file a WIT-snapshot directory carries (<pkg>/wit/SOURCE.json):
// the record of the tool and command that extracted each snapshot from its guest.
const WitSnapshotSource = "SOURCE.json"

// WitSnapshotCommand is the prefix of the extraction command that makes a directory a
// WIT snapshot set: a snapshot is `wasm-tools component wit --json` of a guest, and the
// package's test extracts it again and compares byte for byte.
const WitSnapshotCommand = "wasm-tools component wit"

// WitSnapshotNeeds is the need the atoms that run a tree's suite declare so the
// snapshot-vs-guest test has a wasm-tools to run: the lane's pinned one.
//
// WHY NOT THE 1.261.0 THE SNAPSHOTS WERE EXTRACTED WITH (SOURCE.json records it). MEASURED
// 2026-10-21 over all 30 stellar-core-go guests: `component wit --json` of 1.261.0 and of
// 1.262.0 (the lane's WasmToolsVersion) are byte-identical to every committed snapshot. The
// composed guest.wasm bytes DO differ across the two (WasmTools1261*'s comment), but the
// snapshot reads the WIT the component carries, not the bytes around it, so the second
// pinned tool, with the pod's own archive handling to go with it, buys nothing. If a later
// wasm-tools moves the JSON, this test is the one that says so, and that is the moment to
// pin the older one (checks.WasmToolsRelease already knows 1.261.0).
var WitSnapshotNeeds = []string{"tool:wasm-tools"}

// WitSnapshot is one package that holds WIT snapshots and the variable that turns its
// snapshot-vs-guest test from a skip (no wasm-tools on PATH) into a failure.
type WitSnapshot struct {
	Dir  string // the package: the parent of the wit directory
	Env  string
	Tool string // the version SOURCE.json records
}

// WitSnapshotEnv names the variable a package's snapshot test reads: the package
// directory, upper-cased, then _WIT_REQUIRED. go/genregistry -> GENREGISTRY_WIT_REQUIRED.
func WitSnapshotEnv(dir string) string {
	if dir == "." {
		return "WIT_REQUIRED"
	}
	return envBase(dir) + "_WIT_REQUIRED"
}

// WitSnapshots reads every wit/SOURCE.json a tree carries (path -> contents) and answers
// the packages whose record names the wasm-tools extraction, in path order. The opt-in is
// a fact about the tree, like GravityRegens': a SOURCE.json that is not JSON or names
// another command is not a snapshot set and is passed over. The variable is harmless to a
// package whose test does not read it.
func WitSnapshots(files map[string]string) []WitSnapshot {
	var out []WitSnapshot
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		if path.Base(p) != WitSnapshotSource || path.Base(path.Dir(p)) != "wit" {
			continue
		}
		var rec struct {
			Tool    string `json:"tool"`
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(files[p]), &rec) != nil || !strings.HasPrefix(rec.Command, WitSnapshotCommand) {
			continue
		}
		dir := path.Dir(path.Dir(p))
		out = append(out, WitSnapshot{Dir: dir, Env: WitSnapshotEnv(dir), Tool: rec.Tool})
	}
	return out
}

// WitSnapshotScope is the line the atom prints about the snapshot tests it held.
func WitSnapshotScope(ws []WitSnapshot) string {
	envs := make([]string, len(ws))
	for i, w := range ws {
		envs[i] = w.Env
	}
	return "wit: " + strings.Join(envs, "=1, ") + "=1 makes a missing wasm-tools a failure, not a skip (" + ws[0].Tool + " recorded)"
}
