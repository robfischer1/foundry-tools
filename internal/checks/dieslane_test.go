package checks

import (
	"reflect"
	"strings"
	"testing"
)

func TestOpaVersionOKMatchesAWholeLineOnly(t *testing.T) {
	out := "Version: 1.18.0\nBuild Commit: abc\nGo Version: go1.24\n"
	if !OpaVersionOK(out, "1.18.0") {
		t.Error("the pinned version is on its own line and was not seen")
	}
	for _, bad := range []string{"1.18.1", "1.18", "1.18.0-rc1"} {
		if OpaVersionOK(out, bad) {
			t.Errorf("%q: matched a version the binary does not carry", bad)
		}
	}
	if OpaVersionOK("Version: 1.18.0-rc1\n", "1.18.0") {
		t.Error("a prerelease is not the pin")
	}
	if OpaVersionOK("", "1.18.0") {
		t.Error("no output is not the pin")
	}
}

func TestOpaTestStateFoldsOpasOwnCodes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		code  int
		out   string
		state int
	}{
		{"clean suite", 0, "data.authz.test_a: PASS (1ms)\nPASS: 312/312\n", 0},
		{"failing assertion is a finding, not a cannot-run", 2, "FAIL: 1/312\n", 1},
		{"a rego load error is a finding too", 1, "rego_parse_error\n", 1},
		{"a code opa does not use is a cannot-run", 137, "", 2},
		{"a zero-test run is refused", 0, "PASS: 0/0\n", 2},
		{"a missing summary line is refused", 0, "nothing to say\n", 2},
	} {
		state, reason := OpaTestState(tc.code, tc.out)
		if state != tc.state {
			t.Errorf("%s: got state %d, want %d (%s)", tc.name, state, tc.state, reason)
		}
		if reason == "" {
			t.Errorf("%s: a state with no reason is a verdict nobody can read", tc.name)
		}
	}
}

func TestOpaTestStateCountsTheLastSummaryLine(t *testing.T) {
	// -v prints a line per file; the summary is the final one.
	_, reason := OpaTestState(0, "PASS: 2/2\nPASS: 312/312\n")
	if !strings.Contains(reason, "312 assertion(s) pass") {
		t.Errorf("got %q", reason)
	}
}

func TestOpaDenySetReadsTheEvalShape(t *testing.T) {
	doc := `{"result":[{"expressions":[{"value":["no owner","bad tier"],"text":"data.admission.deny"}]}]}`
	got, err := OpaDenySet(doc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := []string{"no owner", "bad tier"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestOpaDenySetIsEmptyForAnAdmittedInput(t *testing.T) {
	got, err := OpaDenySet(`{"result":[{"expressions":[{"value":[]}]}]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestOpaDenySetRefusesAShapeItCannotRead(t *testing.T) {
	// `{}` is what an UNDEFINED rule answers, and it is not a deny set of
	// zero — it is an expression that did not evaluate.
	for _, bad := range []string{`{}`, `{"result":[]}`, `{"result":[{"expressions":[]}]}`, `not json`, `{"result":[{"expressions":[{"value":{"a":1}}]}]}`} {
		if _, err := OpaDenySet(bad); err == nil {
			t.Errorf("%q: want an error, got none", bad)
		}
	}
}

func TestOpaDenySetRendersANonStringDeny(t *testing.T) {
	got, err := OpaDenySet(`{"result":[{"expressions":[{"value":[{"rule":"tier"}]}]}]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0], "tier") {
		t.Errorf("got %v", got)
	}
}

func TestOpaAllowedReadsTheVerbList(t *testing.T) {
	got, err := OpaAllowed(`{"result":[{"expressions":[{"value":["search","graph_read"]}]}]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := []string{"search", "graph_read"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	for _, bad := range []string{`{}`, `{"result":[{"expressions":[{"value":"search"}]}]}`, ``} {
		if _, err := OpaAllowed(bad); err == nil {
			t.Errorf("%q: want an error, got none", bad)
		}
	}
}

const fullDataJSON = `{
  "authz_audience": {"star_only": {"chaos": ["graph_write", "graph_gc"], "eros": ["eros_index"]}},
  "authz_grants": {"a": 1},
  "authz_meta": {"b": 2},
  "path_grants": {"c": 3},
  "subject_aliases": {"d": 4}
}`

func TestDiesDataKeysPassesAFullBundleAndCountsTheRoster(t *testing.T) {
	missing, stars, err := DiesDataKeys(fullDataJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("got missing %v, want none", missing)
	}
	if stars != 2 {
		t.Errorf("got %d stars, want 2", stars)
	}
}

func TestDiesDataKeysTreatsEmptyAsMissing(t *testing.T) {
	// This IS the fail-open: bundle mode reads only files literally named
	// data.json, so a tree using arbitrary names builds `{}` and tests green.
	missing, stars, err := DiesDataKeys(`{}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(missing, DiesRequiredDataRoots) {
		t.Errorf("got %v, want every required root", missing)
	}
	if stars != 0 {
		t.Errorf("got %d stars, want 0", stars)
	}

	partial := `{"authz_audience":{"star_only":{}},"authz_grants":{},"authz_meta":{"b":2},"path_grants":[],"subject_aliases":null}`
	missing, _, err = DiesDataKeys(partial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"authz_grants", "path_grants", "subject_aliases"}
	if !reflect.DeepEqual(missing, want) {
		t.Errorf("got %v, want %v", missing, want)
	}
}

func TestDiesDataKeysErrorsOnAnUnparseableDataDocument(t *testing.T) {
	if _, _, err := DiesDataKeys("not json"); err == nil {
		t.Error("want an error, got none")
	}
}

func TestDiesCanaryIsChaosFirstCuratedVerb(t *testing.T) {
	if got := DiesCanary(fullDataJSON); got != "graph_write" {
		t.Errorf("got %q, want graph_write", got)
	}
}

func TestDiesCanaryIsEmptyWhenTheRosterDidNotSurvive(t *testing.T) {
	for _, doc := range []string{
		`{}`,
		`{"authz_audience":{}}`,
		`{"authz_audience":{"star_only":{}}}`,
		`{"authz_audience":{"star_only":{"chaos":[]}}}`,
		`{"authz_audience":{"star_only":{"eros":["x"]}}}`,
		`not json`,
	} {
		if got := DiesCanary(doc); got != "" {
			t.Errorf("%q: got %q, want empty", doc, got)
		}
	}
}

func TestContractFixtureVerdictGradesBothDirections(t *testing.T) {
	for _, tc := range []struct {
		name       string
		expectFail bool
		code       int
		bad        bool
		contains   string
	}{
		{"lagging", true, 1, false, "correctly detected"},
		{"lagging", true, 0, true, "no longer detects it"},
		{"agreeing", false, 0, false, "correctly passed"},
		{"agreeing", false, 1, true, "invents divergence"},
		// A crash is not a detection: the checker must exit non-zero for the
		// right reason, and a fixture that exits 2 still did not pass.
		{"unreadable", true, 2, false, "correctly detected"},
		{"retiring", false, 2, true, "invents divergence"},
	} {
		line, bad := ContractFixtureVerdict(tc.name, tc.expectFail, tc.code)
		if bad != tc.bad {
			t.Errorf("%s expectFail=%v code=%d: got bad=%v, want %v", tc.name, tc.expectFail, tc.code, bad, tc.bad)
		}
		if !strings.Contains(line, tc.contains) || !strings.Contains(line, tc.name) {
			t.Errorf("%s: got %q, want it to name the fixture and say %q", tc.name, line, tc.contains)
		}
	}
}
