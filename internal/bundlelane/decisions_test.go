package bundlelane

import (
	"slices"
	"strings"
	"testing"
)

const table = `{"keys":{"v|s|_|_|_":{"d":{"e":"allow","r":0,"f":"VERB_GRANT"},"c":{"athena":{"task_list":{"e":"deny","r":1}},"chaos":{"a":{"e":"deny","r":1},"b":{"e":"deny","r":1}}}},"e|star:ourea|_|_":{"d":{"e":"deny","r":1},"c":{}}},"reasons":[[],["no"]],"prefix":{"athena":"plan"},"caps":{"athena":{"task_list":1}}}`

func TestDecisionsCountsTheTableItGrades(t *testing.T) {
	s, err := Decisions(table)
	if err != nil {
		t.Fatal(err)
	}
	if s.Keys != 2 || s.Cells != 3 || s.ReasonSets != 2 || s.Bytes != len(table) || s.Gzipped <= 0 {
		t.Errorf("%+v", s)
	}
	if !strings.Contains(s.String(), "2 keys, 3 cells, 2 reason sets") {
		t.Error(s.String())
	}
}

func TestDecisionsRefusesATableThatCouldNeverHit(t *testing.T) {
	for name, tc := range map[string]struct{ raw, want string }{
		"not json":     {`{`, "not one JSON object"},
		"no keys":      {`{"keys":{},"prefix":{"a":"b"},"caps":{"a":{}}}`, "no keys"},
		"no prefix":    {`{"keys":{"k":{"d":{"e":"deny"}}},"caps":{"a":{}}}`, "no star prefix"},
		"no caps":      {`{"keys":{"k":{"d":{"e":"deny"}}},"prefix":{"a":"b"}}`, "no capability"},
		"no default":   {`{"keys":{"k":{"c":{}}},"prefix":{"a":"b"},"caps":{"a":{}}}`, "no default cell"},
		"a json array": {`[]`, "not one JSON object"},
	} {
		if _, err := Decisions(tc.raw); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDecisionsArgsLoadThePolicyTheDeriveAndTheRoster(t *testing.T) {
	got := DecisionsArgs("/work/decisions-stage")
	want := []string{"opa", "eval", "-d", "policy", "-d", "tools/authz_table", "-d", "/work/decisions-stage", "--format", "raw", "data.authz_v2_table.decisions"}
	if !slices.Equal(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if !strings.HasPrefix(DecisionsPath, "policy/grants/") || !strings.HasPrefix(DecisionsDerive, DecisionsDeriveDir+"/") {
		t.Error(DecisionsPath, DecisionsDerive)
	}
}

func TestBuiltDecisionsReadsTheTableOutOfTheArtifact(t *testing.T) {
	n, err := BuiltDecisions(`{"grants":{"rows":[],"decisions":` + table + `}}`)
	if err != nil || n != 2 {
		t.Errorf("%d %v", n, err)
	}
	if n, err := BuiltDecisions(`{"grants":{"rows":[]}}`); err != nil || n != 0 {
		t.Errorf("no table: %d %v", n, err)
	}
	if _, err := BuiltDecisions(`nope`); err == nil {
		t.Error("not json read as a table")
	}
}
