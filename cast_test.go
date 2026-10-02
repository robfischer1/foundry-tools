package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/dagger"
)

const (
	castDoorbell = "http://redpanda:28082/topics/hephaestus.releases"
	castPin      = "g0123456789ab"
	// castStaged is the staging push's manifest digest; castLanded is the
	// channel digest mold answers.
	castStaged = "sha256:5555555555555555555555555555555555555555555555555555555555555555"
	castLanded = "sha256:7777777777777777777777777777777777777777777777777777777777777777"
	castRef    = "foundry.notusmi.com/staging/app-tongs:" + castPin

	// The needles, each in one chain and no other.
	cargoNeedle   = `"cargo","build"`
	castpinNeedle = `"/usr/local/bin/castpin"`
	stageNeedle   = `"oras","push"`
	mintNeedle    = `"layer_cast"`
	verifyNeedle  = `"verify","--key"`
	bellNeedle    = `"curl"`
)

// tongsRecord is a binary repo's v3 record around its cast block.
func tongsRecord(cast string) string {
	return `{"$schema":"https://forgejo.notusmi.com/rob/foundry-dies/schema/slag-v3.schema.json","meta":{"name":"tongs","produces":["binary"]},"tools":{"cast":` + cast + `}}`
}

// castOn is the module constructed on a binary repo at a commit the engine
// fetched, with its record in foundry-dies. A tree entry with an empty value
// removes that file.
func castOn(t *testing.T, tree map[string]string) *FoundryTools {
	t.Helper()
	engine.reset()
	base := map[string]string{
		"Cargo.toml":                        "[workspace]\n",
		"crates/tongs/src/main.rs":          "fn main() {}\n",
		"cosign.pub":                        "-----BEGIN PUBLIC KEY-----\nabc\n-----END PUBLIC KEY-----\n",
		"/dies/fleet/stars/tongs/slag.json": tongsRecord(`{"binaries":["tongs"]}`),
	}
	for k, v := range tree {
		if v == "" {
			delete(base, k)
			continue
		}
		base[k] = v
	}
	engine.withTree(base)
	return &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/rob/tongs.git", Sha: buildSha}
}

// castResult is mold's answer to a mint.
func castResult(pin string, signed bool) string {
	s := "false"
	if signed {
		s = "true"
	}
	return `{"channel":"foundry.notusmi.com/app/tongs:stable","kind":"app","name":"tongs","tag":"stable","version_index":7,"pin":"` + pin + `","digest":"` + castLanded + `","signed":` + s + `,"noop":false}`
}

// scriptACast answers every step of a cast that lands: the pin, the staging
// push and mold.
func scriptACast(listing string) {
	engine.stdout(castpinNeedle, listing)
	engine.stdout(stageNeedle, castStaged+"\n")
	engine.stdout(mintNeedle, "HTTP 200\n"+toolAnswer(false, castResult(castPin, true)))
}

// casts runs the lane the way a landing does: the socket, the token and the
// doorbell.
func casts(t *testing.T, m *FoundryTools) {
	t.Helper()
	castWith(t, m, dag.LoadSocketFromID("spire-agent-socket"), dag.SetSecret("registry-token", "tok"), castDoorbell, false)
}

func castWith(t *testing.T, m *FoundryTools, spire *dagger.Socket, token *dagger.Secret, doorbell string, dryRun bool) {
	t.Helper()
	if err := m.Cast(context.Background(), spire, token, doorbell, "https://hades:8102", "spiffe://notusmi.com/star/hades", dryRun, nil); err != nil {
		t.Fatalf("cast: %v", err)
	}
}

