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
	wantState(t, v, 0, "rust:wit-guest: ABSENT", "declares no wit-guest-<world> feature")
	if v.Result != "absent" {
		t.Errorf("want absent, got %q", v.Result)
	}
	fleetNoContainer(t, "no wit-guest feature")
}

func TestRustWitGuestBuildsLiftsAndValidates(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": witCargo})
	engine.stdout(`"component","wit"`, "world identity-core")
	v := runAtom(t, "rust:wit-guest", "")
	wantState(t, v, 0)
	if !strings.Contains(strings.Join(v.Logs, "\n"), "world identity-core") {
		t.Errorf("the read-back world is the atom's output, got %q", v.Logs)
	}

	inVolume := checks.WitGuestTargetDir + "/" + checks.WitGuestTarget + "/release/stellar_core.wasm"
	const module = "/tmp/wit-guest.wasm"
	const component = "/tmp/wit-guest.component.wasm"
	c := engine.chain(`"component","wit"`, "exitCode")
	if !strings.Contains(c, checks.ImageRust) {
		t.Errorf("rust:wit-guest runs in the rust lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withExec", `args:["cargo","fetch","--locked"]`},
		// The release volume, under the stamp: the guest's crate is rebuilt
		// from THIS tree, and only the registry crates come back Fresh.
		[]string{"withExec", `"touch","-c","-d","@4102444800"`},
		[]string{"withMountedCache", `path:"/cache/cargo-release"`, `sharing:PRIVATE`},
		[]string{"withExec", `args:["rustup","target","add","` + checks.WitGuestTarget + `"]`},
		[]string{"withExec", `args:["tar","xzf","/tmp/wasm-tools.tar.gz","-C","/usr/local/bin","--strip-components=1","` + checks.WasmToolsMember + `"]`},
		[]string{"withExec", `args:["wasm-tools","--version"]`},
		// The module is copied out of the volume BY THE BUILD'S OWN EXEC, and
		// wasm-tools reads the copy.
		[]string{"withExec", `expect:ANY`, `args:["/usr/local/bin/copyout","` + inVolume + `=` + module + `","--","cargo","build","--locked","--release","--no-default-features","--features","wit-guest","--target","wasm32-unknown-unknown","--target-dir","` + checks.WitGuestTargetDir + `"]`},
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

	// A step that failed goes no further.
	for _, c := range [][2]string{
		{guestBuild, `"component","new"`},
		{`"component","new"`, `"wasm-tools","validate"`},
		{`"wasm-tools","validate"`, `"component","wit"`},
	} {
		engine.reset()
		engine.withTree(map[string]string{"Cargo.toml": witCargo})
		engine.exitCode(c[0], 1)
		runAtom(t, "rust:wit-guest", "")
		if engine.chain(c[1]) != "" {
			t.Errorf("%s failed and %s ran anyway:\n%v", c[0], c[1], engine.chains())
		}
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

// ---- rust:wit-guest: world discovery (rob/stellar-core-rust#14205) ----

const witTwoWorlds = "[package]\nname = \"stellar-core\"\n\n[features]\ndefault = []\nwit-guest = [\"wit-guest-identity\"]\nwit-guest-identity = [\"dep:wit-bindgen\"]\nwit-guest-reader = [\"dep:wit-bindgen\"]\n"

func feat(world string) string { return `"--features","` + world + `"` }

// Two worlds and the alias: exactly the two are built and tested, the alias
// never is, and each world gets all five steps.
func TestRustWitGuestGradesEveryWorldAndNotTheAlias(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": witTwoWorlds})
	engine.stdout(`"component","wit"`, "world w")
	v := runAtom(t, "rust:wit-guest", "")
	wantState(t, v, 0)

	for _, w := range []string{"wit-guest-identity", "wit-guest-reader"} {
		if engine.chain(guestBuild, feat(w), "exitCode") == "" {
			t.Errorf("%s: no wasm32 build:\n%v", w, engine.chains())
		}
		if engine.chain(`"cargo","test"`, `"--no-default-features"`, feat(w), "exitCode") == "" {
			t.Errorf("%s: no native cargo test:\n%v", w, engine.chains())
		}
		if !strings.Contains(strings.Join(v.Logs, "\n"), "["+w+"] ") {
			t.Errorf("%s: pass logs name the world, got %q", w, v.Logs)
		}
	}
	if engine.chain(guestBuild, feat("wit-guest"), "exitCode") != "" || engine.chain(`"cargo","test"`, feat("wit-guest"), "exitCode") != "" {
		t.Errorf("the bare alias must not be graded when a world exists:\n%v", engine.chains())
	}
}

// The native test builds into the shared foundry-cargo-target, so it runs under
// the Unstale stamp, after the fetch and before cargo. Without the stamp cargo
// reuses the test binary an older tree left behind and a module added since is
// never compiled (stellar-core-rust#14205: 26 round-trip tests absent, a
// compile_error! under cfg(test) green).
func TestRustWitGuestNativeTestRunsUnderTheStamp(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": witTwoWorlds})
	engine.stdout(`"component","wit"`, "world w")
	wantState(t, runAtom(t, "rust:wit-guest", ""), 0)

	stamp := `"touch","-c","-d","@4102444800"`
	for _, w := range []string{"wit-guest-identity", "wit-guest-reader"} {
		c := engine.chain(`"cargo","test"`, `"--no-default-features"`, feat(w), "exitCode")
		if c == "" {
			t.Fatalf("%s: no native cargo test:\n%v", w, engine.chains())
		}
		fetch, find, test := strings.Index(c, `"cargo","fetch","--locked"`), strings.Index(c, stamp), strings.Index(c, `"cargo","test"`)
		if fetch < 0 || find < 0 || !(fetch < find && find < test) {
			t.Errorf("%s: want fetch, then the stamp, then cargo test (fetch %d, stamp %d, test %d):\n%s", w, fetch, find, test, c)
		}
	}
}

// The alias alone, with no wit-guest-<world>, is ITSELF the one world: it is
// what the atom graded before the split, and going inert would stop grading
// that guest. It is built and tested like a world.
func TestRustWitGuestAliasAloneIsTheOneWorld(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": witCargo})
	wantState(t, runAtom(t, "rust:wit-guest", ""), 0)
	if engine.chain(guestBuild, feat("wit-guest"), "exitCode") == "" || engine.chain(`"cargo","test"`, feat("wit-guest"), "exitCode") == "" {
		t.Errorf("the lone alias is built and tested:\n%v", engine.chains())
	}
}

// A red names the world and the step; the other world's failure is not
// attributed to the first.
func TestRustWitGuestFindingsNameTheFailingWorld(t *testing.T) {
	for _, c := range []struct{ name, needle, world, step string }{
		{"reader build", guestBuild + `,"--no-default-features",` + feat("wit-guest-reader"), "wit-guest-reader", "build"},
		{"reader test", `"cargo","test","--locked","--no-default-features",` + feat("wit-guest-reader"), "wit-guest-reader", "test"},
		{"identity test", `"cargo","test","--locked","--no-default-features",` + feat("wit-guest-identity"), "wit-guest-identity", "test"},
	} {
		t.Run(c.name, func(t *testing.T) {
			engine.reset()
			engine.withTree(map[string]string{"Cargo.toml": witTwoWorlds})
			engine.exitCode(c.needle, 101)
			v := runAtom(t, "rust:wit-guest", "")
			wantState(t, v, 1, "world "+c.world, c.step)
			if len(v.Logs) == 0 || !strings.Contains(v.Logs[0], c.world) {
				t.Errorf("first log line names the world, got %q", v.Logs)
			}
		})
	}

	// identity (sorted first) failing stops before reader is built.
	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": witTwoWorlds})
	engine.exitCode(`"cargo","test","--locked","--no-default-features",`+feat("wit-guest-identity"), 101)
	runAtom(t, "rust:wit-guest", "")
	if engine.chain(feat("wit-guest-reader")) != "" {
		t.Errorf("a failed world goes no further:\n%v", engine.chains())
	}
}

// A crate with no wit-guest feature at all, even one with other features, is
// inert and never starts a container.
func TestRustWitGuestInertWithoutAnyWorld(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": "[package]\nname = \"x\"\n\n[features]\ndefault = []\nwit = []\n"})
	v := runAtom(t, "rust:wit-guest", "")
	wantState(t, v, 0, "ABSENT")
	if v.Result != "absent" {
		t.Errorf("want absent, got %q", v.Result)
	}
	fleetNoContainer(t, "no worlds")
}

// ---- rust:wit-compose (stellar-core-rust#15266) ----

func witComposeTree(extra map[string]string) map[string]string {
	tree := map[string]string{
		"Cargo.toml":                witTwoWorlds,
		"tools/compose/compose.sh":  "#!/usr/bin/env bash\n",
		"tools/replay/Cargo.toml":   "[package]\nname = \"replay\"\n",
		"tests/tapes/identity.json": "{}",
		"tests/tapes/reader.json":   "{}",
		"tests/tapes/promote.json":  "{}",
	}
	for k, v := range extra {
		tree[k] = v
	}
	return tree
}

const composeBuildHost = `"cargo","build","--locked","--release","--manifest-path","tools/replay/Cargo.toml"`
const composeRun = `"tools/compose/compose.sh","/tmp/fleet.component.wasm"`
const composeReplay = `"/tmp/replay-host/replay"`

func TestRustWitComposeIsAbsentWithoutTheComposerOrAWorld(t *testing.T) {
	for name, tree := range map[string]map[string]string{
		"no world":    witComposeTree(map[string]string{"Cargo.toml": "[package]\nname = \"x\"\n\n[features]\ndefault = []\n"}),
		"no composer": {"Cargo.toml": witTwoWorlds, "tools/replay/Cargo.toml": ""},
		"no replay":   {"Cargo.toml": witTwoWorlds, "tools/compose/compose.sh": ""},
	} {
		engine.reset()
		engine.withTree(tree)
		v := runAtom(t, "rust:wit-compose", "")
		wantState(t, v, 0, "rust:wit-compose: ABSENT")
		if v.Result != "absent" {
			t.Errorf("%s: want absent, got %q", name, v.Result)
		}
		fleetNoContainer(t, name)
	}
}

func TestRustWitComposeBuildsComposesAndReplaysTheWorldsTapes(t *testing.T) {
	engine.reset()
	engine.withTree(witComposeTree(nil))
	engine.stdout(composeReplay, "TOTAL 1371 pass, 0 fail")
	v := runAtom(t, "rust:wit-compose", "")
	wantState(t, v, 0)
	if !strings.Contains(strings.Join(v.Logs, "\n"), "TOTAL 1371 pass, 0 fail") {
		t.Errorf("the replay's own tally is the atom's output, got %q", v.Logs)
	}

	c := engine.chain(composeReplay, "exitCode")
	if !strings.Contains(c, checks.ImageRust) {
		t.Errorf("rust:wit-compose runs in the rust lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withExec", `args:["rustup","target","add","` + checks.WitGuestTarget + `"]`},
		[]string{"withExec", `args:["wasm-tools","--version"]`},
		[]string{"withExec", `args:["wac","--version"]`},
		[]string{"withEnvVariable", `name:"CARGO_TARGET_DIR"`, `value:"` + checks.WitGuestTargetDir + `"`},
		// The release volume, under the stamp, and the host copied out of it
		// by the build's own exec.
		[]string{"withExec", `"touch","-c","-d","@4102444800"`},
		[]string{"withMountedCache", `path:"/cache/cargo-release"`, `sharing:PRIVATE`},
		[]string{"withExec", `expect:ANY`, `args:["/usr/local/bin/copyout","` + checks.WitReplayTargetDir + `/release/replay=/tmp/replay-host/replay","--","cargo","build","--locked","--release","--manifest-path","tools/replay/Cargo.toml","--target-dir","` + checks.WitReplayTargetDir + `"]`},
		[]string{"withExec", `expect:ANY`, `args:["tools/compose/compose.sh","/tmp/fleet.component.wasm"]`},
		// The tapes are the discovered worlds' own: identity and reader, not promote.
		[]string{"withExec", `expect:ANY`, `args:["/tmp/replay-host/replay","/tmp/fleet.component.wasm","tests/tapes/identity.json","tests/tapes/reader.json"]`},
	)
	if strings.Contains(c, "promote.json") {
		t.Errorf("a tape no world names is not replayed:\n%s", c)
	}
	at := func(needle string) int { return strings.Index(c, needle) }
	for _, p := range [][2]string{{`"wac","--version"`, composeBuildHost}, {composeBuildHost, composeRun}, {composeRun, composeReplay}} {
		if !(at(p[0]) >= 0 && at(p[0]) < at(p[1])) {
			t.Errorf("%s must come before %s:\n%s", p[0], p[1], c)
		}
	}
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("rule 8: rust:wit-compose must not read GATE_BASE:\n%s", c)
	}
	for _, u := range []string{checks.WasmToolsURL, checks.WacURL} {
		if engine.chain(`http(url:"`+u+`")`, "id") == "" {
			t.Errorf("the pinned tool %s was not fetched:\n%v", u, engine.chains())
		}
	}
}

