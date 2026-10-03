package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/bundlelane"
)

func noOrbitDie(t *testing.T) {
	t.Helper()
	for _, die := range []string{bundlelane.ContractsDie, bundlelane.OrbitsDie} {
		if chain := engine.chain(`"push"`, `"`+die+":"); chain != "" {
			t.Fatalf("%s was pushed:\n%s", die, chain)
		}
	}
}

// orbitLanding runs a green landing whose change set is changed, and answers
// what the lane narrated.
func orbitLanding(t *testing.T, changed string, script func()) string {
	t.Helper()
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.stdout(`"--name-only"`, changed)
	if script != nil {
		script()
	}
	return sayings(t, func() { bundles(t, m) })
}

// A landing that touched a contract publishes both orbit dies under the pin,
// in the die shape data/orbits was first cast in, moves each :stable, and
// signs both — and the verdict names both digests.
func TestAContractLandingPublishesBothOrbitDies(t *testing.T) {
	said := orbitLanding(t, "orbits/urania-themis.toml\n", nil)
	settledOn(t, "0", "orbits: 1 contracts compose to 3 files of data/orbits; published and signed "+
		bundlelane.ContractsDie+"@sha256:"+strings.Repeat("1b", 32)+", "+bundlelane.OrbitsDie+"@sha256:")
	pin := pinOf(t)
	for die, files := range map[string][]string{
		bundlelane.ContractsDie: {"urania-themis.toml"},
		bundlelane.OrbitsDie:    {"orbits.json", "themis.orbit.toml", "urania.orbit.toml"},
	} {
		chain := pushed(die, pin)
		if chain == "" {
			t.Fatalf("%s:%s was not pushed", die, pin)
		}
		args := []string{"withExec", `"--artifact-type"`, `"application/vnd.hephaestus.die.v1"`, `"org.notusmi.die.source-sha=` + buildSha + `"`}
		dir := "/work/die-orbits/"
		if die == bundlelane.ContractsDie {
			dir = "/work/die-contracts/"
		}
		for _, f := range files {
			args = append(args, `"`+f+`:application/octet-stream"`)
			wantCalls(t, chain, []string{"withNewFile", `"` + dir + f + `"`})
		}
		wantCalls(t, chain, args, []string{"withWorkdir", `"` + strings.TrimSuffix(dir, "/") + `"`})
		if strings.Contains(chain, "README.md:") || strings.Contains(chain, "die-contracts/README.md") {
			t.Errorf("%s carries the README:\n%s", die, chain)
		}
		if engine.chain(`"tag"`, `"`+die+":"+pin+`"`, `"stable"`) == "" {
			t.Errorf("%s:stable was not moved to the pin", die)
		}
		for _, line := range []string{"── " + die + ": publish — immutable pin first, then move the channel ──", "pushed " + die + ":" + pin + " (" + string(rune('0'+len(files))) + " files)"} {
			if !strings.Contains(said, line) {
				t.Errorf("the lane did not say %q:\n%s", line, said)
			}
		}
	}
	// The contract's own bytes are what is staged.
	if !strings.Contains(pushed(bundlelane.ContractsDie, pin), `neighbors`) {
		t.Error("the contract's bytes were not staged")
	}
	for _, line := range []string{"── orbits GATE: every contract composes ──", "orbits: 1 contracts compose to 3 files of data/orbits"} {
		if !strings.Contains(said, line) {
			t.Errorf("the lane did not say %q", line)
		}
	}
	if pushed(bundlelane.FleetDie, pin) != "" {
		t.Error("the roster was republished on a contract landing")
	}
	if engine.chain(`"--yes"`) == "" {
		t.Error("nothing was signed")
	}
}

func TestALandingThatMissedOrbitsLeavesBothOrbitDiesAlone(t *testing.T) {
	orbitLanding(t, "policy/authz/visible.rego\n", nil)
	settledOn(t, "0", "published and signed "+bundlelane.PolicyDie)
	settledOn(t, "0", "; orbits: 1 contracts compose to 3 files of data/orbits — nothing under orbits/ changed, data/contracts and data/orbits stay where they are")
	for _, die := range []string{bundlelane.ContractsDie, bundlelane.OrbitsDie} {
		if pushed(die, pinOf(t)) != "" {
			t.Errorf("%s was published on a landing that did not touch orbits/", die)
		}
	}
}

