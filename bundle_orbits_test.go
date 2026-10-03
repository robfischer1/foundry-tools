package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/bundlelane"
)

// A landing that touched a contract publishes both orbit dies under the pin,
// in the die shape data/orbits was first cast in, moves each :stable, and
// signs both.
func TestAContractLandingPublishesBothOrbitDies(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.stdout(`"--name-only"`, "orbits/urania-themis.toml\n")
	bundles(t, m)
	settledOn(t, "0", "published and signed")
	pin := pinOf(t)
	for die, files := range map[string][]string{
		bundlelane.ContractsDie: {`"urania-themis.toml:application/octet-stream"`},
		bundlelane.OrbitsDie:    {`"orbits.json:application/octet-stream"`, `"themis.orbit.toml:application/octet-stream"`, `"urania.orbit.toml:application/octet-stream"`},
	} {
		chain := pushed(die, pin)
		if chain == "" {
			t.Fatalf("%s:%s was not pushed", die, pin)
		}
		wantCalls(t, chain, append([]string{"withExec", `"--artifact-type"`, `"application/vnd.hephaestus.die.v1"`,
			`"org.notusmi.die.source-sha=` + buildSha + `"`}, files...))
		if strings.Contains(chain, "README.md:") {
			t.Errorf("%s carries the README:\n%s", die, chain)
		}
		if engine.chain(`"tag"`, `"`+die+":"+pin+`"`, `"stable"`) == "" {
			t.Errorf("%s:stable was not moved to the pin", die)
		}
	}
	// Each die is staged in its own directory and pushed from it, so a layer's
	// title is the bare file name (orbitcompose.Payloads proves the bytes).
	wantCalls(t, pushed(bundlelane.ContractsDie, pin),
		[]string{"withDirectory", `"/work/die-contracts"`}, []string{"withWorkdir", `"/work/die-contracts"`})
	wantCalls(t, pushed(bundlelane.OrbitsDie, pin),
		[]string{"withDirectory", `"/work/die-orbits"`}, []string{"withWorkdir", `"/work/die-orbits"`})
	if pushed(bundlelane.PolicyDie, pin) == "" {
		t.Error("orbits/ is outside the policy's ignore list, so the policy publishes too")
	}
	if pushed(bundlelane.FleetDie, pin) != "" {
		t.Error("the roster was republished on a contract landing")
	}
	if engine.chain(`"--yes"`) == "" {
		t.Error("nothing was signed")
	}
}

func TestALandingThatMissedOrbitsLeavesBothOrbitDiesAlone(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	bundles(t, m)
	settledOn(t, "0", "published and signed")
	for _, die := range []string{bundlelane.ContractsDie, bundlelane.OrbitsDie} {
		if pushed(die, pinOf(t)) != "" {
			t.Errorf("%s was published on a landing that did not touch orbits/", die)
		}
	}
}

func TestAnOrbitsOnlyReadmeLandingPublishesOnlyTheOrbitDies(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.stdout(`"--name-only"`, "orbits/README.md\n")
	bundles(t, m)
	settledOn(t, "0", "published and signed")
	if pushed(bundlelane.PolicyDie, pinOf(t)) != "" || pushed(bundlelane.FleetDie, pinOf(t)) != "" {
		t.Error("a README landing republished the policy or the roster")
	}
	if pushed(bundlelane.OrbitsDie, pinOf(t)) == "" {
		t.Error("data/orbits was not published")
	}
}

// Contracts that do not compose are a finding about the tree, and nothing is
// published — not even the dies the contracts do not feed.
func TestContractsThatDoNotComposeAreAFinding(t *testing.T) {
	for name, tree := range map[string]map[string]string{
		"a bad contract": {"orbits/urania-themis.toml": "version = 1\n"},
		"no contract":    {"orbits/urania-themis.toml": ""},
	} {
		t.Run(name, func(t *testing.T) {
			m := bundleOn(t, tree)
			scriptAGreenBundle()
			bundles(t, m)
			settledOn(t, "1", "does not compose")
			nothingPushed(t)
		})
	}
}

func TestAContractTheEngineCannotReadCouldNotRun(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.fail(`"orbits/urania-themis.toml"`, "the engine went away")
	bundles(t, m)
	settledOn(t, "2", "could not be read")
	nothingPushed(t)
}

func TestAnOrbitsDirectoryTheEngineCannotListCouldNotRun(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.fail(`"orbits/*"`, "the engine went away")
	bundles(t, m)
	settledOn(t, "2", "could not be listed")
	nothingPushed(t)
}

func TestADryRunSaysItWouldPublishTheOrbitDies(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.stdout(`"--name-only"`, "orbits/urania-themis.toml\n")
	bundleWith(t, m, true, nil, nil, nil, nil)
	settledOn(t, "0", "orbits=true")
	nothingPushed(t)
}

func TestAStandingOrbitPinIsNotPushedAgain(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.stdout(`"--name-only"`, "orbits/README.md\n")
	engine.exitCode(`"fetch"`, 0)
	bundles(t, m)
	settledOn(t, "0", "published and signed")
	if chain := engine.chain(`"push"`, `"--artifact-type"`); chain != "" {
		t.Fatalf("a standing pin was pushed again:\n%s", chain)
	}
	if engine.chain(`"tag"`, `"`+bundlelane.OrbitsDie+":"+pinOf(t)+`"`, `"stable"`) == "" {
		t.Error("data/orbits:stable was not re-pointed at the standing pin")
	}
}

// On a landing that publishes the orbit dies alone, each registry and signer
// step settles by what it answered: the orbit chain is the first to reach
// each of them.
func TestAnOrbitDieSettlesByWhatTheRegistryAndSignerAnswered(t *testing.T) {
	push := `"org.notusmi.die.source-sha=` + buildSha + `"`
	for name, tc := range map[string]struct {
		script       func()
		code, reason string
	}{
		"the registry refuses the push": {func() {
			engine.exitCode(push, 1)
			engine.stderr(push, "Error: unauthorized: authentication required")
		}, "1", "findings in oras push"},
		"the engine loses the fetch":       {func() { engine.failLeaf(`"fetch"`, "exitCode", "engine went away") }, "2", "never ran"},
		"the engine loses the push":        {func() { engine.failLeaf(push, "exitCode", "engine went away") }, "2", "never ran"},
		"the registry token does not read": {func() { engine.failLeaf(`"registry-token"`, "plaintext", "secret vanished") }, "2", "registry token did not read"},
		"the cosign key does not read":     {func() { engine.failLeaf(`"cosign-key"`, "plaintext", "secret vanished") }, "2", "cosign key did not read"},
		"cosign refuses to sign": {func() {
			engine.exitCode(`"--yes"`, 1)
			engine.stderr(`"--yes"`, "Error: signing: decryption failed")
		}, "1", "findings in cosign sign"},
	} {
		t.Run(name, func(t *testing.T) {
			m := bundleOn(t, nil)
			scriptAGreenBundle()
			engine.stdout(`"--name-only"`, "orbits/README.md\n")
			tc.script()
			bundles(t, m)
			settledOn(t, tc.code, tc.reason)
		})
	}
}
