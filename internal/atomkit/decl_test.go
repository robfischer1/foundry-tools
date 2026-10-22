package atomkit

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/checks"
)

func passCheck(context.Context, In) checks.Verdict { return checks.Verdict{Result: "pass"} }

// goDef is UT-467's declaration.
func goDef() Def {
	return Def{ID: "st:x", Stage: "precommit", Lane: checks.LaneGo, Surface: "root:go.mod", Toolchain: "go", Check: passCheck}
}

// keysInOrder are an object's top-level keys in document order.
func keysInOrder(t *testing.T, b []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

func described2(t *testing.T, d Def) map[string]any {
	t.Helper()
	b, err := Describe(d)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUT_467_DescribeKeysAndDefaults(t *testing.T) {
	b, err := Describe(goDef())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"id", "stage", "lane", "group", "desc", "surface", "toolchain", "needs", "timeout_s", "history", "writes_tree", "subsumed_by", "not_on_forks", "absent_reason", "fork_reason"}
	if got := keysInOrder(t, b); !slices.Equal(got, want) {
		t.Fatalf("keys %v", got)
	}
	m := described2(t, goDef())
	if !reflect.DeepEqual(m["needs"], []any{}) || m["timeout_s"] != float64(600) || m["history"] != "shallow" || m["writes_tree"] != false || m["fork_reason"] != "" {
		t.Fatalf("%v", m)
	}
}

func TestUT_468_DescribeSortsNeedsAndKeepsSettings(t *testing.T) {
	d := goDef()
	d.Needs, d.Timeout, d.History = []string{"tool:z", "dies"}, 90*time.Second, "full"
	m := described2(t, d)
	if !reflect.DeepEqual(m["needs"], []any{"dies", "tool:z"}) || m["timeout_s"] != float64(90) || m["history"] != "full" {
		t.Fatalf("%v", m)
	}
	if d.Needs[0] != "tool:z" {
		t.Fatal("Describe sorted the caller's slice")
	}
}

func TestUT_469_471_DescribeAbsentReason(t *testing.T) {
	for _, c := range []struct {
		row  string
		d    Def
		want string
	}{
		{"UT-469", goDef(), "st:x: ABSENT — no go.mod anywhere in the tree (vendor/, testdata/ and _ or . directories do not count), so this repo carries no go to check. Nothing was checked and nothing needed to be."},
		{"UT-471", Def{ID: "ts:visual", Stage: "visual", Lane: checks.LaneTS, NeedsManifest: true, Surface: "root:package.json", Toolchain: "bun", Check: passCheck},
			"ts:visual: ABSENT — no package.json at the repository root, so this repo declares no ts project for it to read. The lane's lint and tests still run. Nothing was checked here and nothing needed to be."},
	} {
		if got := described2(t, c.d)["absent_reason"]; got != c.want {
			t.Errorf("%s: %q", c.row, got)
		}
	}
}

func TestUT_470_DescribeForkReason(t *testing.T) {
	d := goDef()
	d.NotOnForks = checks.ForkFleetStyle
	want := "st:x: ABSENT — fleet/repos/{record}.json is kind vendored, a fork of an upstream, and this atom does not grade a fork: it holds code to the fleet's own lint set, style and conventions, which upstream-owned code does not follow. The fork's build, tests and audits still run. Nothing was checked here and nothing needed to be."
	m := described2(t, d)
	if m["fork_reason"] != want || m["not_on_forks"] != checks.ForkFleetStyle {
		t.Fatalf("%v", m)
	}
}

func TestUT_472_AnyLaneIsTheEmptyString(t *testing.T) {
	d := Def{ID: "fleet:x", Lane: checks.LaneAny, Surface: "any:**", Toolchain: "none", Stage: "precommit", Check: passCheck}
	if m := described2(t, d); m["lane"] != "" {
		t.Fatalf("lane %v", m["lane"])
	}
}

func TestUT_473_DescribeRefusesAnInvalidDef(t *testing.T) {
	b, err := Describe(Def{ID: "bad"})
	if b != nil || err == nil || err.Error() != "id bad is not <ns>:<name>" {
		t.Fatalf("%q %v", b, err)
	}
}

// TestIT_124_DescribeKeysAreAnIndexEntrys: the keys Describe prints are an
// atom-index@1 entry's, less the four the builder fills, plus desc: FN-082
// prints it and daedalus' IndexAtom (and so atom-index.json) has no such field
// (proposed DEF-B3-?).
func TestIT_124_DescribeKeysAreAnIndexEntrys(t *testing.T) {
	var want []string
	typ := reflect.TypeOf(IndexAtom{})
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if !slices.Contains([]string{"kind", "digest", "manifest", "binary_sha256"}, name) {
			want = append(want, name)
		}
	}
	want = append(want, "desc")
	// an omitempty key (reasks_own, after_any) is printed only when set, so the
	// declaration sets every one of them
	d := goDef()
	d.ReasksOwn, d.AfterAny = true, []string{"st:y"}
	m := described2(t, d)
	got := make([]string, 0, len(m))
	for k := range m {
		got = append(got, k)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestUT_474_ValidateID(t *testing.T) {
	for _, c := range []struct{ id, want string }{
		{"", "id  is not <ns>:<name>"},
		{"Fleet:x", "id Fleet:x is not <ns>:<name>"},
		{"fleet:x", ""},
	} {
		d := goDef()
		d.ID = c.id
		if got := errText(Validate(d)); got != c.want {
			t.Errorf("%q: %q", c.id, got)
		}
	}
}

func TestValidateRules(t *testing.T) {
	for _, c := range []struct {
		row, want string
		mut       func(*Def)
	}{
		{"UT-475", "stage precommmit is unknown", func(d *Def) { d.Stage = "precommmit" }},
		{"UT-475", "stage govern is unknown", func(d *Def) { d.Stage = "govern" }},
		{"UT-475", "", func(d *Def) { d.Stage = "dev" }},
		{"UT-476", "surface glob:*.go is not root:, any: or all:", func(d *Def) { d.Surface = "glob:*.go" }},
		{"UT-476", "surface any: is not root:, any: or all:", func(d *Def) { d.Surface = "any:" }},
		{"UT-476", "", func(d *Def) { d.Surface = "all:go.mod,go.sum" }},
		{"UT-477", "toolchain is empty", func(d *Def) { d.Toolchain = "" }},
		{"UT-478", "need net is not dies, svid, pytools, tool:<name> or service:<name>", func(d *Def) { d.Needs = []string{"net"} }},
		{"UT-478", "", func(d *Def) { d.Needs = []string{"dies", "svid", "pytools", "tool:uv", "service:postgres"} }},
		{"UT-479", "history deep is not shallow or full", func(d *Def) { d.History = "deep" }},
		{"UT-479", "", func(d *Def) { d.History = "full" }},
		{"UT-480", "not_on_forks x is not one of checks.ForkReasons", func(d *Def) { d.NotOnForks = "x" }},
		{"UT-480", "", func(d *Def) { d.NotOnForks = checks.ForkChangeBar }},
		{"UT-481", "check is nil", func(d *Def) { d.Check = nil }},
	} {
		d := goDef()
		c.mut(&d)
		if got := errText(Validate(d)); got != c.want {
			t.Errorf("%s: want %q, got %q", c.row, c.want, got)
		}
	}
}

// TestIT_128_130_DescribeExitsTwo: an invalid declaration cannot be described
// by the binary; it exits 2.
func TestIT_128_130_DescribeExitsTwo(t *testing.T) {
	for name, d := range map[string]Def{
		"IT-128 empty id": {},
		"IT-129 stage":    {ID: "st:x", Stage: "precommmit", Surface: "root:go.mod", Toolchain: "go", Check: passCheck},
		"IT-130 surface":  {ID: "st:x", Stage: "precommit", Surface: "glob:*.go", Toolchain: "go", Check: passCheck},
	} {
		out, errOut := captureStreams(t)
		if code := run(d, []string{"atom", "--describe"}); code != 2 || out.Len() != 0 || errOut.Len() == 0 {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, out, errOut)
		}
	}
}

// An atom that re-asks its own work says so in its index entry, so a runner that
// re-asks a could-not-run atom whole leaves it alone; every other entry is
// unchanged (the field is absent unless set).
func TestDescribeCarriesReasksOwnOnlyWhenSet(t *testing.T) {
	plain, err := Describe(goDef())
	if err != nil || strings.Contains(string(plain), "reasks_own") {
		t.Fatalf("%s %v", plain, err)
	}
	d := goDef()
	d.ReasksOwn = true
	own, err := Describe(d)
	if err != nil || !strings.Contains(string(own), `"reasks_own":true`) {
		t.Fatalf("%s %v", own, err)
	}
}

// AN ATOM THAT FOLLOWS ANOTHER ON ANY VERDICT says so in its index entry, as
// daedalus's IndexAtom reads it (after_any, AFA1); every other entry is
// unchanged, the field absent unless set.
func TestDescribeCarriesAfterAnyOnlyWhenSet(t *testing.T) {
	plain, err := Describe(goDef())
	if err != nil || strings.Contains(string(plain), "after_any") {
		t.Fatalf("%s %v", plain, err)
	}
	d := goDef()
	d.AfterAny = []string{"go:test-race", "go:build"}
	got, err := Describe(d)
	if err != nil || !strings.Contains(string(got), `"after_any":["go:test-race","go:build"]`) {
		t.Fatalf("%s %v", got, err)
	}
	// the entry's own slice, not the declaration's
	var e described
	if err := json.Unmarshal(got, &e); err != nil || !slices.Equal(e.AfterAny, d.AfterAny) {
		t.Fatalf("%+v %v", e, err)
	}
}

// AN after_any EDGE NAMES ANOTHER ATOM, ONCE, BY ITS ID: an atom that followed
// itself would wait on its own verdict to the pod's deadline.
func TestValidateRefusesABadAfterAny(t *testing.T) {
	for name, tc := range map[string]struct {
		after []string
		want  string
	}{
		"itself":    {[]string{"st:x"}, "after_any names the atom itself, st:x"},
		"not an id": {[]string{"test-race"}, "after_any test-race is not <ns>:<name>"},
		"twice":     {[]string{"go:a", "go:b", "go:a"}, "after_any names go:a twice"},
	} {
		t.Run(name, func(t *testing.T) {
			d := goDef()
			d.AfterAny = tc.after
			if err := Validate(d); err == nil || err.Error() != tc.want {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
	d := goDef()
	d.AfterAny = []string{"go:a", "go:b"}
	if err := Validate(d); err != nil {
		t.Errorf("two distinct edges: %v", err)
	}
}

// THE CATALOGUE REFUSES A CYCLE OF after_any EDGES: daedalus orders by them and
// does not check them, and every atom on a cycle would wait to the deadline.
func TestAfterAnyCycle(t *testing.T) {
	def := func(id string, after ...string) *Def { return &Def{ID: id, AfterAny: after} }
	for name, tc := range map[string]struct {
		defs []*Def
		want string
	}{
		"none":              {[]*Def{def("a:a"), def("a:b", "a:a")}, ""},
		"an unknown target": {[]*Def{def("a:b", "a:z")}, ""},
		"a chain":           {[]*Def{def("a:c", "a:b"), def("a:b", "a:a"), def("a:a")}, ""},
		"a diamond":         {[]*Def{def("a:d", "a:b", "a:c"), def("a:b", "a:a"), def("a:c", "a:a"), def("a:a")}, ""},
		"two":               {[]*Def{def("a:b", "a:a"), def("a:a", "a:b")}, "a:a -> a:b -> a:a"},
		"three":             {[]*Def{def("a:c", "a:a"), def("a:b", "a:c"), def("a:a", "a:b")}, "a:a -> a:b -> a:c -> a:a"},
		"behind a tail":     {[]*Def{def("a:a", "a:b"), def("a:b", "a:c"), def("a:c", "a:b")}, "a:b -> a:c -> a:b"},
		"itself":            {[]*Def{def("a:a", "a:a")}, "a:a -> a:a"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := AfterAnyCycle(tc.defs); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
