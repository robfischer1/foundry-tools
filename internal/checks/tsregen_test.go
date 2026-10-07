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
	cases := map[string]string{
		"not json":          "{",
		"no wit path":       strings.Replace(goodProv(), `"path":"wit/a.wit",`, "", 1),
		"no wit sha":        strings.Replace(goodProv(), `"sha256":"w"`, `"x":1`, 1),
		"no tape path":      strings.Replace(goodProv(), `"path":"t.json",`, "", 1),
		"no tape sha":       strings.Replace(goodProv(), `"sha256":"t"`, `"x":1`, 1),
		"no toolchain":      strings.Replace(goodProv(), `"toolchain":"rustc 1.90.0"`, `"x":1`, 1),
		"no wasm-tools":     strings.Replace(goodProv(), `"wasm-tools":"1.250.0",`, "", 1),
		"no jco":            strings.Replace(goodProv(), `"jco":"1.35.0"`, `"x":1`, 1),
		"no guest sha":      strings.Replace(goodProv(), `"guest_wasm":{"sha256":"g"}`, `"guest_wasm":{}`, 1),
		"no component sha":  strings.Replace(goodProv(), `"component_wasm":{"sha256":"c"}`, `"component_wasm":{}`, 1),
		"short wit commit":  strings.Replace(goodProv(), `"wit":{"commit":"`+c40+`"`, `"wit":{"commit":"bbf2825"`, 1),
		"tape no commit":    strings.Replace(goodProv(), `"tape":{"commit":"`+c40+`",`, `"tape":{`, 1),
		"core no commit":    strings.Replace(goodProv(), `"core":{"commit":"`+c40+`",`, `"core":{`, 1),
		"result bad commit": strings.Replace(goodProv(), `"wit_result":{"commit":"`+c40+`"`, `"wit_result":{"commit":"x"`, 1),
		"result no path":    strings.Replace(goodProv(), `"path":"wit/r.wit",`, "", 1),
		"result no sha":     strings.Replace(goodProv(), `,"sha256":"r"`, "", 1),
	}
	for name, body := range cases {
		if body == goodProv() {
			t.Fatalf("%s: the fixture did not change", name)
		}
		if _, err := ParseRegenProv(body); err == nil {
			t.Errorf("%s: want an error", name)
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

func TestRegenWorldEnvelope(t *testing.T) {
	if got := RegenWorldEnvelope("stamp", 12); !strings.Contains(got, "regen-check(stamp): PASS") || !strings.Contains(got, "12 pins") {
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
		"no path":      a + "  ",
		"noise":        "sha256sum: /f/x: No such file or directory",
	} {
		if _, err := ParseSha256sum(out); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
