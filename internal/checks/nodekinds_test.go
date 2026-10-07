package checks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// miniSchema is the shape of chaos's DDL: a bootstrap array, a later array, a
// single-row VALUES registration, prose that names a kind and declares none.
const miniSchema = "-- INSERT INTO {schema}.node_kinds (kind) SELECT 'Prose' FROM unnest(ARRAY['Prose']) k\n" +
	"INSERT INTO {schema}.node_kinds (kind, canonical_form, note)\n" +
	"SELECT k, k, 'bootstrap' FROM unnest(ARRAY[\n    'Memory', 'nyx-star'\n]) k\nON CONFLICT (kind) DO NOTHING;\n" +
	"INSERT INTO {schema}.node_kinds (kind, canonical_form, note)\n" +
	"VALUES ('Tombstone', 'Tombstone',\n        'a note with (parens), it''s fine')\nON CONFLICT (kind) DO NOTHING;\n" +
	"INSERT INTO {schema}.node_kinds (kind, canonical_form, note)\n" +
	"SELECT k, k, 'later' FROM unnest(ARRAY[\n    'nyx-contract'\n]) k\nON CONFLICT (kind) DO NOTHING;\n"

func TestDeclaredNodeKindsReadsEveryRegistrationShape(t *testing.T) {
	got := DeclaredNodeKinds(miniSchema)
	for _, k := range []string{"Memory", "nyx-star", "Tombstone", "nyx-contract"} {
		if !got[k] {
			t.Errorf("%q not read from the DDL: %v", k, got)
		}
	}
	if got["Prose"] {
		t.Errorf("a kind named only in an SQL comment was declared: %v", got)
	}
	if len(got) != 4 {
		t.Errorf("want 4 kinds, got %v", got)
	}
}

func goFiles(body string) map[string]string { return map[string]string{"internal/x/x.go": body} }

func TestCapturedKindsFindsEveryCaptureShape(t *testing.T) {
	src := `package x

const ArrKind = "ArrKind"
const (
	OpCreate OpKind = "createNode"
	WireKind        = "WireKind"
)

type Op struct{ Kind OpKind; CreateKind, Label string }

func CreateNode(kind, label string) Op { return Op{Kind: OpCreate, CreateKind: kind, Label: label} }

func use() {
	_ = CreateNode(ArrKind, "a")
	_ = CreateNode("Literal", "a")
	_ = vocabulary.CreateNode(vocab.WireKind, "a")
	_ = []map[string]any{{"op": "createNode", "kind": "MapKind", "label": "x"}}
	_ = []wireOp{{Op: "createNode", Kind: "StructKind"}}
	_ = ops.CreateNode{NodeType: "TypedKind"}
	_ = map[string]any{"op": "addEdge", "kind": "NotACreate"}
}
`
	uses, unresolved := CapturedKinds(goFiles(src))
	got := map[string]bool{}
	for _, u := range uses {
		got[u.Kind] = true
	}
	for _, k := range []string{"ArrKind", "Literal", "WireKind", "MapKind", "StructKind", "TypedKind"} {
		if !got[k] {
			t.Errorf("capture of %q missed: %v", k, uses)
		}
	}
	if got["NotACreate"] {
		t.Errorf("a non-createNode literal's kind was read as a capture")
	}
	if unresolved != 0 {
		t.Errorf("%d unresolved, want 0 (the helper forwards its parameter to its callers)", unresolved)
	}
}

func TestCapturedKindsReadsAHelperThroughItsCallers(t *testing.T) {
	src := `package x
func mint(kind, label string) map[string]any { return map[string]any{"op": "createNode", "kind": kind, "label": label} }
func a() { _ = mint("ViaHelper", "l") }
func b(k string) { _ = mint(k, "l") }
`
	uses, unresolved := CapturedKinds(goFiles(src))
	if len(uses) != 1 || uses[0].Kind != "ViaHelper" {
		t.Errorf("want the one resolvable caller, got %v", uses)
	}
	if unresolved != 1 {
		t.Errorf("a caller passing a variable is one unresolved site, got %d", unresolved)
	}
}

