package catalogue

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
)

var splitLangs = []string{"go", "rust", "ts"}

func laneFile(t *testing.T, name string) atomkit.LaneDef {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "lanes", name+".toml"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := atomkit.ParseLane(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return d
}

// expand is a lane's stage:* expansion over the catalogue, with lane@1's
// atom_lanes rule as daedalus plans it: a lane with atom_lanes plans only the
// atoms whose lane it names; one without plans every atom of its stages.
func expand(d atomkit.LaneDef) map[string]string {
	out := map[string]string{}
	for _, r := range Rows() {
		if slices.Contains(d.Stages, r.Stage) && (len(d.AtomLanes) == 0 || slices.Contains(d.AtomLanes, string(r.Lane))) {
			out[r.ID] = string(r.Lane)
		}
	}
	return out
}

func TestEachSplitLaneExpandsOnlyItsOwnAtoms(t *testing.T) {
	seen := map[string]string{}
	for _, lang := range splitLangs {
		got := expand(laneFile(t, "gate-"+lang))
		if len(got) == 0 {
			t.Fatalf("gate-%s plans no atoms", lang)
		}
		for id, lane := range got {
			if lane != lang {
				t.Errorf("gate-%s plans %s, an atom of lane %q", lang, id, lane)
			}
			if prev, dup := seen[id]; dup {
				t.Errorf("%s is planned by gate-%s and gate-%s", id, prev, lang)
			}
			seen[id] = lang
		}
	}
}

// rust-wasm is a toolchain, not a lane: ts:regen is Lane ts, so it renders in
// gate-ts and never in gate-rust.
func TestRegenRendersInGateTS(t *testing.T) {
	if _, ok := expand(laneFile(t, "gate-ts"))["ts:regen"]; !ok {
		t.Error("gate-ts does not plan ts:regen")
	}
	if _, ok := expand(laneFile(t, "gate-rust"))["ts:regen"]; ok {
		t.Error("gate-rust plans ts:regen")
	}
}

// The base gate's remainder (atoms of no split language) plus the three split
// lanes is exactly the unsplit gate: no atom is lost and none is planned twice.
func TestSplitLanesPartitionTheGate(t *testing.T) {
	all := expand(laneFile(t, "gate"))
	if len(all) == 0 {
		t.Fatal("gate plans no atoms")
	}
	got := map[string]bool{}
	for id, lane := range all {
		if !slices.Contains(splitLangs, lane) {
			got[id] = true
		}
	}
	for _, lang := range splitLangs {
		for id := range expand(laneFile(t, "gate-"+lang)) {
			if got[id] {
				t.Errorf("%s planned twice", id)
			}
			got[id] = true
		}
	}
	for id := range all {
		if !got[id] {
			t.Errorf("%s is planned by no lane once the gate splits", id)
		}
	}
	if len(got) != len(all) {
		t.Errorf("split plans %d atoms, the gate %d", len(got), len(all))
	}
}
