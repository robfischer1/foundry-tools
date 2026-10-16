package checks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const capFixtures = "testdata/capabilities"

func loadFixtureServed(t *testing.T) []Served {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(capFixtures, "served", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no served fixtures (%v)", err)
	}
	var out []Served
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		s, err := ParseServed(strings.TrimSuffix(filepath.Base(p), ".json"), raw)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		out = append(out, s)
	}
	return out
}

func loadFixtureJSON(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capFixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatal(err)
	}
}

// TestRenderCapabilitiesMatchesTheGolden is the renderer's fixture output: the
// served exports of three stars (one with no verbs and no prefix) rendered
// against an audience, byte for byte. Nothing is written to foundry-dies.
func TestRenderCapabilitiesMatchesTheGolden(t *testing.T) {
	var prefixes map[string]string
	var aud Audience
	loadFixtureJSON(t, "prefixes.json", &prefixes)
	loadFixtureJSON(t, "audience.json", &aud)
	got, err := RenderCapabilities(loadFixtureServed(t), prefixes, aud)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(capFixtures, "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("rendered capabilities differ from testdata/capabilities/data.json:\n%s", got)
	}
	// Idempotent on input order.
	s := loadFixtureServed(t)
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
	again, _ := RenderCapabilities(s, prefixes, aud)
	if string(again) != string(got) {
		t.Error("render depends on input order")
	}
	// And the gate agrees with its own render.
	var decl Capabilities
	if err := json.Unmarshal(got, &decl); err != nil {
		t.Fatal(err)
	}
	if drift := CapabilitiesDrift(decl, loadFixtureServed(t), prefixes); len(drift) != 0 {
		t.Errorf("a fresh render drifts from what it was rendered from: %v", drift)
	}
}

func TestRenderCapabilitiesTiers(t *testing.T) {
	aud := Audience{StarOnly: map[string][]string{"s": {"p_a"}}, SessionHidden: map[string][]string{"s": {"p_a", "p_b"}}}
	for gw, want := range map[string]string{"p_a": TierStarOnly, "p_b": TierSessionHidden, "p_c": TierDefault} {
		if got := aud.TierOf("s", gw); got != want {
			t.Errorf("%s: %s, want %s", gw, got, want)
		}
	}
	if aud.TierOf("other", "p_a") != TierDefault {
		t.Error("another star's rows leaked")
	}
}

func TestRenderCapabilitiesRefusesWhatItCannotName(t *testing.T) {
	_, err := RenderCapabilities([]Served{{Schema: 1, Star: "x", Verbs: []string{"a"}}}, map[string]string{}, Audience{})
	if err == nil || !strings.Contains(err.Error(), "x: serves 1 verb(s) but its record has no verb_prefix") {
		t.Errorf("a served star with no prefix rendered: %v", err)
	}
	_, err = RenderCapabilities([]Served{{Schema: 1, Star: "x"}, {Schema: 1, Star: "x"}}, map[string]string{"x": "p"}, Audience{})
	if err == nil || !strings.Contains(err.Error(), "exported twice") {
		t.Errorf("a duplicate export rendered: %v", err)
	}
}

func TestParseServed(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"x", `{"schema":1,"star":"x","verbs":["a"]}`, ""},
		{"x", `{"schema":1,"star":"x","verbs":[]}`, ""},
		{"x", `{"schema":2,"star":"x","verbs":[]}`, "schema 2"},
		{"x", `{"schema":1,"star":"y","verbs":[]}`, `names star "y" but is committed as "x"`},
		{"x", `{"schema":1,"star":"x"}`, "no verbs list"},
		{"x", `{"schema":1,"star":"x","verbs":["a","a"]}`, `"a" listed twice`},
		{"x", `{"schema":1,"star":"x","verbs":[""]}`, "empty verb name"},
		{"x", `{"schema":1,"star":"","verbs":[]}`, "no star"},
		{"x", `{"schema":1,"star":"x","verbs":[],"extra":1}`, "not a served-verbs export"},
		{"x", `nope`, "not a served-verbs export"},
	} {
		_, err := ParseServed(tc.name, []byte(tc.body))
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: refused: %v", tc.body, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: err %v, want %q", tc.body, err, tc.want)
		}
	}
}

