package checks

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWitnessChangeSetSplitsWhatIsAskedSkippedAndVendored(t *testing.T) {
	sources, skipped, vendored := WitnessChangeSet([]string{
		"src/x.py", "cmd/main.go", "web/a.ts", "web/b.tsx", "lib.rs", "x.js",
		"vendor/golang.org/x/y.go", "app/node_modules/p/q.py", "third_party/z.py",
		"README.md", "Makefile", "vendor.go", "deep/vendored/a.py",
	})
	if strings.Join(sources, ",") != "src/x.py,cmd/main.go,vendor.go,deep/vendored/a.py" {
		t.Errorf("sources %v", sources)
	}
	if strings.Join(skipped, ",") != "web/a.ts,web/b.tsx,lib.rs,x.js" {
		t.Errorf("skipped %v", skipped)
	}
	if strings.Join(vendored, ",") != "vendor/golang.org/x/y.go,app/node_modules/p/q.py,third_party/z.py" {
		t.Errorf("vendored %v", vendored)
	}
	if WitnessLanguage("a.go") != "go" || WitnessLanguage("a.py") != "python" || WitnessLanguage("a.pyc") != "" {
		t.Error("WitnessLanguage")
	}
}

func TestDefinesCanonical(t *testing.T) {
	const imp = "stellar_core.classes.adapter.Adapter"
	for p, want := range map[string]bool{
		"src/stellar_core/classes/adapter.py":         true,
		"stellar_core/classes/adapter.py":             true,
		"other/classes/adapter.py":                    false,
		"src/stellar_core/classes/__init__.py":        false,
		"src/stellar_core/classes/adapter.go":         false,
		"src/stellar_core/classes.py":                 true,  // stellar_core.classes, with adapter.Adapter left over
		"src/stellar_core/classes/adapter/Adapter.py": false, // no qualname remains
	} {
		if got := DefinesCanonical(p, imp); got != want {
			t.Errorf("DefinesCanonical(%q) = %v, want %v", p, got, want)
		}
	}
	if DefinesCanonical("src/stellar_core/classes/adapter.py", "") {
		t.Error("an answer without an import path never exempts")
	}
}

// answer wraps a witness verb's JSON body as a tools/call result.
func answer(body string) string {
	text, _ := json.Marshal(body)
	return `{"content":[{"type":"text","text":` + string(text) + `}]}`
}

