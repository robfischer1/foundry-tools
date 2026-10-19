package goregen

import (
	"dagger/foundry-tools/internal/regenkit"
	"strings"
	"testing"
)

// alarmLike is a provenance.json in the shape of the composed cores: two-space
// indent, a note with braces and quotes in it, and every block the rewrite reads.
const alarmLike = `{
  "tape": {
    "path": "conformance/tapes/alarm.json",
    "envelope": "{\"substrate\",\"provenance\",\"cases\"}",
    "sha256": "1111111111111111111111111111111111111111111111111111111111111111",
    "bytes": 7
  },
  "core": {
    "repo": "rob/stellar-core-rust",
    "commit": "f6106f92ff44a4860b12e1c3cbbaaa971a2f29d7",
    "build": "tools/compose/compose.sh OUT.wasm identity door (cargo build)",
    "toolchain": "rustc 1.97.1 (8bab26f4f 2026-07-14), wasm-tools 1.261.0, wac 0.12.0"
  },
  "generator": {
    "repo": "forks/gravity",
    "commit": "aeb03bfd4a6f14e29b1a91dcdfad05e02695db39",
    "command": "gravity --name door-core --package door --gofmt --output ./zz_generated.go ./guest.wasm"
  },
  "guest_wasm": {
    "file": "guest.wasm",
    "sha256": "25669e54a82bf93d443b25916c209bc5612f2cfed6726f0c714a6cb9d8694583",
    "bytes": 216841
  },
  "embedded_wasm": {
    "file": "door_core_1.wasm",
    "sha256": "eb6836bd19567605a5e36f62ce9aa730c6919b199b995f1cdf55746cbc1ec20f",
    "bytes": 80500
  },
  "embedded_wasm_identity": {
    "file": "door_core_0.wasm",
    "sha256": "29bee08b7f5c7b72e212326747ee95163abfa62f110d7562d315365fd4b8753f",
    "bytes": 118235
  },
  "bindings": {
    "file": "zz_generated.go",
    "sha256": "fa21e0704b947c5e9338f0182f9da07c584c5bf71b5c954d7d9eebb4428f9479",
    "bytes": 120774,
    "note": "what the generator writes {braces} and \"quotes\" and a \\ slash"
  }
}
`

func TestParseProvReadsTheFieldsTheRegenerationNeeds(t *testing.T) {
	p, err := ParseProv(alarmLike)
	if err != nil {
		t.Fatal(err)
	}
	if p.CoreCommit != "f6106f92ff44a4860b12e1c3cbbaaa971a2f29d7" || !strings.HasPrefix(p.CoreBuild, "tools/compose/compose.sh") ||
		p.CoreToolchain != "rustc 1.97.1 (8bab26f4f 2026-07-14), wasm-tools 1.261.0, wac 0.12.0" ||
		p.GenCommit != "aeb03bfd4a6f14e29b1a91dcdfad05e02695db39" || !strings.HasPrefix(p.GenCommand, "gravity --name door-core") {
		t.Errorf("%+v", p)
	}
	if p.Guest != (Rec{Key: "guest_wasm", File: "guest.wasm", SHA256: "25669e54a82bf93d443b25916c209bc5612f2cfed6726f0c714a6cb9d8694583", Bytes: 216841}) {
		t.Errorf("%+v", p.Guest)
	}
	if len(p.Embedded) != 2 || p.Embedded[0].Key != "embedded_wasm" || p.Embedded[1].Key != "embedded_wasm_identity" || p.Embedded[1].File != "door_core_0.wasm" {
		t.Errorf("%+v", p.Embedded)
	}
	if p.Bindings == nil || p.Bindings.File != "zz_generated.go" || p.Bindings.Bytes != 120774 {
		t.Errorf("%+v", p.Bindings)
	}
	if got := len(p.Records()); got != 4 {
		t.Errorf("%d records", got)
	}
}

