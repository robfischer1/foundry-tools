package checks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path"
	"slices"
	"testing"
)

// laneCasesSum pins testdata/lane-cases.json. daedalus' executor plans lanes
// from the same table (internal/executor/plan/testdata/lane-cases.json) and pins
// the same sum: the two repos are two implementations of one question, and a
// table that moved in one of them breaks the other's test.
const laneCasesSum = "2d15715de08238a860f2bd6fa8d0e058097b2aeb8f6cea42223cd5e92c8f3ea9"

type laneCase struct {
	Name  string   `json:"name"`
	Files []string `json:"files"`
	Lanes []string `json:"lanes"`
}

// lanesOfFiles is the Dagger runner's answer for a tree listing: the
// population the engine hands the gate, its root entries, its go modules and
// its python, through the planner's own LanesOfTree.
func lanesOfFiles(files []string) []Lane {
	pop := GatePopulation(files)
	var entries []string
	for _, f := range pop {
		if path.Dir(f) == "." {
			entries = append(entries, f)
		}
	}
	return LanesOfTree(Tree{Entries: entries, GoModules: len(GoModuleDirs(pop)), PythonFiles: len(PythonFiles(pop))})
}

func TestLaneCasesShared(t *testing.T) {
	raw, err := os.ReadFile("testdata/lane-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != laneCasesSum {
		t.Fatalf("lane-cases.json sha256 %s; this pin and daedalus' must move together", got)
	}
	var tab struct {
		Cases []laneCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &tab); err != nil {
		t.Fatal(err)
	}
	for _, c := range tab.Cases {
		var got []string
		for _, l := range lanesOfFiles(c.Files) {
			got = append(got, string(l))
		}
		if !slices.Equal(got, c.Lanes) && (len(got) != 0 || len(c.Lanes) != 0) {
			t.Errorf("%s: lanes %v, want %v", c.Name, got, c.Lanes)
		}
	}
}
