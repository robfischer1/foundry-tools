package checks

import (
	"strings"
	"testing"
)

func TestRegenWorldsReadsTheRecipesCallsOnce(t *testing.T) {
	just := "regen-check:\n    scripts/regen-check.sh classes\n    scripts/regen-check.sh circuit  \n\tscripts/regen-check.sh classes\n" +
		"# scripts/regen-check.sh commented\nscripts/regen-check.sh flush\n    scripts/regen-check.sh Bad\n    scripts/regen-check.sh two words\n    scripts/regen-check.sh fade2\n"
	got := RegenWorlds(just)
	want := []string{"classes", "circuit", "fade2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := RegenWorlds(""); len(got) != 0 {
		t.Fatalf("empty: %v", got)
	}
}

const c40 = "bbf2825c710bca00df747d477865f256e14abea7"

func goodProv() string {
	return `{
 "wit":{"commit":"` + c40 + `","path":"wit/a.wit","sha256":"w"},
 "wit_result":{"commit":"` + c40 + `","path":"wit/r.wit","sha256":"r"},
 "tape":{"commit":"` + c40 + `","path":"t.json","sha256":"t"},
 "core":{"commit":"` + c40 + `","toolchain":"rustc 1.90.0"},
 "tools":{"wasm-tools":"1.250.0","jco":"1.35.0"},
 "guest_wasm":{"sha256":"g"},"component_wasm":{"sha256":"c"}}`
}

func TestParseRegenProvReadsEveryPin(t *testing.T) {
	p, err := ParseRegenProv(goodProv())
	if err != nil {
		t.Fatal(err)
	}
	if p.WIT.Path != "wit/a.wit" || p.WIT.SHA256 != "w" || p.WITResult == nil || p.WITResult.Path != "wit/r.wit" ||
		p.Tape.SHA256 != "t" || p.Core.Commit != c40 || p.Core.Toolchain != "rustc 1.90.0" ||
		p.Tools.WasmTools != "1.250.0" || p.Tools.JCO != "1.35.0" || p.GuestWasm.SHA256 != "g" || p.ComponentWasm.SHA256 != "c" {
		t.Fatalf("misread: %+v", p)
	}
}

func TestParseRegenProvResultIsOptional(t *testing.T) {
	body := strings.Replace(goodProv(), `"wit_result":{"commit":"`+c40+`","path":"wit/r.wit","sha256":"r"},`, "", 1)
	p, err := ParseRegenProv(body)
	if err != nil || p.WITResult != nil {
		t.Fatalf("%+v, %v", p, err)
	}
}

func TestParseRegenProvRefusesWhatItCannotRegenerateFrom(t *testing.T) {
	type c struct{ body, want string }
	cases := map[string]c{
		"not json":          {"{", "provenance.json is not JSON"},
		"no wit path":       {strings.Replace(goodProv(), `"path":"wit/a.wit",`, "", 1), "pins no wit.path"},
		"no wit sha":        {strings.Replace(goodProv(), `"sha256":"w"`, `"x":1`, 1), "pins no wit.sha256"},
		"no tape path":      {strings.Replace(goodProv(), `"path":"t.json",`, "", 1), "pins no tape.path"},
		"no tape sha":       {strings.Replace(goodProv(), `"sha256":"t"`, `"x":1`, 1), "pins no tape.sha256"},
		"no toolchain":      {strings.Replace(goodProv(), `"toolchain":"rustc 1.90.0"`, `"x":1`, 1), "pins no core.toolchain"},
		"no wasm-tools":     {strings.Replace(goodProv(), `"wasm-tools":"1.250.0",`, "", 1), "pins no tools.wasm-tools"},
		"no jco":            {strings.Replace(goodProv(), `"jco":"1.35.0"`, `"x":1`, 1), "pins no tools.jco"},
		"no guest sha":      {strings.Replace(goodProv(), `"guest_wasm":{"sha256":"g"}`, `"guest_wasm":{}`, 1), "pins no guest_wasm.sha256"},
		"no component sha":  {strings.Replace(goodProv(), `"component_wasm":{"sha256":"c"}`, `"component_wasm":{}`, 1), "pins no component_wasm.sha256"},
		"short wit commit":  {strings.Replace(goodProv(), `"wit":{"commit":"`+c40+`"`, `"wit":{"commit":"bbf2825"`, 1), `wit.commit is "bbf2825", not a 40-hex commit`},
		"tape no commit":    {strings.Replace(goodProv(), `"tape":{"commit":"`+c40+`",`, `"tape":{`, 1), "tape.commit"},
		"core no commit":    {strings.Replace(goodProv(), `"core":{"commit":"`+c40+`",`, `"core":{`, 1), "core.commit"},
		"result bad commit": {strings.Replace(goodProv(), `"wit_result":{"commit":"`+c40+`"`, `"wit_result":{"commit":"x"`, 1), "wit_result.commit"},
		"result no path":    {strings.Replace(goodProv(), `"path":"wit/r.wit",`, "", 1), "wit_result pins no path or sha256"},
		"result no sha":     {strings.Replace(goodProv(), `,"sha256":"r"`, "", 1), "wit_result pins no path or sha256"},
	}
	for name, tc := range cases {
		if tc.body == goodProv() {
			t.Fatalf("%s: the fixture did not change", name)
		}
		if _, err := ParseRegenProv(tc.body); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err %v, want it to contain %q", name, err, tc.want)
		}
	}
}

func TestSHA256HexIsSha256sum(t *testing.T) {
	if got := SHA256Hex("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatal(got)
	}
}

