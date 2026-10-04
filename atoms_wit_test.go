package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// ---- wit:validate ----

const witJustfile = "default:\n    @just --list\n\nvalidate: wit\n\nwit:\n    true\n"

// witTree is a tree an interface repo would carry: one WIT file and a
// justfile that defines validate.
func witTree(extra map[string]string) map[string]string {
	tree := map[string]string{
		"wit/aiws-identity.wit": "package aiws:identity@0.1.0;\n",
		"justfile":              witJustfile,
	}
	for k, v := range extra {
		tree[k] = v
	}
	return tree
}

func TestWitValidateIsAbsentWithoutWitOrARecipe(t *testing.T) {
	for name, tree := range map[string]map[string]string{
		"no wit":        {"justfile": witJustfile},
		"no justfile":   {"wit/x.wit": ""},
		"no validate":   {"wit/x.wit": "", "justfile": "default:\n    true\n"},
		"wit under src": {"src/wit/x.wit": "", "justfile": witJustfile},
	} {
		engine.reset()
		engine.withTree(tree)
		v := runAtom(t, "wit:validate", "")
		wantState(t, v, 0, "wit:validate: ABSENT")
		if v.Result != "absent" {
			t.Errorf("%s: want absent, got %q", name, v.Result)
		}
		fleetNoContainer(t, name)
	}
}

// THE TOOLS ARE FETCHED AT THEIR PINS, EXTRACTED BY MEMBER, PROVED, AND THE
// RECIPE IS THE LAST EXEC UNDER ANY: the provisioning runs under the default
// Expect so a failed fetch is a could-not-run, and the recipe's own exit is
// the verdict.
func TestWitValidateRunsTheRepositorysOwnRecipe(t *testing.T) {
	engine.reset()
	engine.withTree(witTree(nil))
	wantState(t, runAtom(t, "wit:validate", ""), 0)

	c := engine.chain(`"just","validate"`, "exitCode")
	if !strings.Contains(c, checks.ImageFleet) {
		t.Errorf("wit:validate runs in the fleet lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withEnvVariable", `name:"CI"`, `value:"true"`},
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withFile", `path:"/tmp/wasm-tools.tar.gz"`},
		[]string{"withExec", `args:["tar","xzf","/tmp/wasm-tools.tar.gz","-C","/usr/local/bin","--strip-components=1","` + checks.WasmToolsMember + `"]`},
		[]string{"withExec", `args:["wasm-tools","--version"]`},
		[]string{"withFile", `path:"/tmp/just.tar.gz"`},
		[]string{"withExec", `args:["tar","xzf","/tmp/just.tar.gz","-C","/usr/local/bin","` + checks.JustMember + `"]`},
		[]string{"withExec", `args:["just","--version"]`},
		[]string{"withExec", `expect:ANY`, `args:["just","validate"]`},
	)
	if hasCall(c, "withExec", `"--version"`, `expect:ANY`) || hasCall(c, "withExec", `"tar","xzf"`, `expect:ANY`) {
		t.Errorf("provisioning runs under the default Expect:\n%s", c)
	}
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("rule 8: wit:validate must not read GATE_BASE:\n%s", c)
	}
	for _, url := range []string{checks.WasmToolsURL, checks.JustURL} {
		if engine.chain(`http(url:"`+url+`")`, "id") == "" {
			t.Errorf("the pinned release %s was not fetched:\n%v", url, engine.chains())
		}
	}
	if strings.Contains(c, `"--strip-components=0"`) {
		t.Errorf("a member at the tarball root is extracted without --strip-components:\n%s", c)
	}
}

func TestWitValidateMapsTheRecipesExit(t *testing.T) {
	engine.reset()
	engine.withTree(witTree(nil))
	engine.exitCode(`"just","validate"`, 1)
	engine.stdout(`"just","validate"`, "error: expected keyword `package`")
	wantState(t, runAtom(t, "wit:validate", ""), 1, "expected keyword")

	engine.reset()
	engine.withTree(witTree(nil))
	engine.exitCode(`"just","validate"`, 127)
	wantState(t, runAtom(t, "wit:validate", ""), 2)
}