func TestParseProvAnswersWithoutBindingsAndRefusesWhatIsNotAGravityCore(t *testing.T) {
	older := strings.Replace(alarmLike[:strings.Index(alarmLike, `  "bindings"`)], "  },\n  \"embedded_wasm_identity\"", "  },\n  \"embedded_wasm_identity\"", 1)
	older = strings.TrimSuffix(strings.TrimRight(older, "\n"), ",") + "\n}\n"
	p, err := ParseProv(older)
	if err != nil || p.Bindings != nil || len(p.Records()) != 3 {
		t.Errorf("%+v %v", p, err)
	}
	for name, text := range map[string]string{
		"not json":                       "{",
		"no core commit":                 `{"core":{},"generator":{"commit":"x","command":"gravity"},"guest_wasm":{"file":"guest.wasm","sha256":"a","bytes":1}}`,
		"no generator":                   `{"core":{"commit":"x"},"guest_wasm":{"file":"guest.wasm","sha256":"a","bytes":1}}`,
		"no guest":                       `{"core":{"commit":"x"},"generator":{"commit":"x","command":"gravity"}}`,
		"no embedded":                    `{"core":{"commit":"x"},"generator":{"commit":"x","command":"gravity"},"guest_wasm":{"file":"guest.wasm","sha256":"a","bytes":1}}`,
		"a record that is not an object": `{"core":{"commit":"x"},"generator":{"commit":"x","command":"gravity"},"guest_wasm":3,"embedded_wasm":{"file":"a","sha256":"a","bytes":1}}`,
	} {
		if _, err := ParseProv(text); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestRewriteWrapsRegenkitsAndLeavesUnchangedInputsAlone(t *testing.T) {
	p, _ := ParseProv(alarmLike)
	same, err := Rewrite(alarmLike, p.Records())
	if err != nil || same != alarmLike {
		t.Fatalf("unchanged inputs rewrote the file: %v\n%s", err, same)
	}
	recs := p.Records()
	recs[0].SHA256, recs[0].Bytes = strings.Repeat("a", 64), 12
	got, err := Rewrite(alarmLike, recs)
	if err != nil || !strings.Contains(got, strings.Repeat("a", 64)) || !strings.Contains(got, `"bytes": 12`) {
		t.Errorf("%v\n%s", err, got)
	}
}

func TestRecordedCargoHomeReadsTheRegistryPathOutOfTheGuest(t *testing.T) {
	std := "index.crates.io-1949cf8c6b5b557f"
	guest := func(paths ...string) []byte {
		return []byte("\x00\x01crates/x/src/lib.rs\x00" + strings.Join(paths, "\x00") + "\x00/rustc/abc/library/core/src/fmt.rs\x00")
	}
	home, err := RecordedCargoHome(guest("/home/rob/.cargo/registry/src/"+std+"/serde_json-1.0.154/src/read.rs", "/home/rob/.cargo/registry/src/"+std+"/itoa-1.0.18/src/lib.rs"))
	if err != nil || home != "/home/rob/.cargo" {
		t.Errorf("%q %v", home, err)
	}
	if home, err = RecordedCargoHome(guest()); err != nil || home != "" {
		t.Errorf("a guest with no registry path: %q %v", home, err)
	}
	_, err = RecordedCargoHome(guest("/home/rob/.cargo/registry/src/"+std+"/a-1/src/lib.rs", "/usr/local/cargo/registry/src/"+std+"/b-1/src/lib.rs"))
	if err == nil || !strings.Contains(err.Error(), "/home/rob/.cargo") || !strings.Contains(err.Error(), "/usr/local/cargo") {
		t.Errorf("two homes: %v", err)
	}
}

func TestAProvenanceWhoseBlocksAreNotRecordsIsRefused(t *testing.T) {
	const head = `{"core":{"commit":"x"},"generator":{"commit":"x","command":"gravity"},"guest_wasm":{"file":"g","sha256":"a","bytes":1},`
	for name, text := range map[string]string{
		"a core that is a string":      `{"core":"x","generator":{"commit":"x","command":"gravity"},"guest_wasm":{},"embedded_wasm":{}}`,
		"an embedded that is a number": head + `"embedded_wasm":3}`,
		"a bindings that is a number":  head + `"embedded_wasm":{"file":"e","sha256":"a","bytes":1},"bindings":3}`,
	} {
		if _, err := ParseProv(text); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	if _, _, ok := regenkit.ObjectSpan(`{"guest`, "guest_wasm"); ok {
		t.Error("an unterminated key was read")
	}
}