func TestRegenExpect(t *testing.T) {
	ok, problem := RegenExpect("guest", "aa", "aa")
	if ok != "ok  guest aa" || problem != "" {
		t.Fatalf("%q %q", ok, problem)
	}
	ok, problem = RegenExpect("guest", "aa", "bb")
	if ok != "" || problem != "guest: got aa, pinned bb" {
		t.Fatalf("%q %q", ok, problem)
	}
}

func TestRegenToolVersionDropsTheName(t *testing.T) {
	if got := RegenToolVersion("wasm-tools 1.250.0\n"); got != "1.250.0" {
		t.Fatal(got)
	}
	if got := RegenToolVersion("  1.250.0  "); got != "1.250.0" {
		t.Fatal(got)
	}
}

func TestParseSha256sum(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	got, err := ParseSha256sum(a + "  /f/guest\n" + b + "  /f/my file\n")
	if err != nil || got["/f/guest"] != a || got["/f/my file"] != b || len(got) != 2 {
		t.Fatalf("%v, %v", got, err)
	}
	for name, out := range map[string]string{
		"empty":        "",
		"one space":    a + " /f/x",
		"short digest": "abc  /f/x",
		"no path":      a + "  \n" + b + "  /f/x",
		"noise":        "sha256sum: /f/x: No such file or directory",
	} {
		if _, err := ParseSha256sum(out); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestRegenProvListsPinsAndCoreCopiesInTheScriptsOrder(t *testing.T) {
	p, _ := ParseRegenProv(goodProv())
	pins := p.Pins()
	if len(pins) != 3 || pins[0].Label != "wit" || pins[0].Pin.Path != "wit/a.wit" ||
		pins[1].Label != "wit" || pins[1].Pin.Path != "wit/r.wit" ||
		pins[2].Label != "tape" || pins[2].Pin.Path != "t.json" {
		t.Fatalf("%+v", pins)
	}
	copies := p.CoreCopies("stamp")
	if len(copies) != 2 || copies[0] != (CoreCopy{"wit/aiws-stamp.wit", "w"}) || copies[1] != (CoreCopy{"wit/aiws-result.wit", "r"}) {
		t.Fatalf("%+v", copies)
	}
	p.WITResult = nil
	if got := p.Pins(); len(got) != 2 || got[1].Label != "tape" {
		t.Fatalf("%+v", got)
	}
	if got := p.CoreCopies("stamp"); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestRegenCompareKeepsGoingPastTheFirstMiss(t *testing.T) {
	lines, problems := RegenCompare([]RegenProbe{{"a", "1", "1"}, {"b", "2", "9"}, {"c", "3", "3"}, {"d", "4", "8"}})
	if strings.Join(lines, "|") != "ok  a 1|ok  c 3" || strings.Join(problems, "|") != "b: got 2, pinned 9|d: got 4, pinned 8" {
		t.Fatalf("%v %v", lines, problems)
	}
	if l, p := RegenCompare(nil); l != nil || p != nil {
		t.Fatalf("%v %v", l, p)
	}
}

func TestRegenFoldSettlesOnTheLouderCode(t *testing.T) {
	ok := RegenWorldResult{World: "a", Lines: []string{"ok  x 1", "PASS - done"}}
	bad := RegenWorldResult{World: "b", Problems: []string{"guest.wasm: got 1, pinned 2"}}
	dead := RegenWorldResult{World: "c", Err: "engine went away"}

	code, out := RegenFold([]RegenWorldResult{ok})
	if code != 0 || out != "regen-check(a): ok  x 1\nregen-check(a): PASS - done" {
		t.Fatalf("%d %q", code, out)
	}
	code, out = RegenFold([]RegenWorldResult{ok, bad})
	if code != 1 || out != "ts:regen: FINDINGS - a core does not regenerate byte-identical from its pins\n"+
		"regen-check(a): ok  x 1\nregen-check(a): PASS - done\nregen-check(b): FAIL: guest.wasm: got 1, pinned 2" {
		t.Fatalf("%d %q", code, out)
	}
	for _, order := range [][]RegenWorldResult{{bad, dead}, {dead, bad}} {
		code, out = RegenFold(order)
		if code != 2 || !strings.Contains(out, "regen-check(c): CANNOT RUN: engine went away") || !strings.Contains(out, "regen-check(b): FAIL:") ||
			strings.Contains(out, "ts:regen: FINDINGS") {
			t.Fatalf("%d %q", code, out)
		}
	}
	if code, out = RegenFold(nil); code != 0 || out != "" {
		t.Fatalf("%d %q", code, out)
	}
}

func TestPlanGravityIsTheWholeDecision(t *testing.T) {
	plan, err := PlanGravity(map[string]string{"a/provenance.json": prov("gravity --x", rev1)}, []string{"a/regen_test.go"})
	if err != nil || plan.Rev != rev1 || len(plan.Regens) != 1 || !strings.Contains(plan.Scope, "A_REGEN_REQUIRED=1") {
		t.Fatalf("%+v %v", plan, err)
	}
	plan, err = PlanGravity(nil, nil)
	if err != nil || plan.Rev != "" || len(plan.Regens) != 0 || !strings.Contains(plan.Scope, "no package pairs") {
		t.Fatalf("%+v %v", plan, err)
	}
	if _, err = PlanGravity(map[string]string{"a/provenance.json": "{"}, []string{"a/regen_test.go"}); err == nil {
		t.Fatal("bad provenance must be an error")
	}
	if _, err = PlanGravity(map[string]string{
		"a/provenance.json": prov("gravity --x", rev1), "b/provenance.json": prov("gravity --x", rev2),
	}, []string{"a/regen_test.go", "b/regen_test.go"}); err == nil {
		t.Fatal("disagreeing revs must be an error")
	}
}