// A cast builds the release, pins and stages the payload, asks mold to mint
// it, verifies what landed against the repo's own key and rings the bell.
func TestACastStagesMintsVerifiesAndRings(t *testing.T) {
	m := castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	casts(t, m)
	settledOn(t, "0", "clean: cast app/tongs:stable at index 7 ("+castPin+", "+castLanded+")")
	settledOn(t, "0", "verified against cosign.pub; the doorbell rang")

	wantCalls(t, engine.chain(cargoNeedle),
		[]string{"from", "rust:1.97.0"},
		[]string{"withEnvVariable", `"CARGO_TARGET_DIR"`, `"/work/target"`},
		[]string{"withExec", `["cargo","build","--release","--locked"]`},
	)
	if engine.chain(cargoNeedle, `file(path:"/work/target/release/tongs")`) == "" {
		t.Error("the binary was not read out of the build's own target directory")
	}
	wantCalls(t, engine.chain(`directory{withFile`), []string{"withFile", `path:"tongs"`})
	wantCalls(t, engine.chain(castpinNeedle),
		[]string{"withMountedDirectory", `path:"/payload"`},
		[]string{"withExec", `["/usr/local/bin/castpin","/payload"]`},
	)
	wantCalls(t, engine.chain(stageNeedle),
		[]string{"withMountedSecret", `"/run/docker/config.json"`},
		[]string{"withExec", `"--registry-config"`, `"{{.digest}}"`, `"` + castRef + `","tongs"]`},
	)
	wantCalls(t, engine.chain(mintNeedle), []string{"withExec", `app/tongs:stable`, castRef + "@" + castStaged, buildSha})
	// SIGNED ON PURPOSE (D12): the lane asks for the signature by name.
	if mint := engine.chain(mintNeedle); !strings.Contains(mint, `sign\":true`) {
		t.Errorf("the mint did not ask for sign=true:\n%s", mint)
	}
	wantCalls(t, engine.chain(verifyNeedle),
		[]string{"withFile", `"/run/cosign/cosign.pub"`},
		[]string{"withExec", `"--insecure-ignore-tlog=true"`, `"foundry.notusmi.com/app/tongs@` + castLanded + `"`},
	)
	wantCalls(t, engine.chain(bellNeedle), []string{"withExec", castDoorbell, `index`, castLanded})
	if engine.chain(`"forge_layer_cast"`) != "" {
		t.Error("the lane asked the retired forge_layer_cast")
	}
}

// A failure of the cast is the cast's failure: the lane asks layer_cast once,
// never a second name, and the verdict names layer_cast.
func TestAFailedCastIsAskedOnceAndNamesLayerCast(t *testing.T) {
	cases := map[string]struct {
		script       func()
		code, reason string
	}{
		"the star refuses the cast": {func() {
			engine.stdout(mintNeedle, "HTTP 200\n"+toolAnswer(true, "layer_cast app/tongs:stable: staged payload pin mismatch"))
		}, "1", "findings in layer_cast"},
		"hades cannot reach the star": {func() {
			engine.stdout(mintNeedle, "HTTP 503\n{\"detail\":\"downstream unavailable\"}")
		}, "2", "HTTP 503"},
		"no star serves the name": {func() {
			engine.stdout(mintNeedle, "HTTP 404\n{\"detail\":\"no star serves verb \\\"layer_cast\\\"\"}")
		}, "2", "HTTP 404"},
		"the caller is not permitted": {func() {
			engine.stdout(mintNeedle, "HTTP 403\n{\"detail\":\"forbidden: caller not permitted for verb \\\"layer_cast\\\": no grant\"}")
		}, "1", "not granted layer_cast"},
		"hades cannot identify the caller": {func() {
			engine.stdout(mintNeedle, "HTTP 403\n{\"detail\":\"forbidden: caller not permitted for verb \\\"layer_cast\\\": unidentifiable caller\"}")
		}, "2", "did not derive a principal"},
		"hadescall cannot ask": {func() {
			engine.exitCode(mintNeedle, 2)
			engine.stdout(mintNeedle, "no identity within 2m0s")
		}, "2", "could not ask hades: no identity"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := castOn(t, nil)
			scriptACast(castPin + "\ntongs\n")
			c.script()
			casts(t, m)
			settledOn(t, c.code, c.reason)
			if n := strings.Count(engine.chain(mintNeedle), `["/usr/local/bin/hadescall",`); n != 1 {
				t.Errorf("the lane asked hades %d times", n)
			}
			if engine.chain(`"forge_layer_cast"`) != "" {
				t.Error("a failure of layer_cast fell back to forge_layer_cast")
			}
			if engine.chain(verifyNeedle) != "" {
				t.Error("a cast that did not mint went on to verify")
			}
		})
	}
}

