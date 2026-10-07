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

func TestDeclaredNodeKindsKeepsADashesInsideAQuotedKind(t *testing.T) {
	ddl := "INSERT INTO {schema}.node_kinds (kind, canonical_form, note)\n" +
		"SELECT k, k, 'n' FROM unnest(ARRAY[\n    'a--b', 'c', -- trailing prose 'Ghost'\n    'd'\n]) k\nON CONFLICT (kind) DO NOTHING;\n" +
		"INSERT INTO {schema}.node_kinds (kind, canonical_form, note)\nVALUES ('Solo', 'Solo', 'x')\nON CONFLICT (kind) DO NOTHING;\n" +
		"SELECT k FROM unnest(ARRAY['NotAnInsert']) k;\n"
	got := DeclaredNodeKinds(ddl)
	for _, k := range []string{"a--b", "c", "d", "Solo"} {
		if !got[k] {
			t.Errorf("%q missing from %v", k, got)
		}
	}
	if got["Ghost"] || got["NotAnInsert"] || len(got) != 4 {
		t.Errorf("only INSERT blocks declare, and a trailing SQL comment does not: %v", got)
	}
}

func TestDeclaredNodeKindsStopsAtEachStatement(t *testing.T) {
	ddl := "INSERT INTO {schema}.node_kinds (kind, canonical_form, note)\nVALUES ('One', 'One', 'x')\nON CONFLICT (kind) DO NOTHING;\n" +
		"INSERT INTO {schema}.node_kinds (kind, canonical_form, note)\nSELECT k, k, 'n' FROM unnest(ARRAY['Two']) k\nON CONFLICT (kind) DO NOTHING;\n"
	got := DeclaredNodeKinds(ddl)
	if !got["One"] || !got["Two"] || len(got) != 2 {
		t.Errorf("a VALUES row must not borrow the next statement's array: %v", got)
	}
}