func TestAnOrbitsReadmeLandingPublishesOnlyTheOrbitDies(t *testing.T) {
	orbitLanding(t, "orbits/README.md\n", nil)
	settledOn(t, "0", "neither has anything to publish")
	settledOn(t, "0", "; published and signed "+bundlelane.ContractsDie)
	if pushed(bundlelane.PolicyDie, pinOf(t)) != "" || pushed(bundlelane.FleetDie, pinOf(t)) != "" {
		t.Error("a README landing republished the policy or the roster")
	}
}

// Contracts that do not compose are a finding about the tree, and the orbit
// dies are not published. The policy and the roster are independent of the
// contracts and settle first, so a broken contract does not hold them back —
// the lane is red all the same.
func TestContractsThatDoNotComposeAreAFinding(t *testing.T) {
	for name, tree := range map[string]map[string]string{
		"a bad contract": {"orbits/urania-themis.toml": "version = 1\n"},
		"no contract":    {"orbits/urania-themis.toml": ""},
	} {
		t.Run(name, func(t *testing.T) {
			m := bundleOn(t, tree)
			scriptAGreenBundle()
			engine.stdout(`"--name-only"`, "orbits/urania-themis.toml\n")
			bundles(t, m)
			settledOn(t, "1", "does not compose")
			noOrbitDie(t)
		})
	}
}

// A lane that already failed stays failed, and the orbit step adds nothing.
func TestAFailedLaneIsPassedThroughUntouched(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.stdout(`"-v"`, "FAIL: 1/325")
	engine.exitCode(`"-v"`, 1)
	bundles(t, m)
	settledOn(t, "1", "opa test policy/ failed")
	if strings.Contains(engine.chain(`"/usr/local/bin/verdict"`), "orbits:") {
		t.Error("the orbit step spoke on a lane that had already failed")
	}
}

func TestAContractTheEngineCannotReadCouldNotRun(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.fail(`"orbits/urania-themis.toml"`, "the engine went away")
	bundles(t, m)
	settledOn(t, "2", "could not be read")
	noOrbitDie(t)
}

func TestAnOrbitsDirectoryTheEngineCannotListCouldNotRun(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.fail(`"orbits/*"`, "the engine went away")
	bundles(t, m)
	settledOn(t, "2", "could not be listed")
	noOrbitDie(t)
}

func TestADryRunGatesTheOrbitDiesAndPublishesNeither(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.stdout(`"--name-only"`, "orbits/urania-themis.toml\n")
	bundleWith(t, m, true, nil, nil, nil, nil)
	settledOn(t, "0", "compose to 3 files of data/orbits — dry run, data/contracts and data/orbits not published")
	nothingPushed(t)
}

func TestAStandingOrbitPinIsNotPushedAgain(t *testing.T) {
	said := orbitLanding(t, "orbits/README.md\n", func() { engine.exitCode(`"fetch"`, 0) })
	settledOn(t, "0", "published and signed")
	if chain := engine.chain(`"push"`, `"--artifact-type"`); chain != "" {
		t.Fatalf("a standing pin was pushed again:\n%s", chain)
	}
	if engine.chain(`"tag"`, `"`+bundlelane.OrbitsDie+":"+pinOf(t)+`"`, `"stable"`) == "" {
		t.Error("data/orbits:stable was not re-pointed at the standing pin")
	}
	if !strings.Contains(said, bundlelane.OrbitsDie+":"+pinOf(t)+" already stands — a pin is immutable, not re-pushing") {
		t.Errorf("the lane did not say the pin stands:\n%s", said)
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
			orbitLanding(t, "orbits/README.md\n", tc.script)
			settledOn(t, tc.code, tc.reason)
		})
	}
}
