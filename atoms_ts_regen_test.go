package main

import (
	"fmt"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

const (
	rgCommit = "bbf2825c710bca00df747d477865f256e14abea7"
	rgGuest  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rgComp   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// regenFixture lays a star whose justfile regenerates one world, its
// provenance, and the engine's answers for every read the atom makes, all
// agreeing with the pins. A test then breaks one.
func regenFixture(world string) {
	engine.reset()
	wit, result, tape := "wit text", "result text", "tape text"
	prov := fmt.Sprintf(`{
 "wit":{"commit":%q,"path":"wit/aiws-%s.wit","sha256":%q},
 "wit_result":{"commit":%q,"path":"wit/aiws-result.wit","sha256":%q},
 "tape":{"commit":%q,"path":"conformance/tapes/%s.json","sha256":%q},
 "core":{"commit":%q,"toolchain":"rustc 1.90.0 (1159e78c4 2025-09-14)"},
 "tools":{"wasm-tools":"1.250.0","jco":"1.35.0"},
 "guest_wasm":{"sha256":%q},"component_wasm":{"sha256":%q}}`,
		rgCommit, world, checks.SHA256Hex(wit),
		rgCommit, checks.SHA256Hex(result),
		rgCommit, world, checks.SHA256Hex(tape),
		rgCommit, rgGuest, rgComp)
	engine.withTree(map[string]string{
		"justfile": "regen-check:\n    scripts/regen-check.sh " + world + "\n",
		"src/" + world + "core-gen/provenance.json": prov,
	})
	engine.contents(`"wit/aiws-`+world+`.wit"`, wit)
	engine.contents(`"wit/aiws-result.wit"`, result)
	engine.contents(`"conformance/tapes/`+world+`.json"`, tape)
	engine.contents(`wasm-tools.version`, "wasm-tools 1.250.0\n")
	engine.contents(`rustc.version`, "rustc 1.90.0 (1159e78c4 2025-09-14)\n")
	engine.contents(`.jco-version`, "1.35.0\n")
	engine.stdout(`"sha256sum"`, rgComp+"  /f/component\n"+rgGuest+"  /f/guest\n")
}

func TestTSRegenHoldsAWorldToItsPinsAndToTheShippedBytes(t *testing.T) {
	regenFixture("stamp")
	v := runAtom(t, "ts:regen", "")
	wantState(t, v, 0)

	// The core is read at its pinned commit from stellar-core-rust, the WIT and
	// tape from stellar-core.
	if engine.chain(checks.TSRegenWitGit, rgCommit, `"conformance/tapes/stamp.json"`) == "" ||
		engine.chain(checks.TSRegenCoreGit, rgCommit, `"wit/aiws-stamp.wit"`, "discardGitDir:true") == "" {
		t.Errorf("the pins are not read where the provenance says:\n%v", engine.chains())
	}
	// The guest is built by the star's own inner script on the toolchain image,
	// with the pinned wasm-tools, and its output read back from /out.
	b := engine.chain(`"sh","/inner.sh"`, "wasm-tools.version")
	if !strings.Contains(b, checks.ImageRustWasm) {
		t.Errorf("the guest builds on the toolchain the provenance records:\n%s", b)
	}
	wantCalls(t, b,
		[]string{"withExec", `"rustup","target","add","wasm32-unknown-unknown"`},
		[]string{"withExec", `"cargo","install","wasm-tools","--locked","--version","1.250.0"`},
		[]string{"withMountedFile", `path:"/inner.sh"`},
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withEnvVariable", `name:"WASM_TOOLS_VERSION"`, `value:"1.250.0"`},
		[]string{"withEnvVariable", `name:"CARGO_TARGET_DIR"`, `value:"/tmp/target"`},
		[]string{"withExec", `"sh","/inner.sh"`},
		[]string{"directory", `path:"/out"`},
	)
	// Transpiled by the star's own script with the pinned jco.
	j := engine.chain(checks.ImageNode, `.jco-version`)
	wantCalls(t, j,
		[]string{"withEnvVariable", `name:"JCO_VERSION"`, `value:"1.35.0"`},
		[]string{"withEnvVariable", `name:"NODE_EXTRA_CA_CERTS"`},
		[]string{"withMountedFile", `path:"/in/stamp.component.wasm"`},
		[]string{"withMountedFile", `path:"/inner.sh"`},
		[]string{"withExec", `"sh","/inner.sh"`},
	)
	// And diffed against the tree, minus the provenance that records the diff.
	d := engine.chain(`"diff","-r"`)
	wantCalls(t, d,
		[]string{"withMountedDirectory", `path:"/gen"`},
		[]string{"withMountedDirectory", `path:"/shipped"`},
		[]string{"withExec", `expect:ANY`, `"diff","-r","-q","-x","provenance.json","-x",".jco-version","/gen","/shipped"`},
	)
	// The wasm is hashed in the engine, in name order, never read as a string.
	wantCalls(t, engine.chain(`"sha256sum"`),
		[]string{"withFile", `path:"/f/component"`}, []string{"withFile", `path:"/f/guest"`},
		[]string{"withExec", `expect:ANY`, `"sha256sum","/f/component","/f/guest"`})
}

func TestTSRegenNamesEveryPinThatMoved(t *testing.T) {
	regenFixture("stamp")
	engine.stdout(`"sha256sum"`, rgComp+"  /f/component\n"+strings.Repeat("c", 64)+"  /f/guest\n")
	engine.contents(`rustc.version`, "rustc 1.91.0\n")
	engine.contents(`"conformance/tapes/stamp.json"`, "a different tape")
	v := runAtom(t, "ts:regen", "")
	wantState(t, v, 1, "FINDINGS - a core does not regenerate byte-identical",
		"regen-check(stamp): FAIL: guest.wasm: got "+strings.Repeat("c", 64)+", pinned "+rgGuest,
		"regen-check(stamp): FAIL: rustc: got rustc 1.91.0, pinned rustc 1.90.0",
		"regen-check(stamp): FAIL: tape conformance/tapes/stamp.json@"+rgCommit,
		"regen-check(stamp): ok  stamp.component.wasm "+rgComp)
}

func TestTSRegenFilesAGeneratedFileThatDiffers(t *testing.T) {
	regenFixture("stamp")
	engine.exitCode(`"diff","-r"`, 1)
	engine.stdout(`"diff","-r"`, "Files /gen/stamp-core.config.js and /shipped/stamp-core.config.js differ")
	wantState(t, runAtom(t, "ts:regen", ""), 1, "regenerated output differs from src/stampcore-gen:",
		"Files /gen/stamp-core.config.js and /shipped/stamp-core.config.js differ")
}

func TestTSRegenIsAbsentWithoutARegenCheckRecipe(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"justfile": "test:\n    bun test\n"})
	v := runAtom(t, "ts:regen", "")
	wantState(t, v, 0, "ABSENT")
	if v.Result != "absent" {
		t.Errorf("result %q", v.Result)
	}
	engine.reset()
	v = runAtom(t, "ts:regen", "")
	if v.Result != "absent" {
		t.Errorf("no justfile at all is absent too: %q", v.Result)
	}
}

