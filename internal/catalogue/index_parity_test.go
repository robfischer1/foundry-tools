package catalogue

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// needWords are the Needs that are not tools.toml keys (atomkit.Def.Needs).
var needWords = []string{"dies", "svid", "pytools"}

// builtNeeds are programs the Dagger gate builds or mounts per run rather than
// pins, so tools.toml cannot name them yet. The executor cannot provision them
// (proposed DEF-B4-orbit-tools); each is a hole to close, not a naming choice.
var builtNeeds = []string{"tool:orbitparse", "tool:narc"}

func needsOf(e Entry) []string {
	if e.Def != nil {
		return e.Def.Needs
	}
	return e.Manifest.Needs
}

// TestDEF_B3_050_EveryNeedResolvesToAToolsKey: the executor provisions a Need by
// its tools.toml key, so a Need that names the program instead is refused as
// "not pinned" (compose:config asked for tool:docker-compose; the key is
// tool.compose).
func TestDEF_B3_050_EveryNeedResolvesToAToolsKey(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "tools.toml"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := atomkit.ParseTools(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range Entries() {
		for _, n := range needsOf(e) {
			kind, name, _ := strings.Cut(n, ":")
			switch {
			case slices.Contains(needWords, n), slices.Contains(builtNeeds, n):
			case kind == "tool":
				if _, ok := f.Tools[name]; !ok {
					t.Errorf("%s: need %q is not a [tool.*] key of tools.toml", e.ID, n)
				}
			case kind == "service":
				if _, ok := f.Services[name]; !ok {
					t.Errorf("%s: need %q is not a [service.*] key of tools.toml", e.ID, n)
				}
			default:
				t.Errorf("%s: need %q is not a known need word", e.ID, n)
			}
		}
	}
}

// described is the index entry of a logic atom.
func described(t *testing.T, d atomkit.Def) map[string]any {
	t.Helper()
	b, err := atomkit.Describe(d)
	if err != nil {
		t.Fatalf("%s: %v", d.ID, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestDEF_B4_EveryIndexAbsentSentenceIsTheDaggerPathsSentence: the sentence an
// atom's index entry carries is the one the Dagger gate prints for that atom
// when nothing is there to check (R20: result and reason byte-equal). The Dagger
// gate's answer is what an any-lane atom itself says over a tree holding
// nothing it reads (every other atom is stood down by its lane first, with
// AbsentVerdict, and never asked). A subsumed atom's covered sentence is the planner's, not the
// index's: the surface stands an atom down before its subsumer is looked at.
func TestDEF_B4_EveryIndexAbsentSentenceIsTheDaggerPathsSentence(t *testing.T) {
	refuse := func(context.Context, atoms.Cmd) (string, int) { return "no program in this test", 127 }
	for _, e := range Entries() {
		if !checks.AtomExists(e.ID) {
			t.Fatalf("%s has no catalogue row", e.ID)
		}
		row := checks.AtomByID(e.ID)
		want := checks.AbsentVerdict(row).Reason
		if d := e.Def; d != nil {
			in := atomtest.TreeIn(t, map[string]string{"README.md": "x\n"})
			in.Exec = refuse
			if row.Lane == checks.LaneAny {
				if v := d.Check(context.Background(), in); v.Result == "absent" {
					want = v.Reason
				}
			}
			if got, _ := described(t, *d)["absent_reason"].(string); got != want {
				t.Errorf("%s:\n index  %q\n dagger %q", e.ID, got, want)
			}
			continue
		}
		if got := checks.AbsentReasonFor(row.ID, row.Lane, row.NeedsManifest); got != want {
			t.Errorf("%s: %q vs %q", e.ID, got, want)
		}
	}
}