func TestClassifyWitness(t *testing.T) {
	for _, c := range []struct {
		name, path, raw string
		status          int
		class, verdict  string
		reason          string
		footguns        int
	}{
		{"not 200", "a.py", "", 502, "could-not-consult", "", "narcissus returned HTTP 502 — could not consult.", 0},
		{"not json", "a.py", "<html>", 200, "could-not-consult", "", "not a witness answer", 0},
		{"no content", "a.py", `{"content":[]}`, 200, "could-not-consult", "", "not a witness answer", 0},
		{"no text", "a.py", `{"content":[{"type":"text"}]}`, 200, "could-not-consult", "", "not a witness answer", 0},
		{"an erroring verb", "a.py", `{"isError":true,"content":[{"type":"text","text":"` + strings.Repeat("x", 250) + `"}]}`, 200,
			"could-not-consult", "", "the witness verb errored: " + strings.Repeat("x", 200), 0},
		{"a body that is not json", "a.py", answer("nope"), 200, "could-not-consult", "", "not a witness answer", 0},
		{"no unit", "doc.go", answer(`{"verdict":"refused","failure":{"kind":"parse","detail":"no function/method/class unit found in the snippet"}}`), 200,
			"no-unit", "refused", "nothing to witness", 0},
		{"refused", "a.py", answer(`{"verdict":"refused","failure":{"kind":"parse","detail":"bad indent","expected":"python"}}`), 200,
			"could-not-consult", "refused", "the witness could not read this file (parse): bad indent — expected python", 0},
		{"refused with no kind", "a.py", answer(`{"verdict":"refused","failure":{}}`), 200,
			"could-not-consult", "refused", "could not read this file (?):  — expected ", 0},
		{"unknown", "a.py", answer(`{"verdict":"unknown"}`), 200, "could-not-consult", "unknown", "empty corpus", 0},
		{"canonical source", "src/stellar_core/classes/adapter.py",
			answer(`{"verdict":"standard","novelty":{"canonical":{"class":"Adapter","import":"stellar_core.classes.adapter.Adapter"}}}`), 200,
			"canonical-source", "standard", "defines canonical class 'Adapter' (stellar_core.classes.adapter.Adapter): its own source", 0},
		{"canonical reuse", "src/x.py", answer(`{"verdict":"standard","novelty":{"canonical":{"class":"Adapter","reused":true}}}`), 200,
			"canonical-reuse", "standard", "reuses canonical class 'Adapter'", 0},
		{"a test that does not grade", "tests/test_x.py",
			answer(`{"verdict":"convention","recommendation":"grade with the suite","novelty":{"canonical":{"class":"Adapter","test":true}}}`), 200,
			"advisory", "convention", "advisory — a test resembling canonical class 'Adapter': grade with the suite", 0},
		{"a test that grades", "tests/test_x.py",
			answer(`{"verdict":"novel","novelty":{"canonical":{"class":"Adapter","test":true,"graded":true}}}`), 200,
			"canonical-in-test", "novel", "it grades with the class's own suite", 0},
		{"a test from an older narcissus", "conftest.py", answer(`{"verdict":"novel","novelty":{"canonical":{"class":"Adapter"}}}`), 200,
			"canonical-in-test", "novel", "it exercises the shape, it does not re-implement it", 0},
		{"a test that duplicates a standard", "tests/test_x.py", answer(`{"verdict":"standard","novelty":{"canonical":{"class":"Adapter","footguns":["a","b"]}}}`), 200,
			"finding", "standard", "matches canonical class 'Adapter' — 2 known failure mode(s) to check", 2},
		{"testing.py is not a test", "src/testing.py", answer(`{"verdict":"novel","novelty":{"canonical":{"class":"Adapter","descriptor":"d"}}}`), 200,
			"finding", "novel", "matches canonical class 'Adapter' — 0 known failure mode(s)", 0},
		{"a standard", "a.py", answer(`{"verdict":"standard","recommendation":"` + strings.Repeat("r", 400) + `"}`), 200,
			"finding", "standard", "duplicates a standard: " + strings.Repeat("r", 276), 0},
		{"a convention", "a.py", answer(`{"verdict":"convention","recommendation":"reuse x"}`), 200,
			"advisory", "convention", "advisory — duplicates a convention: reuse x", 0},
		{"novel", "a.py", answer(`{"verdict":"novel","novelty":{"canonical":null}}`), 200, "clean", "novel", "novel on both axes", 0},
		{"an unrecognised verdict", "a.py", answer(`{"verdict":"maybe"}`), 200, "could-not-consult", "maybe", "unrecognised witness verdict 'maybe'", 0},
		{"no verdict", "a.py", answer(`{}`), 200, "could-not-consult", "", "unrecognised witness verdict None", 0},
		{"a verdict that is not a string", "a.py", answer(`{"verdict":3}`), 200, "could-not-consult", "", "unrecognised witness verdict '3'", 0},
	} {
		row := ClassifyWitness(c.path, c.status, c.raw)
		if row.Class != c.class || row.Verdict != c.verdict || !strings.Contains(row.Reason, c.reason) || row.Path != c.path {
			t.Errorf("%s: %+v; want class %s verdict %q reason ~ %q", c.name, row, c.class, c.verdict, c.reason)
		}
		if c.class == "finding" && c.reason != "" && strings.HasPrefix(c.reason, "matches") {
			if row.Canonical == nil || len(row.Canonical.Footguns) != c.footguns || row.Canonical.Class != "Adapter" {
				t.Errorf("%s: canonical %+v", c.name, row.Canonical)
			}
		} else if row.Canonical != nil {
			t.Errorf("%s: carries a canonical it did not match: %+v", c.name, row.Canonical)
		}
		if strings.HasPrefix(c.name, "a standard") && len([]rune(row.Reason)) != 300 {
			t.Errorf("%s: reason not cut at 300: %d", c.name, len([]rune(row.Reason)))
		}
	}
}

