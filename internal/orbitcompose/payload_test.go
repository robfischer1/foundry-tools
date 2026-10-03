package orbitcompose

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestPayloadsAreTheContractsVerbatimAndTheDie(t *testing.T) {
	files := map[string][]byte{
		"urania-themis.toml": []byte(contractBody),
		"chaos-urania.toml":  []byte(contractBody),
		"README.md":          []byte("not a contract"),
	}
	contracts, orbits, err := Payloads(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for n, raw := range contracts {
		names = append(names, n)
		if !bytes.Equal(raw, files[n]) {
			t.Errorf("%s is not byte for byte", n)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"chaos-urania.toml", "urania-themis.toml"}) {
		t.Errorf("contracts die %v", names)
	}
	ut := mustParse(t, "urania-themis.toml", contractBody)
	cu := mustParse(t, "chaos-urania.toml", contractBody)
	want := Die([]Contract{cu, ut})
	if len(orbits) != len(want) || !bytes.Equal(orbits[DieIndex], want[DieIndex]) || !bytes.Equal(orbits["urania.orbit.toml"], want["urania.orbit.toml"]) {
		t.Errorf("orbits die is not Die: %v", orbits)
	}
}

func TestPayloadsRefuseADirectoryThatDoesNotCompose(t *testing.T) {
	for name, files := range map[string]map[string][]byte{
		"bad contract": {"urania-themis.toml": []byte("version = 1")},
		"none":         {"README.md": nil},
	} {
		c, o, err := Payloads(files, nil)
		if err == nil || c != nil || o != nil {
			t.Errorf("%s: %v %v %v", name, c, o, err)
		}
	}
	if _, _, err := Payloads(map[string][]byte{"x.toml": []byte(contractBody)}, nil); err == nil || !strings.Contains(err.Error(), "<producer>-<consumer>") {
		t.Errorf("a misnamed contract: %v", err)
	}
	if _, _, err := Payloads(nil, nil); err == nil || !strings.Contains(err.Error(), "holds no contract") {
		t.Errorf("no files: %v", err)
	}
}

// THE DIE NAMES A HYPHENATED STAR'S EDGE WHEN GIVEN THE ROSTER; ONE BAD FILE
// STILL REFUSES THE DIE (a die is never published over a partial directory).
func TestPayloadsSplitAHyphenatedStarAndStillRefuseABadFile(t *testing.T) {
	stars := map[string]bool{"blade-runner": true, "poseidon": true}
	files := map[string][]byte{"blade-runner-poseidon.toml": []byte(contractBody)}
	contracts, orbits, err := Payloads(files, stars)
	if err != nil || len(contracts) != 1 || orbits["blade-runner.orbit.toml"] == nil || orbits["poseidon.orbit.toml"] == nil {
		t.Fatalf("%v %v %v", contracts, orbits, err)
	}
	files["bad-poseidon.toml"] = []byte("version = 1")
	if c, o, err := Payloads(files, stars); err == nil || c != nil || o != nil || !strings.Contains(err.Error(), "bad-poseidon.toml") {
		t.Errorf("%v %v %v", c, o, err)
	}
}