func TestRustWitComposeMapsEachStepsExitAndStops(t *testing.T) {
	steps := []struct{ name, needle, next string }{
		{"replay host", composeBuildHost, composeRun},
		{"compose", composeRun, composeReplay},
		{"replay", composeReplay, ""},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			engine.reset()
			engine.withTree(witComposeTree(nil))
			engine.exitCode(s.needle, 1)
			v := runAtom(t, "rust:wit-compose", "")
			wantState(t, v, 1, "["+s.name+"]")
			if len(v.Logs) == 0 || !strings.Contains(v.Logs[0], s.name) {
				t.Errorf("first log line names the step, got %q", v.Logs)
			}
			if s.next != "" && engine.chain(s.next) != "" {
				t.Errorf("%s failed and %s ran anyway:\n%v", s.name, s.next, engine.chains())
			}

			engine.reset()
			engine.withTree(witComposeTree(nil))
			engine.exitCode(s.needle, 137)
			wantState(t, runAtom(t, "rust:wit-compose", ""), 2)
		})
	}
}

func TestRustWitComposeRefusalsAreCouldNotRuns(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": witTwoWorlds, "tools/compose/compose.sh": "", "tools/replay/Cargo.toml": "", "tests/tapes/promote.json": ""})
	wantState(t, runAtom(t, "rust:wit-compose", ""), 2, "CANNOT RUN", "answer nothing")

	engine.reset()
	engine.withTree(witComposeTree(nil))
	engine.fail(`glob(pattern:"tests/tapes/*.json")`, "the tree went away")
	wantState(t, runAtom(t, "rust:wit-compose", ""), 2, "the tree would not enumerate")

	engine.reset()
	engine.withTree(witComposeTree(nil))
	engine.fail("bytecodealliance/wac/releases", "404")
	wantState(t, runAtom(t, "rust:wit-compose", ""), 2, "CANNOT RUN", "could not fetch", "never built is not one that passed")

	engine.reset()
	engine.withTree(witComposeTree(nil))
	engine.fail("bytecodealliance/wasm-tools/releases", "404")
	wantState(t, runAtom(t, "rust:wit-compose", ""), 2, "CANNOT RUN", "could not fetch")

	engine.reset()
	engine.withTree(witComposeTree(nil))
	engine.fail(`file(path:"Cargo.toml")`, "read refused")
	wantState(t, runAtom(t, "rust:wit-compose", ""), 2, "Cargo.toml would not read", "read refused")
}