func TestWitValidateRefusesWhenAToolCannotBeFetchedOrRun(t *testing.T) {
	engine.reset()
	engine.withTree(witTree(nil))
	engine.fail("bytecodealliance/wasm-tools/releases", "404")
	wantState(t, runAtom(t, "wit:validate", ""), 2, "CANNOT RUN", "could not fetch", "never resolved is not WIT that passed")
	if engine.chain(`"just","validate"`) != "" {
		t.Errorf("WIT was judged without its tool:\n%v", engine.chains())
	}

	engine.reset()
	engine.withTree(witTree(nil))
	engine.fail("casey/just/releases", "404")
	wantState(t, runAtom(t, "wit:validate", ""), 2, "CANNOT RUN", "could not fetch")

	engine.reset()
	engine.withTree(witTree(nil))
	engine.fail(`"wasm-tools","--version"`, "exit code: 126: cannot execute binary file")
	wantState(t, runAtom(t, "wit:validate", ""), 2, "the atom never ran", "cannot execute")
}

func TestWitValidateEngineFailures(t *testing.T) {
	engine.reset()
	engine.withTree(witTree(nil))
	engine.fail(`glob(pattern:"wit/**/*.wit")`, "the tree went away")
	wantState(t, runAtom(t, "wit:validate", ""), 2, "the tree would not enumerate")

	engine.reset()
	engine.withTree(witTree(nil))
	engine.fail(`file(path:"justfile")`, "read refused")
	wantState(t, runAtom(t, "wit:validate", ""), 2, "the justfile would not read", "read refused")
}

// ---- rust:wit-guest ----

const witCargo = "[package]\nname = \"stellar-core\"\n\n[features]\ndefault = []\nwit-guest = [\"dep:wit-bindgen\"]\n"

const guestBuild = `"cargo","build","--locked","--release"`

func TestRustWitGuestIsAbsentWithoutTheFeature(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree) // Cargo.toml declares no features
	v := runAtom(t, "rust:wit-guest", "")
	wantState(t, v, 0, "rust:wit-guest: ABSENT", "declares no wit-guest feature")
	if v.Result != "absent" {
		t.Errorf("want absent, got %q", v.Result)
	}
	fleetNoContainer(t, "no wit-guest feature")
}

func TestRustWitGuestBuildsLiftsAndValidates(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": witCargo})
	wantState(t, runAtom(t, "rust:wit-guest", ""), 0)

	module := checks.WitGuestTargetDir + "/" + checks.WitGuestTarget + "/release/stellar_core.wasm"
	const component = "/tmp/wit-guest.component.wasm"
	c := engine.chain(`"component","wit"`, "exitCode")
	if !strings.Contains(c, checks.ImageRust) {
		t.Errorf("rust:wit-guest runs in the rust lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withExec", `args:["cargo","fetch","--locked"]`},
		[]string{"withExec", `args:["rustup","target","add","` + checks.WitGuestTarget + `"]`},
		[]string{"withExec", `args:["tar","xzf","/tmp/wasm-tools.tar.gz","-C","/usr/local/bin","--strip-components=1","` + checks.WasmToolsMember + `"]`},
		[]string{"withExec", `args:["wasm-tools","--version"]`},
		[]string{"withExec", `expect:ANY`, `args:["cargo","build","--locked","--release","--no-default-features","--features","wit-guest","--target","wasm32-unknown-unknown","--target-dir","` + checks.WitGuestTargetDir + `"]`},
		[]string{"withExec", `expect:ANY`, `args:["wasm-tools","component","new","` + module + `","-o","` + component + `"]`},
		[]string{"withExec", `expect:ANY`, `args:["wasm-tools","validate","` + component + `"]`},
		[]string{"withExec", `expect:ANY`, `args:["wasm-tools","component","wit","` + component + `"]`},
	)
	// Order is the contract: the target and the tool before the build, the
	// build before the lift, the lift before the validation, the validation
	// before the read-back.
	at := func(needle string) int { return strings.Index(c, needle) }
	order := []string{`"rustup","target","add"`, `"wasm-tools","--version"`, guestBuild, `"component","new"`, `"wasm-tools","validate"`, `"component","wit"`}
	for i := 1; i < len(order); i++ {
		if !(at(order[i-1]) >= 0 && at(order[i-1]) < at(order[i])) {
			t.Errorf("%s must come before %s:\n%s", order[i-1], order[i], c)
		}
	}
	if hasCall(c, "withExec", `"rustup","target","add"`, `expect:ANY`) || hasCall(c, "withExec", `"wasm-tools","--version"`, `expect:ANY`) {
		t.Errorf("provisioning runs under the default Expect:\n%s", c)
	}
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("rule 8: rust:wit-guest must not read GATE_BASE:\n%s", c)
	}
	if engine.chain(`http(url:"`+checks.WasmToolsURL+`")`, "id") == "" {
		t.Errorf("the pinned wasm-tools was not fetched:\n%v", engine.chains())
	}
}