func TestCapturedKindSkipTable(t *testing.T) {
	for p, want := range map[string]bool{
		"internal/x/x.go":           false,
		"cmd/star/main.go":          false,
		"x.py":                      true,
		"internal/x/x_test.go":      true,
		"vendor/a/a.go":             true,
		"internal/vendor/a.go":      true,
		"testdata/a.go":             true,
		"internal/chaosfake/a.go":   true,
		"internal/Fake/a.go":        true,
		"internal/boardtest/a.go":   true,
		"internal/orbitstest/a.go":  true,
		"internal/testutil/a.go":    true,
		"internal/x/fakes.go":       true,
		"internal/latest/a.go":      false,
		"internal/contest/a.go":     false,
		"internal/attestation/a.go": false,
	} {
		if got := capturedKindSkip(p); got != want {
			t.Errorf("capturedKindSkip(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestCapturedKindsEdgeCases(t *testing.T) {
	src := `package x

type Kind string

const Conv = Kind("ConvKind")
const (
	A, B = "KindA", "KindB"
	Dup1 = "Same"
	Dup2 = "Same"
	Num  = 7
)
var Pkg = "PkgVar"

type CreateNode struct{ Kind, NodeType string }

func mint(label, kind string) map[string]any {
	return map[string]any{"op": "createNode", "label": label, "kind": kind}
}

func use() {
	_ = CreateNode()
	_ = CreateNode(Kind("Direct"), "l")
	_ = CreateNode(Conv, "l")
	_ = CreateNode(A, "l")
	_ = CreateNode(B, "l")
	_ = CreateNode(Dup1, "l")
	_ = CreateNode(Dup2, "l")
	_ = CreateNode(Pkg, "l")
	_ = CreateNode(7, "l")
	_ = CreateNode(Num, "l")
	_ = CreateNode(a.b.c, "l")
	_ = CreateNode{Kind: "Unqualified"}
	_ = CreateNode{NodeType: "NodeTypeField"}
	_ = map[string]any{"op": "createNode", "node_type": "UnderscoreField"}
	_ = map[string]any{"op": "createNode", "label": "nokind"}
	_ = []int{1, 2}
	_ = mint("l", "ViaSecondParam")
	_ = mint("only-one-arg")
	_ = CreateNode(Unknown, "l")
}
`
	uses, unresolved := CapturedKinds(goFiles(src))
	count := map[string]int{}
	for _, u := range uses {
		count[u.Kind]++
	}
	want := map[string]int{"Direct": 1, "ConvKind": 1, "KindA": 1, "KindB": 1, "Same": 2, "PkgVar": 1,
		"Unqualified": 1, "NodeTypeField": 1, "UnderscoreField": 1, "ViaSecondParam": 1}
	for k, n := range want {
		if count[k] != n {
			t.Errorf("%s captured %d time(s), want %d (%v)", k, count[k], n, uses)
		}
	}
	if len(count) != len(want) {
		t.Errorf("extra kinds read: %v", count)
	}
	// 7, Num, a.b.c and Unknown name no string constant.
	if unresolved != 4 {
		t.Errorf("unresolved = %d, want 4", unresolved)
	}
}

func TestCapturedKindsKeepsOnePackageQualifierAndLineNumbers(t *testing.T) {
	files := map[string]string{
		"internal/a/a.go": "package a\n\nfunc f() {\n\t_ = CreateNode(\"First\", \"l\")\n\n\t_ = CreateNode(\"Second\", \"l\")\n}\n",
	}
	uses, _ := CapturedKinds(files)
	if len(uses) != 2 || uses[0].Line != 4 || uses[1].Line != 6 || uses[0].File != "internal/a/a.go" {
		t.Errorf("sites carry file and line: %v", uses)
	}
}

func TestCapturedKindsOrdersFilesAndSurvivesASyntaxError(t *testing.T) {
	files := map[string]string{
		"z/z.go": "package z\nfunc f() { _ = CreateNode(\"ZKind\", \"l\") }\n",
		"a/a.go": "package a\nfunc f() { _ = CreateNode(\"AKind\", \"l\") }\n",
		"m/m.go": "package m\nfunc f() { _ = CreateNode(\"MKind\", \"l\") }\n",
		"b/b.go": "package b\nfunc (((( broken\n",
	}
	for i := 0; i < 20; i++ {
		uses, _ := CapturedKinds(files)
		if len(uses) != 3 || uses[0].Kind != "AKind" || uses[1].Kind != "MKind" || uses[2].Kind != "ZKind" {
			t.Fatalf("files are read in path order and a broken one is skipped: %v", uses)
		}
	}
}

func TestCapturedKindsASameNamedConstInAnotherPackageIsTheFallback(t *testing.T) {
	files := map[string]string{
		"internal/one/a.go": "package one\nconst K = \"FromOne\"\n",
		"internal/two/a.go": "package two\nconst K = \"FromTwo\"\n",
		"internal/s/s.go":   "package s\nfunc f() { _ = CreateNode(nowhere.K, \"l\"); _ = CreateNode(two.K, \"l\"); _ = CreateNode(K, \"l\") }\n",
	}
	uses, _ := CapturedKinds(files)
	var got []string
	for _, u := range uses {
		got = append(got, u.Kind)
	}
	// nowhere.K and bare K have no qualifying directory: the union, once each.
	if strings.Join(got, ",") != "FromOne,FromTwo,FromTwo,FromOne,FromTwo" {
		t.Errorf("got %v", got)
	}
}

func TestNodeKindsDeclaredReportsKindsInOrderAndPluralizesSites(t *testing.T) {
	src := "package x\nfunc f() {\n_ = CreateNode(\"Zed\", \"l\")\n_ = CreateNode(\"Alpha\", \"l\")\n_ = CreateNode(\"Memory\", \"l\")\n}\n"
	state, report := NodeKindsDeclared(goFiles(src), miniSchema)
	if state != 1 {
		t.Fatalf("state %d", state)
	}
	if a, z := strings.Index(report, "'Alpha'"), strings.Index(report, "'Zed'"); a == -1 || z == -1 || a > z {
		t.Errorf("kinds are reported in name order:\n%s", report)
	}
	if strings.Contains(report, "'Memory'") {
		t.Errorf("a declared kind is not reported:\n%s", report)
	}
	state, report = NodeKindsDeclared(goFiles("package x\nfunc f() {\n_ = CreateNode(\"Memory\", \"l\")\n_ = CreateNode(\"Memory\", \"l\")\n_ = CreateNode(Mystery(), \"l\")\n}\n"), miniSchema)
	if state != 0 || !strings.Contains(report, "1 captured kind(s)") || !strings.Contains(report, "2 capture site(s)") || !strings.Contains(report, "1 unresolved") {
		t.Errorf("counts are kinds, sites, unresolved: %d\n%s", state, report)
	}
	_, report = NodeKindsDeclared(goFiles("package x\nfunc f() {\n_ = CreateNode(\"N\", \"l\")\n_ = CreateNode(\"N\", \"l\")\n_ = CreateNode(\"N\", \"l\")\n}\n"), miniSchema)
	if strings.Contains(report, "more") || !strings.Contains(report, "x.go:3, internal/x/x.go:4, internal/x/x.go:5") && !strings.Contains(report, "internal/x/x.go:3, internal/x/x.go:4, internal/x/x.go:5") {
		t.Errorf("exactly three sites are listed whole:\n%s", report)
	}
}
