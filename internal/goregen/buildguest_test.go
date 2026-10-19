package goregen

import (
	"reflect"
	"strings"
	"testing"
)

const plainScript = `#!/bin/sh
# Build the core module this package embeds.
set -eu
REV=f6106f92ff44a4860b12e1c3cbbaaa971a2f29d7
OUT=$(realpath -m "$1")
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
git clone --quiet https://git.notusmi.com/stellar-core-rust.git "$TMP/src"
git -C "$TMP/src" checkout --quiet "$REV"
(cd "$TMP/src" && cargo build --locked --release --no-default-features --features wit-guest-alarm --target wasm32-unknown-unknown)
cp "$TMP/src/target/wasm32-unknown-unknown/release/stellar_core.wasm" "$OUT"
`

const composeScript = `#!/bin/sh
# composed through tools/compose/compose.sh (one world in the composition)
set -eu
REV=f6106f92ff44a4860b12e1c3cbbaaa971a2f29d7
OUT=$(realpath -m "$1")
(cd "$TMP/src" && tools/compose/compose.sh "$TMP/composed.wasm" identity door >/dev/null)
cp "$TMP/composed.wasm" "$OUT"
`

func TestParseBuildGuestReadsThePlainAndTheComposedForms(t *testing.T) {
	plain, err := ParseBuildGuest(plainScript)
	if err != nil || plain.Rev != "f6106f92ff44a4860b12e1c3cbbaaa971a2f29d7" || plain.Compose != nil ||
		!reflect.DeepEqual(plain.Cargo, []string{"cargo", "build", "--locked", "--release", "--no-default-features", "--features", "wit-guest-alarm", "--target", "wasm32-unknown-unknown"}) ||
		plain.Artifact != "wasm32-unknown-unknown/release/stellar_core.wasm" {
		t.Errorf("%+v %v", plain, err)
	}
	composed, err := ParseBuildGuest(composeScript)
	if err != nil || composed.Rev != "f6106f92ff44a4860b12e1c3cbbaaa971a2f29d7" || !reflect.DeepEqual(composed.Compose, []string{"identity", "door"}) || composed.Cargo != nil {
		t.Errorf("%+v %v", composed, err)
	}
	one, err := ParseBuildGuest(strings.Replace(composeScript, "identity door", "observability-api", 1))
	if err != nil || !reflect.DeepEqual(one.Compose, []string{"observability-api"}) {
		t.Errorf("%+v %v", one, err)
	}
}

func TestParseBuildGuestRefusesAScriptItCannotRun(t *testing.T) {
	for name, tc := range map[string]struct{ text, says string }{
		"no REV":                     {strings.Replace(plainScript, "REV=f6106f92ff44a4860b12e1c3cbbaaa971a2f29d7\n", "", 1), "no REV="},
		"a short REV":                {strings.Replace(plainScript, "f6106f92ff44a4860b12e1c3cbbaaa971a2f29d7", "f6106f9", 1), "no REV="},
		"two REVs":                   {plainScript + "REV=0123456789abcdef0123456789abcdef01234567\n", "two REV="},
		"no build at all":            {"#!/bin/sh\nREV=f6106f92ff44a4860b12e1c3cbbaaa971a2f29d7\n", "neither tools/compose/compose.sh nor cargo build"},
		"both builds":                {composeScript + "(cd x && cargo build --locked)\n", "both tools/compose/compose.sh and cargo build"},
		"a compose with no world":    {strings.Replace(composeScript, "identity door ", "", 1), "names no world"},
		"a plain build with no copy": {strings.Replace(plainScript, "cp \"$TMP/src/target/wasm32-unknown-unknown/release/stellar_core.wasm\" \"$OUT\"\n", "", 1), "copies no artifact"},
	} {
		if _, err := ParseBuildGuest(tc.text); err == nil || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: %v, want %q", name, err, tc.says)
		}
	}
}

func TestACommentThatNamesComposeIsNotABuild(t *testing.T) {
	text := "# built with tools/compose/compose.sh \"x\" y\n" + plainScript
	g, err := ParseBuildGuest(text)
	if err != nil || g.Compose != nil || g.Cargo == nil {
		t.Errorf("%+v %v", g, err)
	}
}