// A directory in payload_extra ships under its basename beside the binaries,
// and every file the pin lists is pushed.
func TestAPayloadExtraDirectoryShipsUnderItsBasename(t *testing.T) {
	m := castOn(t, map[string]string{
		"/dies/fleet/stars/tongs/slag.json": tongsRecord(`{"binaries":["tongs","git-credential-tongs"],"payload_extra":[".tongs/hooks"]}`),
		".tongs/hooks/a.py":                 "a",
		".tongs/hooks/sub/b.py":             "b",
	})
	scriptACast(castPin + "\ngit-credential-tongs\nhooks/a.py\nhooks/sub/b.py\ntongs\n")
	casts(t, m)
	settledOn(t, "0", "clean: cast app/tongs:stable")
	wantCalls(t, engine.chain(`directory{withFile`),
		[]string{"withFile", `path:"tongs"`},
		[]string{"withFile", `path:"git-credential-tongs"`},
		[]string{"withDirectory", `path:"hooks"`},
	)
	if engine.chain(`directory(path:".tongs/hooks")`) == "" {
		t.Error("the hooks directory was not taken from the checkout")
	}
	wantCalls(t, engine.chain(stageNeedle), []string{"withExec", `"git-credential-tongs","hooks/a.py","hooks/sub/b.py","tongs"]`})
}

// A single file in payload_extra ships under its basename.
func TestAPayloadExtraFileShipsUnderItsBasename(t *testing.T) {
	m := castOn(t, map[string]string{
		"/dies/fleet/stars/tongs/slag.json": tongsRecord(`{"binaries":["tongs"],"payload_extra":["docs/tongs.1"]}`),
		"docs/tongs.1":                      "man",
	})
	scriptACast(castPin + "\ntongs\ntongs.1\n")
	casts(t, m)
	settledOn(t, "0", "clean: cast app/tongs:stable")
	wantCalls(t, engine.chain(`directory{withFile`), []string{"withFile", `path:"tongs.1"`})
	if engine.chain(`file(path:"docs/tongs.1"){id}`) == "" {
		t.Error("the file was not taken from the checkout")
	}
}

// Everything the lane refuses before it builds is refused with nothing built.
func TestACastThatCannotStartBuildsNothing(t *testing.T) {
	cases := map[string]struct {
		tree         map[string]string
		script       func()
		code, reason string
	}{
		// The needle is the read, not the path: the settle's reason names the
		// path too, and a path needle would fail the verdict exec with it.
		"no record": {nil, func() {
			engine.fail(`file(path:"fleet/stars/tongs/slag.json"){contents}`, "no such file or directory")
		}, "2", "no record for tongs could be read at foundry-dies fleet/stars/tongs/slag.json, so nothing says what it ships"},
		"a record with no cast block": {map[string]string{"/dies/fleet/stars/tongs/slag.json": `{"$schema":"slag-v3.schema.json","meta":{"name":"tongs","produces":["binary"]},"tools":{}}`}, nil, "1",
			"carries no tools.cast"},
		"a record that is not a binary": {map[string]string{"/dies/fleet/stars/tongs/slag.json": `{"$schema":"slag-v3.schema.json","meta":{"name":"tongs","produces":["image"]},"tools":{"cast":{"binaries":["tongs"]}}}`}, nil, "1",
			"not binary"},
		"no cosign.pub": {map[string]string{"cosign.pub": ""}, nil, "1", "carries no cosign.pub"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := castOn(t, c.tree)
			scriptACast(castPin + "\ntongs\n")
			if c.script != nil {
				c.script()
			}
			casts(t, m)
			settledOn(t, c.code, c.reason)
			if engine.chain(cargoNeedle) != "" || engine.chain(stageNeedle) != "" {
				t.Fatal("a cast that could not start built or staged")
			}
		})
	}
}