func TestRustWitGuestReadsTheArtifactNameOffTheManifest(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": strings.Replace(witCargo, "[features]", "[lib]\nname = \"core-guest\"\n\n[features]", 1)})
	wantState(t, runAtom(t, "rust:wit-guest", ""), 0)
	if engine.chain(`/release/core_guest.wasm`, "exitCode") == "" {
		t.Errorf("the [lib] name names the artifact:\n%v", engine.chains())
	}
}

func TestRustWitGuestMapsEachStepsExit(t *testing.T) {
	cases := []struct {
		name, needle string
		code, state  int
	}{
		{"the build fails", guestBuild, 1, 1},
		{"a compile error, cargo's 101", guestBuild, 101, 1},
		{"cargo cannot run", guestBuild, 127, 2},
		{"the module will not lift", `"component","new"`, 1, 1},
		{"the component does not validate", `"wasm-tools","validate"`, 1, 1},
		{"the world cannot be read back", `"component","wit"`, 1, 1},
		{"a step killed by a signal is not a finding", `"wasm-tools","validate"`, 137, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			engine.reset()
			engine.withTree(map[string]string{"Cargo.toml": witCargo})
			engine.exitCode(c.needle, c.code)
			wantState(t, runAtom(t, "rust:wit-guest", ""), c.state)
		})
	}

	// A build that failed goes no further.
	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": witCargo})
	engine.exitCode(guestBuild, 1)
	runAtom(t, "rust:wit-guest", "")
	if engine.chain(`"component","new"`) != "" {
		t.Errorf("a guest that did not build was lifted anyway:\n%v", engine.chains())
	}
}

func TestRustWitGuestRefusalsAreCouldNotRuns(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": "[features]\nwit-guest = []\n"})
	wantState(t, runAtom(t, "rust:wit-guest", ""), 2, "CANNOT RUN", "names no [package] or [lib] name")

	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": witCargo})
	engine.fail("bytecodealliance/wasm-tools/releases", "404")
	wantState(t, runAtom(t, "rust:wit-guest", ""), 2, "CANNOT RUN", "could not fetch", "never validated is not a guest that passed")

	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": witCargo})
	engine.fail(`"rustup","target","add"`, "exit code: 1: static.rust-lang.org unreachable")
	wantState(t, runAtom(t, "rust:wit-guest", ""), 2, "the atom never ran", "unreachable")

	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": witCargo})
	engine.fail(`file(path:"Cargo.toml")`, "read refused")
	wantState(t, runAtom(t, "rust:wit-guest", ""), 2, "Cargo.toml would not read", "read refused")
}