func TestCapabilitiesDrift(t *testing.T) {
	prefixes := map[string]string{"a": "pa"}
	good := CapabilityStar{Prefix: "pa", Verbs: []string{"x", "y"}, Tier: map[string]string{"x": TierDefault, "y": TierStarOnly}}
	served := []Served{{Schema: 1, Star: "a", Verbs: []string{"x", "y"}}}
	for _, tc := range []struct {
		name     string
		declared map[string]CapabilityStar
		served   []Served
		want     []string
	}{
		{"agree", map[string]CapabilityStar{"a": good}, served, nil},
		{"served but undeclared verb", map[string]CapabilityStar{"a": {Prefix: "pa", Verbs: []string{"x"}, Tier: map[string]string{"x": TierDefault}}}, served,
			[]string{"a: served but not declared (denied to every caller): y"}},
		{"declared but unserved verb", map[string]CapabilityStar{"a": {Prefix: "pa", Verbs: []string{"x", "y", "z"}, Tier: map[string]string{"x": TierDefault, "y": TierDefault, "z": TierDefault}}}, served,
			[]string{"a: declared but not served: z"}},
		{"star served but not declared at all", nil, served,
			[]string{"a: serves 2 verb(s) x, y but " + CapabilitiesData + " declares none - every one is denied"}},
		{"star declared with no export", map[string]CapabilityStar{"a": good, "b": good}, served,
			[]string{"b: declared in " + CapabilitiesData + " but no served-verbs export is committed under " + CapabilitiesServedDir + "/"}},
		{"empty surface needs no declaration", nil, []Served{{Schema: 1, Star: "e", Verbs: []string{}}}, nil},
		{"wrong prefix", map[string]CapabilityStar{"a": {Prefix: "zz", Verbs: good.Verbs, Tier: good.Tier}}, served,
			[]string{`a: declared prefix "zz" but the star's record says "pa"`}},
		{"tier names an unknown verb", map[string]CapabilityStar{"a": {Prefix: "pa", Verbs: good.Verbs, Tier: map[string]string{"x": TierDefault, "y": TierDefault, "q": TierDefault}}}, served,
			[]string{`a: tier names "q", which is not a declared verb`}},
		{"tier of a verb is not a class", map[string]CapabilityStar{"a": {Prefix: "pa", Verbs: good.Verbs, Tier: map[string]string{"x": "admin", "y": TierDefault}}}, served,
			[]string{`a: tier of "x" is "admin", not one of default, star_only, session_hidden`}},
		{"declared verb with no tier", map[string]CapabilityStar{"a": {Prefix: "pa", Verbs: good.Verbs, Tier: map[string]string{"x": TierDefault}}}, served,
			[]string{`a: declared verb "y" has no tier`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CapabilitiesDrift(Capabilities{Stars: tc.declared}, tc.served, prefixes)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestQuoteListCapsLongLists(t *testing.T) {
	var vs []string
	for _, c := range "abcdefghijkl" {
		vs = append(vs, string(c))
	}
	if got := quoteList(vs); got != "a, b, c, d, e, f, g, h (+4 more)" {
		t.Errorf("got %q", got)
	}
}

func fakeTree(files map[string]string) TreeReader {
	return TreeReader{
		Read: func(p string) (string, error) {
			if s, ok := files[p]; ok {
				return s, nil
			}
			return "", os.ErrNotExist
		},
		Glob: func(pat string) []string {
			var out []string
			for p := range files {
				if ok, _ := filepath.Match(pat, p); ok {
					out = append(out, p)
				}
			}
			return out
		},
	}
}

func TestGradeCapabilitiesServed(t *testing.T) {
	a := AtomDef{ID: CapabilitiesServedID, Stage: StagePrecommit, Lane: LaneAny}
	served := `{"schema":1,"star":"a","verbs":["x"]}`
	decl := `{"stars":{"a":{"prefix":"pa","verbs":["x"],"tier":{"x":"default"}}},"tx":7}`
	rec := `{"verb_prefix":"pa"}`
	full := map[string]string{CapabilitiesData: decl, CapabilitiesServedDir + "/a.json": served, "fleet/repos/a.json": rec}
	with := func(over map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range full {
			out[k] = v
		}
		for k, v := range over {
			if v == "<del>" {
				delete(out, k)
				continue
			}
			out[k] = v
		}
		return out
	}
	for _, tc := range []struct {
		name   string
		files  map[string]string
		state  int
		needle string
	}{
		{"agree", full, 0, "1 verb(s) across 1 star(s)"},
		{"nothing landed is absent", map[string]string{"fleet/repos/a.json": rec}, 0, "ABSENT"},
		{"served verb dropped from the document", with(map[string]string{CapabilitiesData: `{"stars":{"a":{"prefix":"pa","verbs":[],"tier":{}}}}`}), 1, "served but not declared (denied to every caller): x"},
		{"document deleted while a star serves", with(map[string]string{CapabilitiesData: "<del>"}), 1, "declares none"},
		{"every export deleted", with(map[string]string{CapabilitiesServedDir + "/a.json": "<del>"}), 1, "no served-verbs export is committed"},
		{"export malformed", with(map[string]string{CapabilitiesServedDir + "/a.json": "{"}), 1, "not a served-verbs export"},
		{"export from another star", with(map[string]string{CapabilitiesServedDir + "/b.json": served}), 1, `names star "a" but is committed as "b"`},
		{"document malformed", with(map[string]string{CapabilitiesData: "["}), 1, "not a capabilities document"},
		{"record unreadable json", with(map[string]string{"fleet/repos/a.json": "["}), 2, "is not JSON"},
		{"declaration beside the path the gate reads", map[string]string{CapabilitiesDir + "/stars/data.json": decl, "fleet/repos/a.json": rec}, 1, "not " + CapabilitiesData},
		{"document with no stars and no exports", map[string]string{CapabilitiesData: `{"tx":1}`}, 1, `no "stars" object`},
		{"document with null stars", map[string]string{CapabilitiesData: `{"stars":null}`}, 1, `no "stars" object`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := GradeCapabilitiesServed(a, fakeTree(tc.files))
			said := v.Reason + "\n" + strings.Join(v.Logs, "\n")
			if v.State != tc.state || !strings.Contains(said, tc.needle) {
				t.Errorf("state %d (want %d): %s\nwant %q", v.State, tc.state, said, tc.needle)
			}
		})
	}
	// A file the glob lists but the read refuses is a 2, never a pass.
	tr := fakeTree(full)
	tr.Read = func(p string) (string, error) {
		if strings.HasPrefix(p, CapabilitiesServedDir) {
			return "", os.ErrPermission
		}
		return full[p], nil
	}
	if v := GradeCapabilitiesServed(a, tr); v.State != 2 {
		t.Errorf("unreadable export graded %d: %s", v.State, v.Reason)
	}
	// So is the document, or a star's record, that the glob lists and the read refuses.
	for _, refused := range []string{CapabilitiesData, "fleet/repos/a.json"} {
		tr := fakeTree(full)
		tr.Read = func(p string) (string, error) {
			if p == refused {
				return "", os.ErrPermission
			}
			return full[p], nil
		}
		if v := GradeCapabilitiesServed(a, tr); v.State != 2 || !strings.Contains(v.Reason, "could not be read") {
			t.Errorf("unreadable %s graded %d: %s", refused, v.State, v.Reason)
		}
	}
}
