package checks

import (
	"encoding/json"
	"testing"
)

// nonEmptyJSON is python's truthiness over a JSON value; every branch of the
// type switch has a case here, both sides.
func TestNonEmptyJSONIsPythonsTruthiness(t *testing.T) {
	cases := map[string]bool{
		``: false, `null`: false, `not json`: false,
		`true`: true, `false`: false,
		`0`: false, `0.0`: false, `1`: true, `-2.5`: true,
		`""`: false, `"x"`: true, `" "`: true,
		`[]`: false, `[0]`: true,
		`{}`: false, `{"a":null}`: true,
	}
	for raw, want := range cases {
		if got := nonEmptyJSON(json.RawMessage(raw)); got != want {
			t.Errorf("nonEmptyJSON(%s) = %v, want %v", raw, got, want)
		}
	}
}