func TestCapturedKindsPrefersTheQualifiedPackage(t *testing.T) {
	files := map[string]string{
		"internal/vocabulary/v.go": "package vocabulary\nconst Kind = \"Memory\"\n",
		"internal/other/o.go":      "package other\nconst Kind = \"Elsewhere\"\n",
		"internal/s/s.go":          "package s\nfunc f() { _ = graph.CreateNode(vocabulary.Kind, \"l\") }\n",
	}
	uses, _ := CapturedKinds(files)
	if len(uses) != 1 || uses[0].Kind != "Memory" {
		t.Errorf("vocabulary.Kind is Memory, not the union: %v", uses)
	}
}

func TestCapturedKindsSkipsTestsFakesVendorAndFixtures(t *testing.T) {
	body := "package x\nfunc f() { _ = CreateNode(\"Nope\", \"l\") }\n"
	files := map[string]string{
		"x_test.go": body, "vendor/a/a.go": body, "internal/chaosfake/f.go": body,
		"internal/boardtest/b.go": body, "testdata/t.go": body, "internal/p/p.txt": body,
	}
	if uses, _ := CapturedKinds(files); len(uses) != 0 {
		t.Errorf("a capture that is not the star's own was counted: %v", uses)
	}
}

func TestNodeKindsDeclaredLadder(t *testing.T) {
	declared := goFiles("package x\nfunc f() { _ = CreateNode(\"Memory\", \"l\") }\n")
	undeclared := goFiles("package x\nfunc f() { _ = CreateNode(\"ChronicleChapterX\", \"l\") }\n")
	for _, tc := range []struct {
		name    string
		files   map[string]string
		schema  string
		state   int
		needles []string
	}{
		{"captures nothing", goFiles("package x\n"), "", 0, []string{"no node kind is captured"}},
		{"all declared", declared, miniSchema, 0, []string{"1 captured kind(s) are all declared"}},
		{"undeclared is a finding", undeclared, miniSchema, 1,
			[]string{"'ChronicleChapterX' is captured (internal/x/x.go:2)", "capture_refused", "bigintschema migration"}},
		{"empty vocabulary cannot run", declared, "package bigintschema\n", 2, []string{"CANNOT RUN", "parsed to no declared kinds"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, report := NodeKindsDeclared(tc.files, tc.schema)
			if state != tc.state {
				t.Fatalf("state %d, want %d\n%s", state, tc.state, report)
			}
			for _, n := range tc.needles {
				if !strings.Contains(report, n) {
					t.Errorf("report lacks %q:\n%s", n, report)
				}
			}
		})
	}
}

func TestNodeKindsDeclaredTruncatesLongSiteLists(t *testing.T) {
	var b strings.Builder
	b.WriteString("package x\nfunc f() {\n")
	for i := 0; i < 5; i++ {
		b.WriteString("_ = CreateNode(\"Nope\", \"l\")\n")
	}
	b.WriteString("}\n")
	_, report := NodeKindsDeclared(goFiles(b.String()), miniSchema)
	if !strings.Contains(report, "+2 more") {
		t.Errorf("five sites should list three and count two:\n%s", report)
	}
}

func TestFetchNodeKindsSchemaNamesWhatTheDoorSaid(t *testing.T) {
	var asked string
	var gone atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gone.Load() {
			http.NotFound(w, r)
			return
		}
		asked = r.URL.Query().Get("repo") + " " + r.URL.Query().Get("path")
		if r.URL.Query().Get("repo") != NodeKindsRepo {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(miniSchema))
	}))
	defer srv.Close()
	door := Door{Base: srv.URL, Client: srv.Client()}
	body, err := FetchNodeKindsSchema(context.Background(), door)
	if err != nil || body != miniSchema {
		t.Fatalf("body %q err %v", body, err)
	}
	if asked != "rob/chaos bigintschema/schema.go" {
		t.Errorf("asked %q", asked)
	}
	gone.Store(true)
	if _, err := FetchNodeKindsSchema(context.Background(), door); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("a 404 must be an error naming the status, got %v", err)
	}
	srv.Close()
	if _, err := FetchNodeKindsSchema(context.Background(), door); err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("a dead door must be unreachable, got %v", err)
	}
}
