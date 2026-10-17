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
	m := described2(t, goDef())
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