func TestWitnessEnvelope(t *testing.T) {
	result := `{"content":[{"type":"text","text":"x"}]}`
	env := `{"jsonrpc":"2.0","id":1,"result":` + result + `}`
	for _, c := range []struct {
		name, contentType, body string
		status                  int
		wantStatus              int
		want                    string
	}{
		{"not 200 passes through", "text/plain", "busy", 503, 503, "busy"},
		{"a json envelope", "application/json", env, 200, 200, result},
		{"an event stream, last data line", "text/event-stream; charset=utf-8", "event: message\r\ndata: {\"x\":1}\r\ndata: " + env + "\r\n\r\n", 200, 200, result},
		{"an event stream with no data", "text/event-stream", "event: ping\n", 200, 200, ""},
		{"not json", "application/json", "oops", 200, 200, "oops"},
		{"no result", "application/json", `{"jsonrpc":"2.0"}`, 200, 200, "{}"},
		{"a json-rpc error", "application/json", `{"error":{"code":-1,"message":"boom"}}`, 200, 200,
			`{"isError": true, "content": [{"type": "text", "text": "{\"code\":-1,\"message\":\"boom\"}"}]}`},
	} {
		status, got := WitnessEnvelope(c.status, c.contentType, c.body)
		if status != c.wantStatus || got != c.want {
			t.Errorf("%s: (%d, %q), want (%d, %q)", c.name, status, got, c.wantStatus, c.want)
		}
	}
	// An error envelope classifies as the verb erroring, with the error as its text.
	_, errored := WitnessEnvelope(200, "application/json", `{"error":{"message":"boom"}}`)
	if row := ClassifyWitness("a.py", 200, errored); !strings.Contains(row.Reason, `the witness verb errored: {"message":"boom"}`) {
		t.Errorf("error envelope: %+v", row)
	}
}

func rows(classes ...string) []WitnessRow {
	out := make([]WitnessRow, len(classes))
	for i, c := range classes {
		out[i] = WitnessRow{Path: c + ".py", Class: c, Reason: "because " + c}
	}
	return out
}

