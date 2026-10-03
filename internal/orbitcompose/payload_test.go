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
	contracts, orbits, err := Payloads(files)
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
		c, o, err := Payloads(files)
		if err == nil || c != nil || o != nil {
			t.Errorf("%s: %v %v %v", name, c, o, err)
		}
	}
	if _, _, err := Payloads(map[string][]byte{"x.toml": []byte(contractBody)}); err == nil || !strings.Contains(err.Error(), "<producer>-<consumer>") {
		t.Errorf("a misnamed contract: %v", err)
	}
	if _, _, err := Payloads(nil); err == nil || !strings.Contains(err.Error(), "holds no contract") {
		t.Errorf("no files: %v", err)
	}
}
