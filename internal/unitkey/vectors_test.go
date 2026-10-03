package unitkey

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// THE GOLDEN VECTORS ARE THE CONTRACT BETWEEN TWO COPIES. stellar-core-go
// carries the canonical package as unitkey, with the identical
// testdata/vectors.json; both copies must answer
// every vector bit for bit, or a key the lane stored would not match the key
// the door recomputes. Regenerate with UNITKEY_WRITE_VECTORS=1 only alongside
// a Version bump, and copy the file to the other repository in the same change.

type hashVector struct {
	Name    string   `json:"name"`
	Lang    Lang     `json:"lang"`
	Entries []Entry  `json:"entries"`
	Closure []string `json:"closure"`
	Hash    string   `json:"hash"`
}

type rangesVector struct {
	Name    string             `json:"name"`
	Diff    string             `json:"diff"`
	Parsed  map[string][]Range `json:"parsed"`
	Lang    Lang               `json:"lang"`
	Entries []Entry            `json:"entries"`
	Unit    string             `json:"unit"`
	Ranges  string             `json:"ranges"`
}

type engineVector struct {
	Inputs map[string]string `json:"inputs"`
	Engine string            `json:"engine"`
}

type vectors struct {
	Version string         `json:"version"`
	Hashes  []hashVector   `json:"hashes"`
	Ranges  []rangesVector `json:"ranges"`
	Engines []engineVector `json:"engines"`
}

const vectorsFile = "testdata/vectors.json"

// computed answers every vector's outputs from its inputs.
func computed(v vectors) vectors {
	v.Version = Version
	for i, h := range v.Hashes {
		v.Hashes[i].Hash = Hash(h.Lang, h.Entries, h.Closure)
	}
	for i, r := range v.Ranges {
		v.Ranges[i].Parsed = ParseRanges(r.Diff)
		v.Ranges[i].Ranges = Ranges(r.Lang, r.Entries, r.Unit, v.Ranges[i].Parsed)
	}
	for i, e := range v.Engines {
		v.Engines[i].Engine = Engine(e.Inputs)
	}
	return v
}

func TestTheGoldenVectors(t *testing.T) {
	raw, err := os.ReadFile(vectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	var want vectors
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	var in vectors
	_ = json.Unmarshal(raw, &in)
	got := computed(in)
	if os.Getenv("UNITKEY_WRITE_VECTORS") == "1" {
		out, _ := json.MarshalIndent(got, "", "  ")
		if err := os.WriteFile(vectorsFile, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if len(want.Hashes) == 0 || len(want.Ranges) == 0 || len(want.Engines) == 0 {
		t.Fatal("the vectors file must carry every kind of vector")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the code answers vectors the file does not hold — a rule moved without a Version bump\ngot  %+v\nwant %+v", got, want)
	}
}
