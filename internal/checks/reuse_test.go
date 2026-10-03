package checks

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

var lookupKeys = []UnitKey{{Unit: "internal/a", Hash: "ha", Ranges: "ra"}, {Unit: "internal/b", Hash: "hb", Ranges: "rb"}, {Unit: "internal/c", Err: "go list exited 1"}}

func TestTheLookupAsksOnlyAboutKeyedUnits(t *testing.T) {
	var got struct {
		Engine string `json:"engine"`
		Units  []map[string]string
	}
	if err := json.Unmarshal(LookupBody("go", "E", lookupKeys), &got); err != nil {
		t.Fatal(err)
	}
	want := []map[string]string{{"lang": "go", "unit": "internal/a", "hash": "ha", "ranges": "ra"}, {"lang": "go", "unit": "internal/b", "hash": "hb", "ranges": "rb"}}
	if got.Engine != "E" || !reflect.DeepEqual(got.Units, want) {
		t.Fatalf("got %+v", got)
	}
	if string(LookupBody("go", "E", nil)) != `{"engine":"E","units":[]}` {
		t.Fatal("no keys asks about none, as a list")
	}
}

func TestALookupAnswerIsBelievedOnlyWhole(t *testing.T) {
	ok := `{"gradings":[{"lang":"go","unit":"internal/a","hash":"ha","ranges":"ra","run_number":4,"counts":{"killed":2},"mutants":[]}],"misses":[{"lang":"go","unit":"internal/b"}]}`
	got, err := AcceptLookup("go", lookupKeys, []byte(ok))
	if err != nil || len(got) != 1 || got["internal/a"].RunNumber != 4 || got["internal/a"].Counts.Killed != 2 {
		t.Fatalf("got %+v %v", got, err)
	}
	for name, raw := range map[string]string{
		"not JSON":          `{`,
		"another language":  `{"gradings":[{"lang":"rust","unit":"internal/a","hash":"ha","ranges":"ra"}]}`,
		"an unasked unit":   `{"gradings":[{"lang":"go","unit":"internal/z","hash":"ha","ranges":"ra"}]}`,
		"an unkeyable unit": `{"gradings":[{"lang":"go","unit":"internal/c","hash":"","ranges":""}]}`,
		"another hash":      `{"gradings":[{"lang":"go","unit":"internal/a","hash":"hx","ranges":"ra"}]}`,
		"other ranges":      `{"gradings":[{"lang":"go","unit":"internal/a","hash":"ha","ranges":"rx"}]}`,
		"a unit twice":      `{"gradings":[{"lang":"go","unit":"internal/a","hash":"ha","ranges":"ra"},{"lang":"go","unit":"internal/a","hash":"ha","ranges":"ra"}]}`,
	} {
		if got, err := AcceptLookup("go", lookupKeys, []byte(raw)); err == nil || got != nil {
			t.Errorf("%s: believed %+v", name, got)
		}
	}
}

func TestTheRunGradesWhatWasNotReused(t *testing.T) {
	reused := map[string]ReusedGrading{"internal/b": {Unit: "internal/b"}}
	misses := Misses(lookupKeys, reused)
	if len(misses) != 2 || misses[0].Unit != "internal/a" || misses[1].Unit != "internal/c" {
		t.Fatalf("misses = %+v", misses)
	}
	if got := MissPatterns(".", []UnitKey{{Unit: "internal/z"}, {Unit: "."}, {Unit: "internal/a"}}); strings.Join(got, " ") != ". ./internal/a ./internal/z" {
		t.Fatalf("root module patterns = %v", got)
	}
	if got := MissPatterns("tools/forge", []UnitKey{{Unit: "tools/forge/x"}, {Unit: "tools/forge"}}); strings.Join(got, " ") != ". ./x" {
		t.Fatalf("nested module patterns = %v", got)
	}
	got := Reused(map[string]ReusedGrading{"z": {Unit: "z"}, "a": {Unit: "a"}})
	if len(got) != 2 || got[0].Unit != "a" || got[1].Unit != "z" {
		t.Fatalf("reused order = %+v", got)
	}
}

func TestTheReuseLine(t *testing.T) {
	if ReuseLine("go:mutation", nil, 3) != "" {
		t.Fatal("nothing reused says nothing")
	}
	got := ReuseLine("go:mutation", []ReusedGrading{{Unit: "internal/a", Lane: "mutation", RunNumber: 9, Sha: "abcdef0123456789"}, {Unit: "b", Lane: "mutation-bg", RunNumber: 2, Sha: "ff"}}, 3)
	if got != "go:mutation: reused 2 of 3 unit(s), graded earlier at the same content, scope and engine: internal/a (mutation #9 @abcdef012345), b (mutation-bg #2 @ff)" {
		t.Fatalf("got %q", got)
	}
}