func TestRenderBuildGuestIsTheComposedFormWithTheCommitSet(t *testing.T) {
	w := World{Pkg: "sqlport", Name: "sqlport-core", Version: "0.2.0"}
	text := RenderBuildGuest(w, "0123456789abcdef0123456789abcdef01234567", "1.262.0", "0.12.0")
	if !strings.HasPrefix(text, "#!/bin/sh\n") || !strings.Contains(text, "wasm-tools 1.262.0 and wac 0.12.0") ||
		!strings.Contains(text, "aiws:sqlport world (feature wit-guest-sqlport)") || !strings.Contains(text, `. "$(dirname "$0")/../tools/pin-rustc.sh"`) {
		t.Errorf("\n%s", text)
	}
	g, err := ParseBuildGuest(text)
	if err != nil || g.Rev != "0123456789abcdef0123456789abcdef01234567" || !reflect.DeepEqual(g.Compose, []string{"sqlport"}) {
		t.Errorf("a script it wrote does not read back: %+v %v", g, err)
	}
}

func TestRenderProvenanceWritesTheBlocksItCanDeriveAndNamesTheRest(t *testing.T) {
	w := World{Pkg: "sqlport", Name: "sqlport-core", Version: "0.2.0"}
	in := NewProv{
		Core: "sqlport", World: w, Commit: "0123456789abcdef0123456789abcdef01234567", Rev: "v0.78.0",
		Toolchain: "rustc 1.99.0 (b940084d7 2026-09-28), wasm-tools 1.262.0, wac 0.12.0",
		GenCommit: "aeb03bfd4a6f14e29b1a91dcdfad05e02695db39",
		WitSHA256: strings.Repeat("c", 64),
		Guest:     Rec{File: "guest.wasm", SHA256: strings.Repeat("a", 64), Bytes: 11},
		Embedded:  Rec{File: "sqlport_core_0.wasm", SHA256: strings.Repeat("b", 64), Bytes: 22},
		Bindings:  Rec{File: "zz_generated.go", SHA256: strings.Repeat("d", 64), Bytes: 33},
	}
	text := RenderProvenance(in)
	if !strings.HasSuffix(text, "}\n") || !strings.HasPrefix(text, "{\n  \"wit\": {") {
		t.Errorf("\n%s", text)
	}
	p, err := ParseProv(text)
	if err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	if p.CoreCommit != in.Commit || p.GenCommit != in.GenCommit || p.GenCommand != "gravity --name sqlport-core --package sqlport --gofmt --output ./zz_generated.go ./guest.wasm" ||
		p.CoreBuild != "tools/compose/compose.sh OUT.wasm sqlport (cargo build --locked --release --no-default-features --features wit-guest-sqlport --target wasm32-unknown-unknown, wasm-tools component new, wac compose)" ||
		p.CoreToolchain != in.Toolchain || p.Guest.SHA256 != in.Guest.SHA256 || len(p.Embedded) != 1 || p.Embedded[0].File != "sqlport_core_0.wasm" || p.Bindings == nil || p.Bindings.Bytes != 33 {
		t.Errorf("%+v", p)
	}
	for _, want := range []string{`"path": "wit/aiws-sqlport.wit"`, `"sha256": "` + strings.Repeat("c", 64) + `"`, `"commit": ""`, "aiws:sqlport@0.2.0", "v0.78.0"} {
		if !strings.Contains(text, want) {
			t.Errorf("provenance.json lacks %s:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"tape"`) {
		t.Errorf("a tape block was invented:\n%s", text)
	}
	// and rewriting what it wrote with the same records changes nothing
	if again, err := Rewrite(text, p.Records()); err != nil || again != text {
		t.Errorf("the rewrite of a fresh provenance moved it: %v", err)
	}
}

func TestLeftForAHumanNamesWhatTheRegenerationCouldNotDerive(t *testing.T) {
	wit := "package aiws:sqlport@0.2.0;\nuse aiws:%result/types.{refusal};\ninterface sqlport { use aiws:identity/types@0.5.0.{principal}; }\nworld sqlport-core { export sqlport; }\n"
	got := LeftForAHuman(wit, "sqlport")
	want := []string{"wit.commit", "tape", "wit_result (the world uses aiws:%result)", "wit_identity (the world uses aiws:identity)"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%q, want %q", got, want)
	}
	if got := LeftForAHuman("package aiws:x@0.1.0;\n", "x"); !reflect.DeepEqual(got, []string{"wit.commit", "tape"}) {
		t.Errorf("%q", got)
	}
}

func TestAComposeLineStopsAtTheFirstWordThatIsNotAWorld(t *testing.T) {
	text := strings.Replace(composeScript, "identity door >/dev/null", "identity --verbose door", 1)
	g, err := ParseBuildGuest(text)
	if err != nil || !reflect.DeepEqual(g.Compose, []string{"identity"}) {
		t.Errorf("%+v %v", g, err)
	}
}