// A cast without its credentials cannot run, and a module with no commit
// cannot either.
func TestACastWithoutItsCredentialsCannotRun(t *testing.T) {
	m := castOn(t, nil)
	castWith(t, m, nil, dag.SetSecret("registry-token", "tok"), castDoorbell, false)
	settledOn(t, "2", "--spire and --registry-token are both required")

	m = castOn(t, nil)
	castWith(t, m, dag.LoadSocketFromID("spire-agent-socket"), nil, castDoorbell, false)
	settledOn(t, "2", "--spire and --registry-token are both required")

	m = castOn(t, nil)
	m.Sha = ""
	casts(t, m)
	settledOn(t, "2", "construct the module with --repo and --sha")
}

// A dry run builds and pins for real and stages, mints and verifies nothing,
// with no credentials at all.
func TestADryRunBuildsAndPinsAndPublishesNothing(t *testing.T) {
	m := castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	castWith(t, m, nil, nil, castDoorbell, true)
	settledOn(t, "0", "clean: dry run — 1 file(s) pin to "+castPin+" for app/tongs:stable")
	if engine.chain(castpinNeedle) == "" {
		t.Fatal("a dry run did not pin")
	}
	for _, needle := range []string{stageNeedle, mintNeedle, verifyNeedle, bellNeedle} {
		if engine.chain(needle) != "" {
			t.Errorf("a dry run reached %s", needle)
		}
	}
}

// Every step that fails after the build starts settles with what it means,
// and nothing after it runs.
func TestACastThatFailsStopsWhereItFailed(t *testing.T) {
	cases := map[string]struct {
		tree         map[string]string
		script       func()
		code, reason string
		reached      []string
		never        []string
	}{
		"the build fails": {nil, func() {
			engine.exitCode(cargoNeedle, 101)
			engine.stdout(cargoNeedle, "error[E0425]: cannot find value `x` in this scope")
		}, "1", "findings in cargo build", nil, []string{castpinNeedle, stageNeedle}},
		"the build cannot run": {nil, func() {
			engine.failLeaf(cargoNeedle, "exitCode", "the engine went away")
		}, "2", "the release build did not run", nil, []string{castpinNeedle}},
		"a binary the build did not leave": {nil, func() {
			engine.failLeaf(`"/work/target/release/tongs"`, "size", "no such file")
		}, "1", "the release build left no", []string{cargoNeedle}, []string{castpinNeedle}},
		"an extra the checkout does not carry": {map[string]string{
			"/dies/fleet/stars/tongs/slag.json": tongsRecord(`{"binaries":["tongs"],"payload_extra":[".tongs/hooks"]}`),
		}, nil, "1", "names .tongs/hooks, which this checkout does not carry", []string{cargoNeedle}, []string{castpinNeedle}},
		"castpin fails": {nil, func() {
			engine.exitCode(castpinNeedle, 2)
		}, "2", "castpin exited 2", nil, []string{stageNeedle}},
		"castpin cannot run": {nil, func() {
			engine.failLeaf(castpinNeedle, "exitCode", "the engine went away")
		}, "2", "the payload could not be pinned", nil, []string{stageNeedle}},
		"castpin says nothing readable": {nil, func() {
			engine.stdout(castpinNeedle, "garbage")
		}, "2", "castpin answered no pin", nil, []string{stageNeedle}},
		"the registry refuses the push": {nil, func() {
			engine.exitCode(stageNeedle, 1)
			engine.stdout(stageNeedle, "Error: failed to push: unauthorized")
		}, "1", "findings in staging push", nil, []string{mintNeedle}},
		"the push cannot run": {nil, func() {
			engine.failLeaf(stageNeedle, "exitCode", "the engine went away")
		}, "2", "the staging push did not run", nil, []string{mintNeedle}},
		"the push answers no digest": {nil, func() {
			engine.stdout(stageNeedle, "Pushed\n")
		}, "2", "answered no digest", nil, []string{mintNeedle}},
		"hades cannot be asked": {nil, func() {
			engine.exitCode(mintNeedle, 1)
			engine.stdout(mintNeedle, "no identity within 2m0s")
		}, "2", "could not ask hades: no identity", nil, []string{verifyNeedle}},
		"hadescall cannot run": {nil, func() {
			engine.failLeaf(mintNeedle, "exitCode", "the engine went away")
		}, "2", "could not ask hades", nil, []string{verifyNeedle}},
		"hades answers no status": {nil, func() {
			engine.stdout(mintNeedle, "garbage")
		}, "2", "no status line", nil, []string{verifyNeedle}},
		"mold refuses the payload": {nil, func() {
			engine.stdout(mintNeedle, "HTTP 200\n"+toolAnswer(true, "mold app/tongs:stable: staged payload pin mismatch"))
		}, "1", "findings in layer_cast", nil, []string{verifyNeedle}},
		"the policy refuses the lane": {nil, func() {
			engine.stdout(mintNeedle, "HTTP 403\n{\"detail\":\"denied\"}")
		}, "1", "not granted layer_cast", nil, []string{verifyNeedle}},
		"mold mints another pin": {nil, func() {
			engine.stdout(mintNeedle, "HTTP 200\n"+toolAnswer(false, castResult("gffffffffffff", true)))
		}, "2", "minted pin gffffffffffff", nil, []string{verifyNeedle}},
		"the landed digest does not verify": {nil, func() {
			engine.exitCode(verifyNeedle, 10)
			engine.stdout(verifyNeedle, "Error: no matching signatures")
		}, "1", "findings in cosign verify foundry.notusmi.com/app/tongs@" + castLanded, nil, []string{bellNeedle}},
		"the check cannot run": {nil, func() {
			engine.failLeaf(verifyNeedle, "exitCode", "the engine went away")
		}, "2", "the signature check did not run", nil, []string{bellNeedle}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := castOn(t, c.tree)
			scriptACast(castPin + "\ntongs\n")
			if c.script != nil {
				c.script()
			}
			casts(t, m)
			settledOn(t, c.code, c.reason)
			for _, needle := range c.reached {
				if engine.chain(needle) == "" {
					t.Errorf("never reached %s", needle)
				}
			}
			for _, needle := range c.never {
				if engine.chain(needle) != "" {
					t.Errorf("went on to %s", needle)
				}
			}
		})
	}
}

