package main

import (
	"strings"
	"testing"
)

// THE KIT CAST, THROUGH THE REAL LANE. foundry-stocks casts a kit, not a
// binary: the payload is furnace's render under die/, the furnace that renders
// it is the signed app/furnace:stable, and nothing runs until it verified.
const (
	furnaceDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	furnaceRef    = "foundry.notusmi.com/app/furnace@" + furnaceDigest
	kitRef        = "foundry.notusmi.com/staging/app-forge-user:" + castPin

	resolveNeedle = `"oras","resolve"`
	pullNeedle    = `"oras","pull"`
	dieNeedle     = `"furnace","die"`
	// furnaceVerify is the signature check on the FURNACE, as against the one on
	// the cast's own landed digest: both are `cosign verify`.
	furnaceVerify = `"verify","--key"`
)

// kitRecord is foundry-stocks's record: produces binary (the one word the door
// maps to the cast lane), a kit instead of binaries, and units beside the die.
const kitRecord = `{"$schema":"https://forgejo.notusmi.com/rob/foundry-dies/schema/slag-v3.schema.json","meta":{"name":"foundry-stocks","produces":["binary"]},"tools":{"cast":{"kit":"forge-user","payload_extra":["delivery/units"]}}}`

// kitOn is the module constructed on foundry-stocks at a commit the engine
// fetched. There is no Cargo.toml: nothing here compiles.
func kitOn(t *testing.T, tree map[string]string) *FoundryTools {
	t.Helper()
	engine.reset()
	base := map[string]string{
		"cosign.pub":                                 "-----BEGIN PUBLIC KEY-----\nabc\n-----END PUBLIC KEY-----\n",
		"delivery/units/forge-user-lay.service":      "[Service]\n",
		"/dies/fleet/stars/foundry-stocks/slag.json": kitRecord,
	}
	for k, v := range tree {
		if v == "" {
			delete(base, k)
			continue
		}
		base[k] = v
	}
	engine.withTree(base)
	return &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/foundry/foundry-stocks.git", Sha: buildSha}
}

// scriptAKitCast answers every step of a kit cast that lands: the furnace's
// digest, the pin, the staging push and the mint.
func scriptAKitCast() {
	engine.stdout(resolveNeedle, furnaceDigest+"\n")
	engine.stdout(castpinNeedle, castPin+"\ndie/.claude/forge/managed-settings.json\nunits/forge-user-lay.service\n")
	engine.stdout(stageNeedle, castStaged+"\n")
	engine.stdout(mintNeedle, "HTTP 200\n"+toolAnswer(false, castResult(castPin, true)))
}

// A kit cast resolves the furnace once, verifies THAT DIGEST against the
// repo's own key, pulls THAT DIGEST, renders the kit at the landing commit from
// the door, and ships die/ and units/ as app/forge-user.
func TestAKitCastRendersWithTheVerifiedFurnaceAndShipsItsDie(t *testing.T) {
	m := kitOn(t, nil)
	scriptAKitCast()
	casts(t, m)
	settledOn(t, "0", "clean: cast app/forge-user:stable at index 7 ("+castPin+", "+castLanded+")")

	wantCalls(t, engine.chain(resolveNeedle),
		[]string{"withMountedSecret", `"/run/docker/config.json"`},
		[]string{"withExec", `"foundry.notusmi.com/app/furnace:stable"`},
	)
	wantCalls(t, engine.chain(furnaceVerify, furnaceRef),
		[]string{"withFile", `"/run/cosign/cosign.pub"`},
		[]string{"withExec", `"--insecure-ignore-tlog=true"`, `"` + furnaceRef + `"`},
	)
	wantCalls(t, engine.chain(pullNeedle), []string{"withExec", `"-o","/furnace"`, `"` + furnaceRef + `"`})
	wantCalls(t, engine.chain(dieNeedle),
		[]string{"from", "rust:1.97.0"},
		[]string{"withEnvVariable", `"FURNACE_SOURCE"`, `"https://git.notusmi.com/foundry/foundry-stocks.git@` + buildSha + `"`},
		[]string{"withExec", `["furnace","die","forge-user","--provider","claude","--dest","/die"]`},
	)
	// The render is read out of the container's /die, and the extra rides beside it.
	if engine.chain(dieNeedle, `directory(path:"/die")`) == "" {
		t.Error("the render was not read out of the container's /die")
	}
	for _, want := range []string{`withDirectory(path:"die"`, `withDirectory(path:"units"`} {
		if engine.chain(`directory{withDirectory`, want) == "" {
			t.Errorf("the payload lacks %s", want)
		}
	}
	// Staged and minted under the KIT's name, on the app kind.
	wantCalls(t, engine.chain(stageNeedle), []string{"withExec", `"` + kitRef + `"`})
	wantCalls(t, engine.chain(mintNeedle), []string{"withExec", `app/forge-user:stable`, kitRef + "@" + castStaged, buildSha})
	wantCalls(t, engine.chain(verifyNeedle, castLanded),
		[]string{"withExec", `"foundry.notusmi.com/app/forge-user@` + castLanded + `"`},
	)
	// Nothing was compiled.
	if engine.chain(cargoNeedle) != "" {
		t.Error("a kit cast ran cargo")
	}
	// The furnace was verified BEFORE it was pulled, and both before the render.
	order := map[string]int{}
	for i, c := range engine.chains() {
		for _, n := range []string{resolveNeedle, furnaceRef + `"]`, pullNeedle, dieNeedle} {
			if _, seen := order[n]; !seen && strings.Contains(c, n) {
				order[n] = i
			}
		}
	}
	if !(order[resolveNeedle] < order[pullNeedle] && order[pullNeedle] < order[dieNeedle]) {
		t.Errorf("the lane did not resolve, pull and render in that order: %v", order)
	}
}

