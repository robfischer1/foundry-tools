package regenkit

import (
	"strings"
	"testing"
)

// sample is a provenance.json with a note that holds braces and escaped quotes.
const sample = `{
  "guest_wasm": {
    "file": "guest.wasm",
    "sha256": "25669e54a82bf93d443b25916c209bc5612f2cfed6726f0c714a6cb9d8694583",
    "bytes": 216841
  },
  "bindings": {
    "file": "zz_generated.go",
    "sha256": "fa21e0704b947c5e9338f0182f9da07c584c5bf71b5c954d7d9eebb4428f9479",
    "bytes": 120774,
    "note": "what the generator writes {braces} and \"quotes\" and a \\ slash"
  }
}
`

func TestRewriteChangesOnlyTheHashesAndSizes(t *testing.T) {
	recs := []Rec{
		{Key: "guest_wasm", SHA256: "25669e54a82bf93d443b25916c209bc5612f2cfed6726f0c714a6cb9d8694583", Bytes: 216841},
		{Key: "bindings", SHA256: "fa21e0704b947c5e9338f0182f9da07c584c5bf71b5c954d7d9eebb4428f9479", Bytes: 120774},
	}
	same, err := Rewrite(sample, recs)
	if err != nil || same != sample {
		t.Fatalf("unchanged inputs rewrote the file: %v\n%s", err, same)
	}
	recs[0].SHA256, recs[0].Bytes = strings.Repeat("a", 64), 12
	recs[1].SHA256, recs[1].Bytes = strings.Repeat("b", 64), 99999
	got, err := Rewrite(sample, recs)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		"25669e54a82bf93d443b25916c209bc5612f2cfed6726f0c714a6cb9d8694583", strings.Repeat("a", 64), "216841", "12",
		"fa21e0704b947c5e9338f0182f9da07c584c5bf71b5c954d7d9eebb4428f9479", strings.Repeat("b", 64), "120774", "99999",
	).Replace(sample)
	if got != want {
		t.Errorf("rewrite differs from the expected text:\n%s", got)
	}
}

func TestRewriteKeepsTheFilesOwnIndent(t *testing.T) {
	tab := "{\n\t\"guest_wasm\": {\n\t\t\"file\": \"guest.wasm\",\n\t\t\"sha256\": \"" + strings.Repeat("0", 64) + "\",\n\t\t\"bytes\": 1\n\t}\n}"
	got, err := Rewrite(tab, []Rec{{Key: "guest_wasm", SHA256: strings.Repeat("f", 64), Bytes: 22}})
	if err != nil || !strings.Contains(got, "\t\t\"bytes\": 22\n") || !strings.Contains(got, strings.Repeat("f", 64)) || strings.HasSuffix(got, "\n") {
		t.Errorf("%v\n%q", err, got)
	}
	compact := `{"guest_wasm":{"file":"g","sha256":"` + strings.Repeat("0", 64) + `","bytes":1},"x":[{"a":"}"}]}`
	got, err = Rewrite(compact, []Rec{{Key: "guest_wasm", SHA256: strings.Repeat("1", 64), Bytes: 2}})
	if err != nil || got != `{"guest_wasm":{"file":"g","sha256":"`+strings.Repeat("1", 64)+`","bytes":2},"x":[{"a":"}"}]}` {
		t.Errorf("%v %s", err, got)
	}
}

func TestRewriteSaysWhatItCouldNotFind(t *testing.T) {
	for name, tc := range map[string]struct {
		text string
		rec  Rec
		says string
	}{
		"a record the file lacks": {`{"a":{}}`, Rec{Key: "guest_wasm"}, "no guest_wasm record"},
		"a record with no sha256": {`{"guest_wasm":{"bytes":1}}`, Rec{Key: "guest_wasm"}, "guest_wasm has no sha256"},
		"a record with no bytes":  {`{"guest_wasm":{"sha256":"` + strings.Repeat("0", 64) + `"}}`, Rec{Key: "guest_wasm"}, "guest_wasm has no bytes"},
		"text that is not json":   {`{"guest_wasm":{"file":"unterminated`, Rec{Key: "guest_wasm"}, "no guest_wasm record"},
		"an unterminated key":     {`{"guest`, Rec{Key: "guest_wasm"}, "no guest_wasm record"},
		"an unterminated object":  {`{"guest_wasm":{"file":"g"`, Rec{Key: "guest_wasm"}, "no guest_wasm record"},
	} {
		if _, err := Rewrite(tc.text, []Rec{tc.rec}); err == nil || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: %v, want %q", name, err, tc.says)
		}
	}
}

func TestRewriteFilesMovesOnlyTheNamedEntries(t *testing.T) {
	text := "{\n \"guest_wasm\": {\"sha256\": \"" + strings.Repeat("0", 64) + "\", \"bytes\": 1},\n \"files\": {\n  \"a.js\": {\"sha256\": \"" + strings.Repeat("a", 64) + "\", \"bytes\": 1},\n  \"interfaces/b.d.ts\": {\"sha256\": \"" + strings.Repeat("b", 64) + "\", \"bytes\": 2}\n }\n}\n"
	same, absent, err := RewriteFiles(text, []Rec{{Key: "a.js", SHA256: strings.Repeat("a", 64), Bytes: 1}})
	if err != nil || absent != nil || same != text {
		t.Fatalf("an entry that held was rewritten: %v %v\n%s", err, absent, same)
	}
	got, absent, err := RewriteFiles(text, []Rec{{Key: "interfaces/b.d.ts", SHA256: strings.Repeat("c", 64), Bytes: 9}, {Key: "new.js", SHA256: strings.Repeat("d", 64), Bytes: 4}})
	want := strings.NewReplacer(strings.Repeat("b", 64), strings.Repeat("c", 64), `"bytes": 2`, `"bytes": 9`).Replace(text)
	if err != nil || got != want || len(absent) != 1 || absent[0] != "new.js" {
		t.Errorf("%v %v\n%s", err, absent, got)
	}
	// the top-level guest_wasm is not a files entry, and a file with no files record has none to move
	if out, absent, err := RewriteFiles(`{"guest_wasm": {"sha256": "x", "bytes": 1}}`, []Rec{{Key: "guest_wasm", SHA256: "y", Bytes: 2}}); err != nil || absent != nil || !strings.Contains(out, `"x"`) {
		t.Errorf("%v %v %s", err, absent, out)
	}
	if _, _, err := RewriteFiles(`{"files": {"a.js": {"note": "no hash"}}}`, []Rec{{Key: "a.js", SHA256: "y", Bytes: 2}}); err == nil {
		t.Error("an entry with no sha256 was left stale without an error")
	}
}