// The doorbell is best effort: a bell that does not ring leaves the cast
// clean, and no doorbell rings nothing.
func TestTheDoorbellNeverFailsACast(t *testing.T) {
	m := castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	engine.exitCode(bellNeedle, 7)
	engine.stdout(bellNeedle, "curl: (7) Failed to connect")
	casts(t, m)
	settledOn(t, "0", "the doorbell did not ring (curl exited 7: curl: (7) Failed to connect)")

	m = castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	engine.failLeaf(bellNeedle, "exitCode", "the engine went away")
	casts(t, m)
	settledOn(t, "0", "the doorbell did not ring (")

	m = castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	castWith(t, m, dag.LoadSocketFromID("spire-agent-socket"), dag.SetSecret("registry-token", "tok"), "", false)
	settledOn(t, "0", "verified against cosign.pub")
	if engine.chain(bellNeedle) != "" {
		t.Error("no doorbell URL, and a bell rang")
	}
	if strings.Contains(engine.chain(`"/usr/local/bin/verdict"`), "doorbell") {
		t.Error("no doorbell URL, and the settle spoke of one")
	}
}

// A no-op mint is clean and says the channel already carried the payload.
func TestACastOfAnUnchangedPayloadSaysSo(t *testing.T) {
	m := castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	engine.stdout(mintNeedle, "HTTP 200\n"+toolAnswer(false, strings.Replace(castResult(castPin, true), `"noop":false`, `"noop":true`, 1)))
	casts(t, m)
	settledOn(t, "0", "already carried this pin")
}

// goNeedle is the go lane's release exec, in one chain and no other: the
// settle builds /out/verdict with `go build` too, so the needle is a release
// flag rather than the verb.
const goNeedle = `"-trimpath"`

