package orbitcompose

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func mustParse(t *testing.T, file, body string) Contract {
	t.Helper()
	c, err := ParseContract(file, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestParseContractStampsTheDigestAndDefaultsViaToMCP(t *testing.T) {
	c := mustParse(t, "urania-themis.toml", contractBody)
	if c.Via != "mcp" || c.Digest != Digest([]byte(contractBody)) {
		t.Errorf("via %q digest %q", c.Via, c.Digest)
	}
	if !strings.HasPrefix(c.Digest, "sha256:") || len(c.Digest) != len("sha256:")+64 {
		t.Errorf("digest %q is not sha256:<64 hex>", c.Digest)
	}
	k := mustParse(t, "urania-themis.toml", `via = "kafka"`+"\n"+contractBody)
	if k.Via != "kafka" {
		t.Errorf("a named via was replaced: %q", k.Via)
	}
	if _, err := ParseContract("urania-themis.toml", []byte(`via = "a b"`+"\n"+contractBody)); err == nil || !strings.Contains(err.Error(), "via") {
		t.Errorf("a via render cannot quote: err %v", err)
	}
}

func TestDigestIsSha256OfTheBytes(t *testing.T) {
	// sha256("") — a fixed vector, so the hash cannot be swapped unseen.
	if got := Digest(nil); got != "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("got %s", got)
	}
}

// indexDoc is orbits.json read back, so the test asserts the shape a reader
// sees rather than the struct that wrote it.
type indexDoc struct {
	GeneratedFrom string `json:"generated_from"`
	Edges         []map[string]any
	Stars         map[string]struct {
		Produces []string `json:"produces"`
		Consumes []string `json:"consumes"`
	} `json:"stars"`
}

func TestIndexIsTheFrozenShapePlusViaDigestAndStars(t *testing.T) {
	ut := mustParse(t, "urania-themis.toml", contractBody)
	cu := mustParse(t, "chaos-urania.toml", strings.Replace(contractBody, `"neighbors"`, `"shapes"`, 1))
	ct := mustParse(t, "chaos-themis.toml", contractBody)
	raw := Index([]Contract{ut, cu, ct})
	if !bytes.HasSuffix(raw, []byte("}\n")) || !bytes.HasPrefix(raw, []byte("{\n \"generated_from\"")) {
		t.Errorf("not one-space indented JSON with a trailing newline:\n%s", raw)
	}
	var doc indexDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.GeneratedFrom != DieGeneratedFrom {
		t.Errorf("generated_from %q", doc.GeneratedFrom)
	}
	var order []string
	for _, e := range doc.Edges {
		order = append(order, e["contract"].(string))
	}
	if !slices.Equal(order, []string{"chaos-themis", "chaos-urania", "urania-themis"}) {
		t.Errorf("edges not sorted producer, consumer: %v", order)
	}
	e := doc.Edges[2]
	for k, want := range map[string]string{
		"producer": "urania", "consumer": "themis", "contract": "urania-themis", "via": "mcp",
		"version": "1", "status": "generated", "wire_form": "native", "digest": ut.Digest,
	} {
		if e[k] != want {
			t.Errorf("edge %s = %v, want %q", k, e[k], want)
		}
	}
	if verbs, _ := json.Marshal(e["verbs"]); string(verbs) != `["neighbors","shape_for"]` {
		t.Errorf("verbs %s", verbs)
	}
	if len(e) != 9 {
		t.Errorf("edge carries %d keys, want 9: %v", len(e), e)
	}
	if s := doc.Stars["themis"]; len(s.Produces) != 0 || !slices.Equal(s.Consumes, []string{"chaos", "urania"}) {
		t.Errorf("themis %+v", s)
	}
	if s := doc.Stars["chaos"]; !slices.Equal(s.Produces, []string{"themis", "urania"}) || len(s.Consumes) != 0 {
		t.Errorf("chaos %+v", s)
	}
	if s := doc.Stars["urania"]; !slices.Equal(s.Produces, []string{"themis"}) || !slices.Equal(s.Consumes, []string{"chaos"}) {
		t.Errorf("urania %+v", s)
	}
	// An empty side is [], never null: a reader iterating it must not branch.
	if !bytes.Contains(raw, []byte(`"produces": []`)) {
		t.Errorf("an empty produces is not []:\n%s", raw)
	}
	if again := Index([]Contract{ct, ut, cu}); !bytes.Equal(raw, again) {
		t.Error("the same contracts in another order indexed differently")
	}
}

func TestIndexOfNothingIsAnEmptyGraph(t *testing.T) {
	if got := string(Index(nil)); !strings.Contains(got, `"edges": []`) || !strings.Contains(got, `"stars": {}`) {
		t.Errorf("got %s", got)
	}
}

func TestDieIsEverySidecarAndTheIndex(t *testing.T) {
	ut := mustParse(t, "urania-themis.toml", contractBody)
	files := Die([]Contract{ut})
	var names []string
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"orbits.json", "themis.orbit.toml", "urania.orbit.toml"}) {
		t.Errorf("files %v", names)
	}
	flux := Files("prime", Compose([]Contract{ut}))
	if !bytes.Equal(files["urania.orbit.toml"], flux["urania.orbit.toml"]) {
		t.Error("the die's sidecar differs from the one flux mounts")
	}
	if !bytes.Equal(files[DieIndex], Index([]Contract{ut})) {
		t.Error("the die's index is not Index")
	}
}

func TestDieOwnedIsSidecarsAndTheIndexOnly(t *testing.T) {
	for name, want := range map[string]bool{
		"chaos.orbit.toml": true, "orbits.json": true, "kustomization.yaml": false, "README.md": false, "other.json": false,
	} {
		if DieOwned(name, nil) != want {
			t.Errorf("%s: owned %v", name, !want)
		}
	}
}

func TestMainWritesTheDieThenChecksIt(t *testing.T) {
	contracts := writeContracts(t, map[string]string{"urania-themis.toml": contractBody})
	die := filepath.Join(t.TempDir(), "orbits")
	code, stdout, stderr := run("-contracts", contracts, "-die", die)
	if code != 0 || stderr != "" || stdout != "write  orbits.json\nwrite  themis.orbit.toml\nwrite  urania.orbit.toml\n" {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(die, KustomizationFile)); !os.IsNotExist(err) {
		t.Errorf("the die carries a kustomization: %v", err)
	}
	if code, _, _ := run("-check", "-contracts", contracts, "-die", die); code != 0 {
		t.Errorf("-check on the written die: exit %d", code)
	}
	if err := os.WriteFile(filepath.Join(die, DieIndex), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stdout, _ := run("-check", "-contracts", contracts, "-die", die); code != 1 || stdout != "write  orbits.json\n" {
		t.Errorf("-check on a hand-edited index: exit %d stdout %q", code, stdout)
	}
	if err := os.WriteFile(filepath.Join(die, "README.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := run("-contracts", contracts, "-die", die); code != 2 || !strings.Contains(stderr, "README.md is not the composer's") {
		t.Errorf("a person's file in the die: exit %d stderr %q", code, stderr)
	}
}

func TestMainTakesExactlyOneTarget(t *testing.T) {
	for _, args := range [][]string{
		{"-contracts", "x", "-out", "y", "-die", "z"},
		{"-contracts", "x", "-die", ""},
	} {
		if code, _, stderr := run(args...); code != 2 || !strings.Contains(stderr, "usage: orbitcompose") {
			t.Errorf("%v: exit %d stderr %q", args, code, stderr)
		}
	}
}