func TestTSRegenRefusesAWorldItCannotReadPinsFor(t *testing.T) {
	regenFixture("stamp")
	engine.reset()
	engine.withTree(map[string]string{"justfile": "r:\n    scripts/regen-check.sh stamp\n"})
	wantState(t, runAtom(t, "ts:regen", ""), 1, "regen-check(stamp): FAIL: no src/stampcore-gen/provenance.json")

	engine.withTree(map[string]string{"src/stampcore-gen/provenance.json": `{"wit":{}}`})
	wantState(t, runAtom(t, "ts:regen", ""), 1, "regen-check(stamp): FAIL: provenance.json pins no wit.path")
}

func TestTSRegenCannotRunWhenTheEngineWillNotAnswer(t *testing.T) {
	for _, c := range []struct {
		name, match, leaf, want string
	}{
		{"justfile", "justfile", "exists", "the justfile would not read"},
		{"provenance", "provenance.json", "exists", "engine: gone"},
		{"pin", `"conformance/tapes/stamp.json"`, "contents", "engine: gone"},
		{"core copy", `"wit/aiws-result.wit"`, "contents", "engine: gone"},
		{"build output", "wasm-tools.version", "contents", "the build's wasm-tools.version"},
		{"rustc output", "rustc.version", "contents", "the build's rustc.version"},
		{"jco", ".jco-version", "contents", "the transpile's .jco-version"},
		{"sha256sum", `"sha256sum"`, "exitCode", "engine: gone"},
		{"diff", `"diff","-r"`, "exitCode", "engine: gone"},
	} {
		regenFixture("stamp")
		engine.failLeaf(c.match, c.leaf, "engine: gone")
		v := runAtom(t, "ts:regen", "")
		wantState(t, v, 2, "CANNOT RUN", c.want)
		if t.Failed() {
			t.Fatalf("case %q", c.name)
		}
	}
	regenFixture("stamp")
	engine.exitCode(`"sha256sum"`, 1)
	engine.stdout(`"sha256sum"`, "sha256sum: /f/guest: No such file")
	wantState(t, runAtom(t, "ts:regen", ""), 2, "sha256sum exited 1")
	regenFixture("stamp")
	engine.stdout(`"sha256sum"`, "garbage")
	wantState(t, runAtom(t, "ts:regen", ""), 2, "not `<digest>  <path>`")
}

func TestTSRegenRunsEveryWorldTheRecipeNames(t *testing.T) {
	regenFixture("stamp")
	engine.withTree(map[string]string{"justfile": "r:\n    scripts/regen-check.sh stamp\n    scripts/regen-check.sh fade\n"})
	v := runAtom(t, "ts:regen", "")
	wantState(t, v, 1, "regen-check(stamp): ok", "regen-check(fade): FAIL: no src/fadecore-gen/provenance.json")
}