// goCastOn is castOn over a Go binary repo: the rust workspace gone, a root
// go.mod and the star's main package in its place.
func goCastOn(t *testing.T, tree map[string]string) *FoundryTools {
	t.Helper()
	base := map[string]string{
		"Cargo.toml":               "",
		"crates/tongs/src/main.rs": "",
		"go.mod":                   "module forgejo.notusmi.com/rob/tongs\n\ngo 1.26\n",
		"cmd/tongs/main.go":        "package main\n\nfunc main() {}\n",
	}
	for k, v := range tree {
		base[k] = v
	}
	return castOn(t, base)
}

// A Go binary repo casts through the go lane: each declared binary is built
// from ./cmd/<name> with the fleet's release flags into /out, CGO off, and
// read back out of /out — cargo is never asked.
func TestAGoBinaryRepoCastsThroughTheGoLane(t *testing.T) {
	m := goCastOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	casts(t, m)
	settledOn(t, "0", "clean: cast app/tongs:stable at index 7")

	wantCalls(t, engine.chain(goNeedle),
		[]string{"withEnvVariable", `"CGO_ENABLED"`, `"0"`},
		[]string{"withExec", `["go","build","-trimpath","-ldflags=-s -w","-o","/out/tongs","./cmd/tongs"]`},
	)
	if engine.chain(goNeedle, `file(path:"/out/tongs")`) == "" {
		t.Error("the binary was not read out of the go lane's release directory")
	}
	if engine.chain(cargoNeedle) != "" {
		t.Error("a Go repo asked cargo to build")
	}
	wantCalls(t, engine.chain(`directory{withFile`), []string{"withFile", `path:"tongs"`})
	wantCalls(t, engine.chain(mintNeedle), []string{"withExec", `app/tongs:stable`, castRef + "@" + castStaged, buildSha})
}

// A module that vendors builds with -mod=vendor; one that declares several
// binaries builds each from its own ./cmd/<name>, in the record's order.
func TestTheGoLaneBuildsWhatTheRecordDeclaresTheWayTheModuleResolves(t *testing.T) {
	cases := map[string]struct {
		tree  map[string]string
		execs []string
	}{
		"vendored": {
			map[string]string{"vendor/modules.txt": "# x\n"},
			[]string{`["go","build","-mod=vendor","-trimpath","-ldflags=-s -w","-o","/out/tongs","./cmd/tongs"]`},
		},
		"two binaries": {
			map[string]string{
				"/dies/fleet/stars/tongs/slag.json": tongsRecord(`{"binaries":["tongs","tongs-agent"]}`),
				"cmd/tongs-agent/main.go":           "package main\n\nfunc main() {}\n",
			},
			[]string{
				`["go","build","-trimpath","-ldflags=-s -w","-o","/out/tongs","./cmd/tongs"]`,
				`["go","build","-trimpath","-ldflags=-s -w","-o","/out/tongs-agent","./cmd/tongs-agent"]`,
			},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := goCastOn(t, c.tree)
			scriptACast(castPin + "\ntongs\n")
			casts(t, m)
			settledOn(t, "0", "clean: cast app/tongs:stable")
			chain := engine.chain(goNeedle)
			for _, e := range c.execs {
				if !strings.Contains(chain, e) {
					t.Errorf("the go lane did not run %s\nchain: %s", e, chain)
				}
			}
		})
	}
}

// A binary that fails to compile is a finding that names its package and
// carries the compiler's words — read after ITS exec, not after the last one,
// so a later binary that compiles cannot report for it.
func TestAGoBinaryThatDoesNotCompileIsAFindingByName(t *testing.T) {
	m := goCastOn(t, map[string]string{
		"/dies/fleet/stars/tongs/slag.json": tongsRecord(`{"binaries":["tongs","tongs-agent"]}`),
		"cmd/tongs-agent/main.go":           "package main\n\nfunc main() {}\n",
	})
	scriptACast(castPin + "\ntongs\n")
	engine.exitCode(`"/out/tongs","./cmd/tongs"]`, 1)
	engine.stdout(`"/out/tongs","./cmd/tongs"]`, "./cmd/tongs/main.go:3:2: undefined: x")
	casts(t, m)
	settledOn(t, "1", "findings in go build ./cmd/tongs")
	if engine.chain(`"/out/tongs-agent"`) != "" {
		t.Error("a second binary was built after the first failed to compile")
	}
	if engine.chain(castpinNeedle) != "" || engine.chain(stageNeedle) != "" {
		t.Error("a failed build was pinned or staged")
	}
}