// A dry run renders and pins for real with no credentials at all: the furnace
// is read anonymously, and nothing is staged, minted or verified.
func TestAKitDryRunRendersAndPinsAndPublishesNothing(t *testing.T) {
	m := kitOn(t, nil)
	scriptAKitCast()
	castWith(t, m, nil, nil, castDoorbell, true)
	settledOn(t, "0", "clean: dry run — 2 file(s) pin to "+castPin+" for app/forge-user:stable")
	for _, needle := range []string{resolveNeedle, pullNeedle, dieNeedle, castpinNeedle} {
		if engine.chain(needle) == "" {
			t.Errorf("a dry run never reached %s", needle)
		}
	}
	for _, needle := range []string{stageNeedle, mintNeedle, bellNeedle} {
		if engine.chain(needle) != "" {
			t.Errorf("a dry run reached %s", needle)
		}
	}
}

// Every step that fails stops the kit cast where it failed, names what it
// means, and nothing after it runs — above all, a furnace that did not verify
// is never executed.
func TestAKitCastThatFailsStopsWhereItFailed(t *testing.T) {
	cases := map[string]struct {
		script       func()
		code, reason string
		never        []string
	}{
		"oras cannot be provisioned": {func() {
			engine.fail(`http(url:"https://github.com/oras`, "no route to host")
		}, "2", "oras could not be provisioned", []string{resolveNeedle}},
		"the furnace does not resolve": {func() {
			engine.failLeaf(resolveNeedle, "exitCode", "the engine went away")
		}, "2", "app/furnace:stable did not resolve", []string{furnaceVerify, dieNeedle}},
		"the registry refuses the resolve": {func() {
			engine.exitCode(resolveNeedle, 1)
			engine.stdout(resolveNeedle, "Error: unauthorized")
		}, "1", "findings in oras resolve foundry.notusmi.com/app/furnace", []string{pullNeedle, dieNeedle}},
		"the resolve answers no digest": {func() {
			engine.stdout(resolveNeedle, "nothing\n")
		}, "2", "resolved to no digest", []string{pullNeedle, dieNeedle}},
		"the furnace does not verify": {func() {
			engine.exitCode(furnaceVerify, 10)
			engine.stdout(furnaceVerify, "Error: no matching signatures")
		}, "1", "findings in cosign verify " + furnaceRef, []string{pullNeedle, dieNeedle}},
		"the signature check cannot run": {func() {
			engine.failLeaf(furnaceVerify, "exitCode", "the engine went away")
		}, "2", "the signature check on " + furnaceRef + " did not run", []string{pullNeedle, dieNeedle}},
		"the pull is refused": {func() {
			engine.exitCode(pullNeedle, 1)
			engine.stdout(pullNeedle, "Error: manifest unknown")
		}, "1", "findings in oras pull " + furnaceRef, []string{dieNeedle}},
		"the pull cannot run": {func() {
			engine.failLeaf(pullNeedle, "exitCode", "the engine went away")
		}, "2", furnaceRef + " did not pull", []string{dieNeedle}},
		"the bundle carries no furnace": {func() {
			engine.failLeaf(`"/furnace/furnace"`, "size", "no such file")
		}, "1", "carries no furnace binary", []string{dieNeedle}},
		"furnace refuses the kit": {func() {
			engine.exitCode(dieNeedle, 1)
			engine.stdout(dieNeedle, "Error: unknown kit forge-user")
		}, "1", "findings in furnace die forge-user", []string{castpinNeedle}},
		"furnace cannot run": {func() {
			engine.failLeaf(dieNeedle, "exitCode", "the engine went away")
		}, "2", "furnace die forge-user did not run", []string{castpinNeedle}},
		"the registry token cannot be read": {func() {
			engine.failLeaf("registry-token", "plaintext", "the secret went away")
		}, "2", "the registry token did not read", []string{resolveNeedle}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := kitOn(t, nil)
			scriptAKitCast()
			if c.script != nil {
				c.script()
			}
			casts(t, m)
			settledOn(t, c.code, c.reason)
			for _, needle := range append(c.never, stageNeedle, mintNeedle) {
				if engine.chain(needle) != "" {
					t.Errorf("went on to %s", needle)
				}
			}
		})
	}
}