// THE RED TAPES RIDE AFTER THE WORLDS' (F18): sorted, named by package, and not
// filtered by the manifest, so the replay host judges each as red.
func TestRustWitComposeReplaysTheRedTapesAfterTheWorldTapes(t *testing.T) {
	engine.reset()
	engine.withTree(witComposeTree(map[string]string{
		"tests/red/tail.json": "{}", "tests/red/alarm.json": "{}", "tests/red/PROVENANCE": "x",
	}))
	wantState(t, runAtom(t, "rust:wit-compose", ""), 0)
	c := engine.chain(composeReplay, "exitCode")
	wantCalls(t, c,
		[]string{"withExec", `expect:ANY`, `args:["/tmp/replay-host/replay","/tmp/fleet.component.wasm","tests/tapes/identity.json","tests/tapes/reader.json","tests/red/alarm.json","tests/red/tail.json"]`},
	)
	if strings.Contains(c, "PROVENANCE") {
		t.Errorf("a non-tape is not replayed:\n%s", c)
	}

	// A failing red replay is the replay step's finding.
	engine.reset()
	engine.withTree(witComposeTree(map[string]string{"tests/red/tail.json": "{}"}))
	engine.exitCode(composeReplay, 1)
	wantState(t, runAtom(t, "rust:wit-compose", ""), 1, "[replay]")

	// The red glob is read with the tape glob: a tree that will not enumerate it cannot run.
	engine.reset()
	engine.withTree(witComposeTree(map[string]string{"tests/red/tail.json": "{}"}))
	engine.fail(`glob(pattern:"tests/red/*.json")`, "the tree went away")
	wantState(t, runAtom(t, "rust:wit-compose", ""), 2, "the tree would not enumerate")

	// Red tapes alone do not stand in for a world's tape.
	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": witTwoWorlds, "tools/compose/compose.sh": "", "tools/replay/Cargo.toml": "", "tests/red/tail.json": "{}"})
	wantState(t, runAtom(t, "rust:wit-compose", ""), 2, "CANNOT RUN", "answer nothing")
}