// A go build whose exit cannot be read did not run: a could-not-run that
// names the package, re-asked rather than settled.
func TestAGoBuildTheEngineLostCouldNotRun(t *testing.T) {
	m := goCastOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	engine.failLeaf(goNeedle, "exitCode", "the engine went away")
	casts(t, m)
	settledOn(t, "2", "the release build of ./cmd/tongs did not run")
	if engine.chain(castpinNeedle) != "" || engine.chain(stageNeedle) != "" {
		t.Error("a build the engine lost was pinned or staged")
	}
}

// A record that says binary over a tree that declares no lane to build one —
// or two — is refused by name and builds with nothing: the lane never falls
// through to cargo on a Go repo (foundry-tools #9839) or guesses between two.
func TestACastRefusesATreeThatDeclaresNoLaneOrTwo(t *testing.T) {
	cases := map[string]struct {
		tree   map[string]string
		reason string
	}{
		"no lane at all": {
			map[string]string{"Cargo.toml": "", "crates/tongs/src/main.rs": "", "README.md": "# tongs\n"},
			"declares no lane that builds one — no Cargo.toml and no go.mod at the root",
		},
		"a nested go.mod is not the root's": {
			map[string]string{"Cargo.toml": "", "crates/tongs/src/main.rs": "", "tools/x/go.mod": "module x\n", "tools/x/main.go": "package main\n"},
			"declares no lane that builds one",
		},
		"both lanes": {
			map[string]string{"go.mod": "module forgejo.notusmi.com/rob/tongs\n\ngo 1.26\n", "cmd/tongs/main.go": "package main\n\nfunc main() {}\n"},
			"declares both a Cargo.toml and a root go.mod",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := castOn(t, c.tree)
			scriptACast(castPin + "\ntongs\n")
			casts(t, m)
			settledOn(t, "1", c.reason)
			if engine.chain(cargoNeedle) != "" || engine.chain(goNeedle) != "" || engine.chain(stageNeedle) != "" {
				t.Fatal("a cast that could not name its lane built or staged")
			}
		})
	}
}

// The lane is read off the tree, and a tree that cannot be read is a
// could-not-run about the repository — re-asked, never a finding.
func TestACastWhoseTreeCannotBeReadCouldNotRun(t *testing.T) {
	cases := map[string]struct {
		script func()
		reason string
	}{
		"the root":        {func() { engine.fail("entries", "the tree went away") }, "the repository root could not be read"},
		"the module walk": {func() { engine.failLeaf("**/go.mod", "glob", "the module walk went away") }, "the tree's Go modules could not be enumerated"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := goCastOn(t, nil)
			scriptACast(castPin + "\ntongs\n")
			c.script()
			casts(t, m)
			settledOn(t, "2", c.reason)
			if engine.chain(cargoNeedle) != "" || engine.chain(goNeedle) != "" {
				t.Fatal("a cast that could not read its tree built")
			}
		})
	}
}

// The pod log names the verb the mint was asked with and what hades answered,
// so a refusal reads as the verb that was refused.
func TestACastSaysWhichVerbHadesAnswered(t *testing.T) {
	m := castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	said := sayings(t, func() { casts(t, m) })
	if !strings.Contains(said, "hephaestus minted foundry.notusmi.com/app/tongs:stable at index 7 ("+castPin+", "+castLanded+")") {
		t.Errorf("the log does not say what hephaestus minted:\n%s", said)
	}
	if !strings.Contains(said, "hades answered layer_cast HTTP 200") || strings.Contains(said, "forge_layer_cast") {
		t.Errorf("the log does not name the verb hades answered:\n%s", said)
	}
}