func TestAggregateWitness(t *testing.T) {
	for _, c := range []struct {
		name              string
		rows              []WitnessRow
		skipped, vendored []string
		state             int
		reason            string
	}{
		{"nothing at all", nil, nil, nil, 0, "nothing to witness — no changed .py or .go file the star authored"},
		{"only skipped and vendored", nil, []string{"a.ts"}, []string{"vendor/x.go", "vendor/y.go"}, 0,
			"nothing to witness — no changed .py or .go file the star authored; skipped 1 file(s) in languages the witness has no analyzer for; skipped 2 vendored file(s)"},
		{"findings win over could-not-consult", rows("finding", "could-not-consult", "clean"), nil, nil, 1,
			"findings in 1 of 3 file(s): finding.py: because finding — also could not consult 1: could-not-consult.py"},
		{"findings alone", rows("finding", "finding"), []string{"a.ts"}, nil, 1,
			"findings in 2 of 2 file(s): finding.py: because finding; finding.py: because finding"},
		{"could-not-consult", rows("could-not-consult", "clean", "could-not-consult"), nil, nil, 2,
			"could not consult 2 of 3 file(s): could-not-consult.py: because could-not-consult; could-not-consult.py: because could-not-consult"},
		{"advisory", rows("advisory", "clean", "no-unit"), nil, nil, 0,
			"clean — 2 file(s) witnessed; 1 advisory (reuse, not rewrite): advisory.py: because advisory; 1 file(s) had no unit to witness: no-unit.py"},
		{"every file had no unit", rows("no-unit", "no-unit"), nil, []string{"vendor/a.go"}, 0,
			"nothing to witness — every changed source file had no unit; skipped 1 vendored file(s); 2 file(s) had no unit to witness: no-unit.py, no-unit.py"},
		{"canonical exemptions", rows("canonical-source", "canonical-in-test", "canonical-reuse", "clean"), nil, nil, 0,
			"clean — 4 file(s) witnessed; 1 file(s) are a canonical class's own source: canonical-source.py; 1 test file(s) resemble a canonical class: canonical-in-test.py; 1 file(s) reuse a canonical class: canonical-reuse.py"},
		{"one exemption is enough to say so", rows("canonical-reuse"), nil, nil, 0,
			"clean — 1 file(s) witnessed; 1 file(s) reuse a canonical class: canonical-reuse.py"},
		{"a canonical test alone", rows("canonical-in-test"), nil, nil, 0, "clean — 1 file(s) witnessed; 1 test file(s)"},
		{"a canonical source alone", rows("canonical-source"), nil, nil, 0, "clean — 1 file(s) witnessed; 1 file(s) are"},
		{"novel", rows("clean", "clean"), nil, nil, 0, "clean — 2 file(s) witnessed, novel on both axes"},
	} {
		state, reason := AggregateWitness(c.rows, c.skipped, c.vendored)
		if state != c.state || !strings.HasPrefix(reason, c.reason) {
			t.Errorf("%s: (%d, %q), want (%d, %q…)", c.name, state, reason, c.state, c.reason)
		}
		if c.name == "novel" && strings.Contains(reason, ";") {
			t.Errorf("%s: a clean run with nothing beside it carries a tail: %q", c.name, reason)
		}
	}
}

func TestWitnessSummary(t *testing.T) {
	if WitnessSummary(nil) != "" {
		t.Error("no rows, no table")
	}
	got := WitnessSummary([]WitnessRow{
		{Path: "a.py", Class: "finding", Verdict: "standard", Reason: strings.Repeat("r", 200), Canonical: &WitnessCanonical{Class: "Adapter", Footguns: []string{"f1", "f2"}}},
		{Path: "b.go", Class: "could-not-consult", Reason: "down"},
	})
	want := "| file | verdict | class | reason |\n|---|---|---|---|\n" +
		"| a.py | standard | finding | " + strings.Repeat("r", 160) + " |\n" +
		"| b.go | - | could-not-consult | down |\n" +
		"- a.py · Adapter: f1\n- a.py · Adapter: f2\n"
	if got != want {
		t.Errorf("summary:\n%s\nwant:\n%s", got, want)
	}
}

func TestStarNameAndRequest(t *testing.T) {
	for url, want := range map[string]string{
		"http://ourea:8215/nereus.git\n": "nereus",
		"/home/rob/Forge/Outputs/x/":     "x",
		"git@host:rob/thing":             "thing",
		"":                               "unknown",
		"/":                              "unknown",
		".git":                           "unknown",
	} {
		if got := StarName(url); got != want {
			t.Errorf("StarName(%q) = %q, want %q", url, got, want)
		}
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(WitnessRequest(7, "q\"", "go", "ci:gate:x@HEAD", "a.go")), &req); err != nil {
		t.Fatal(err)
	}
	args := req["params"].(map[string]any)["arguments"].(map[string]any)
	if req["id"] != float64(7) || req["method"] != "tools/call" || req["params"].(map[string]any)["name"] != "witness" ||
		args["query"] != "q\"" || args["language"] != "go" || args["caller"] != "ci:gate:x@HEAD" || args["path"] != "a.go" || args["granularity"] != "code" {
		t.Errorf("request %v", req)
	}
}
