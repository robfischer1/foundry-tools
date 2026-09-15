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
	moldNeedle    = `"forge_mold"`
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
	engine.stdout(moldNeedle, "HTTP 200\n"+toolAnswer(false, castResult(castPin, true)))
}

// casts runs the lane the way a landing does: the socket, the token and the
// doorbell.
func casts(t *testing.T, m *FoundryTools) {
	t.Helper()
	castWith(t, m, dag.LoadSocketFromID("spire-agent-socket"), dag.SetSecret("registry-token", "tok"), castDoorbell, false)
}

func castWith(t *testing.T, m *FoundryTools, spire *dagger.Socket, token *dagger.Secret, doorbell string, dryRun bool) {
	t.Helper()
	if err := m.Cast(context.Background(), spire, token, doorbell, "https://hades:8102", "spiffe://notusmi.com/star/hades", dryRun); err != nil {
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
	wantCalls(t, engine.chain(moldNeedle), []string{"withExec", `app/tongs:stable`, castRef + "@" + castStaged, buildSha})
	wantCalls(t, engine.chain(verifyNeedle),
		[]string{"withFile", `"/run/cosign/cosign.pub"`},
		[]string{"withExec", `"--insecure-ignore-tlog=true"`, `"foundry.notusmi.com/app/tongs@` + castLanded + `"`},
	)
	wantCalls(t, engine.chain(bellNeedle), []string{"withExec", castDoorbell, `index`, castLanded})
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
	for _, needle := range []string{stageNeedle, moldNeedle, verifyNeedle, bellNeedle} {
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
		}, "1", "findings in staging push", nil, []string{moldNeedle}},
		"the push cannot run": {nil, func() {
			engine.failLeaf(stageNeedle, "exitCode", "the engine went away")
		}, "2", "the staging push did not run", nil, []string{moldNeedle}},
		"the push answers no digest": {nil, func() {
			engine.stdout(stageNeedle, "Pushed\n")
		}, "2", "answered no digest", nil, []string{moldNeedle}},
		"hades cannot be asked": {nil, func() {
			engine.exitCode(moldNeedle, 1)
			engine.stdout(moldNeedle, "no identity within 2m0s")
		}, "2", "could not ask hades: no identity", nil, []string{verifyNeedle}},
		"hadescall cannot run": {nil, func() {
			engine.failLeaf(moldNeedle, "exitCode", "the engine went away")
		}, "2", "could not ask hades", nil, []string{verifyNeedle}},
		"hades answers no status": {nil, func() {
			engine.stdout(moldNeedle, "garbage")
		}, "2", "no status line", nil, []string{verifyNeedle}},
		"mold refuses the payload": {nil, func() {
			engine.stdout(moldNeedle, "HTTP 200\n"+toolAnswer(true, "mold app/tongs:stable: staged payload pin mismatch"))
		}, "1", "findings in forge_mold", nil, []string{verifyNeedle}},
		"the policy refuses the lane": {nil, func() {
			engine.stdout(moldNeedle, "HTTP 403\n{\"detail\":\"denied\"}")
		}, "1", "not granted forge_mold", nil, []string{verifyNeedle}},
		"mold mints another pin": {nil, func() {
			engine.stdout(moldNeedle, "HTTP 200\n"+toolAnswer(false, castResult("gffffffffffff", true)))
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
	engine.stdout(moldNeedle, "HTTP 200\n"+toolAnswer(false, strings.Replace(castResult(castPin, true), `"noop":false`, `"noop":true`, 1)))
	casts(t, m)
	settledOn(t, "0", "already carried this pin")
}
