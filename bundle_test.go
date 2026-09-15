package main

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/dagger"
)

const (
	bundleToken = "registry-token-in-the-clear"
	// policyData is a built policy bundle's data.json: chaos curates a verb.
	policyData = `{"authz_audience":{"star_only":{"chaos":["graph_commit_graph"],"athena":["task_create"]}}}`
	// rosterData is a built roster's data.json.
	rosterData = `{"fleet":{"map":{"athena":{},"ares":{}},"topics":["athena._ops.calls"],"stars":{"athena":{"name":"athena"},"ares":{"name":"ares"}}}}`
	// plantedDenied is the composition probe's answer when both guards fire.
	plantedDenied = `{"result":[{"expressions":[{"value":["seam to definitely-not-a-real-star is not in the fleet roster","topic not-a-registered-topic is not in the fleet roster"]}]}]}`
)

// bundleOn is the module constructed on foundry-dies at a commit the engine
// fetched, over a tree whose two tiers agree. A tree entry with an empty value
// removes that file.
func bundleOn(t *testing.T, tree map[string]string) *FoundryTools {
	t.Helper()
	engine.reset()
	base := map[string]string{
		"policy/authz/visible.rego":        "package authz.visible",
		"tests/fixtures/ouranos-self.json": `{"name":"ouranos"}`,
		"fleet/data.json":                  `{"map":{"athena":{},"ares":{}}}`,
		"fleet/stars/athena/slag.json":     "{}",
		"fleet/stars/ares/slag.json":       "{}",
		"cosign.pub":                       "-----BEGIN PUBLIC KEY-----\nabc\n-----END PUBLIC KEY-----\n",
	}
	for k, v := range tree {
		if v == "" {
			delete(base, k)
			continue
		}
		base[k] = v
	}
	engine.withTree(base)
	return &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/rob/foundry-dies.git", Sha: buildSha}
}

// scriptAGreenBundle answers every step of a landing that touched both dies
// and gates clean. ORDER MATTERS: the paper engine's last matching script wins,
// every fleet chain carries the policy build's arguments and every registry
// chain the fleet's, so later stages are scripted after earlier ones and the
// one-exec needles last.
func scriptAGreenBundle() {
	engine.stdout(`"version"`, "Version: 1.18.0\n")
	engine.stdout(`"--name-only"`, "policy/authz/visible.rego\nfleet/stars/athena/slag.json\n")
	engine.stdout(`"-v"`, "PASS: 325/325")
	engine.stdout(`"tests/fixtures/ouranos-self.json"`, `{"result":[{"expressions":[{"value":[]}]}]}`)
	engine.stdout(`"/work/bundle.tar.gz"`, policyData)
	engine.stdout(`"/work/probe-session.json"`, `{"result":[{"expressions":[{"value":["search"]}]}]}`)
	scriptTheFleet()
}

// scriptTheFleet answers the fleet's stages and the registry's. A case that
// re-scripts a policy needle every later chain carries calls it again.
func scriptTheFleet() {
	engine.stdout(`"/work/stage"`, rosterData)
	engine.stdout(`"/.manifest"`, bundlelane.FleetManifest)
	engine.stdout(`"/work/planted-seams.json"`, plantedDenied)
	engine.stdout(`"tzf"`, "/data.json\n/.manifest\n/.signatures.json\n")
	engine.stdout(`"sha256sum"`, strings.Repeat("c7", 32)+"  bundle.tar.gz\n")
	engine.exitCode(`"fetch"`, 1)
	engine.stdout(`"resolve"`, "sha256:"+strings.Repeat("1b", 32)+"\n")
	engine.exitCode(`"verify"`, 1)
	engine.exitCode(`"after-sign"`, 0)
}

func pem64() string {
	k, _ := bundlelane.EphemeralKey()
	return base64.StdEncoding.EncodeToString([]byte(k))
}

// bundles runs the lane the way a landing does: every secret.
func bundles(t *testing.T, m *FoundryTools) {
	t.Helper()
	bundleWith(t, m, false, dag.SetSecret("opa-key", pem64()), dag.SetSecret("registry-token", bundleToken),
		dag.SetSecret("cosign-key", pem64()), dag.SetSecret("cosign-password", "pw"))
}

func bundleWith(t *testing.T, m *FoundryTools, dryRun bool, opaKey, token, cosignKey, cosignPassphrase *dagger.Secret) {
	t.Helper()
	if err := m.Bundle(context.Background(), opaKey, token, cosignKey, cosignPassphrase, dryRun); err != nil {
		t.Fatalf("bundle: %v", err)
	}
}

func pinOf(t *testing.T) string {
	t.Helper()
	pin, err := bundlelane.Pin(buildSha)
	if err != nil {
		t.Fatal(err)
	}
	return pin
}

// pushed answers the push chain for die:tag, or "".
func pushed(die, tag string) string {
	return engine.chain(`"push"`, `"`+die+":"+tag+`"`)
}

func nothingPushed(t *testing.T) {
	t.Helper()
	if chain := engine.chain(`"push"`, `"--artifact-type"`); chain != "" {
		t.Fatalf("something was pushed:\n%s", chain)
	}
	if chain := engine.chain(`"--yes"`); chain != "" {
		t.Fatalf("something was signed:\n%s", chain)
	}
}

func TestTheBundleLaneRefusesATreeItDidNotFetch(t *testing.T) {
	engine.reset()
	bundles(t, &FoundryTools{Source: dag.Directory()})
	settledOn(t, "2", "--repo and --sha")
	if engine.chain(`"--revision"`) != "" {
		t.Error("a tree the engine did not fetch was built")
	}
}

func TestABundleWithoutItsSecretsBuildsNothing(t *testing.T) {
	m := bundleOn(t, nil)
	bundleWith(t, m, false, nil, dag.SetSecret("registry-token", bundleToken), nil, nil)
	settledOn(t, "2", "are all required")
	if engine.chain(`"--revision"`) != "" {
		t.Error("a bundle that could never publish built anyway")
	}
}

// The whole lane: both dies gated, pushed under their immutable pins, their
// channels moved, and every digest signed with the fleet key.
func TestALandingThatTouchedBothDiesPublishesAndSignsBoth(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	bundles(t, m)
	settledOn(t, "0", "published and signed")
	pin := pinOf(t)
	for _, die := range []string{bundlelane.PolicyDie, bundlelane.FleetDie} {
		for _, tag := range []string{pin, pin + "-signed"} {
			chain := pushed(die, tag)
			if chain == "" {
				t.Fatalf("%s:%s was not pushed", die, tag)
			}
			wantCalls(t, chain,
				[]string{"withExec", `"--artifact-type"`, `"` + bundlelane.BundleMediaType + `"`, `"org.opencontainers.image.revision=` + buildSha + `"`},
				[]string{"withMountedSecret", `"/run/docker/config.json"`},
				[]string{"withWorkdir", `"/work"`},
			)
			if strings.Contains(chain, bundleToken) {
				t.Errorf("the registry token reached the push in the clear:\n%s", chain)
			}
		}
		if engine.chain(`"tag"`, `"`+die+":"+pin+`"`, `"stable"`) == "" {
			t.Errorf("%s:stable was not moved to the pin", die)
		}
		if engine.chain(`"tag"`, `"`+die+":"+pin+`-signed"`, `"signed"`) == "" {
			t.Errorf("%s:signed was not moved to the signed pin", die)
		}
	}
	wantCalls(t, engine.chain(`"--yes"`),
		[]string{"withSecretVariable", `"COSIGN_PASSWORD"`},
		[]string{"withMountedSecret", `"/run/cosign/key"`},
		[]string{"withEnvVariable", `"DOCKER_CONFIG"`, `"/run/docker"`},
		[]string{"withExec", `"sign"`, `"--key"`, `"/run/cosign/key"`},
	)
	// The twins are built with the OPA key mounted, never passed.
	wantCalls(t, engine.chain(`"--signing-key"`), []string{"withMountedSecret", `"/run/opa/sign.key"`})
}

// A docs-only landing still runs every gate; it only publishes nothing.
func TestADocsOnlyLandingGatesEverythingAndPublishesNothing(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.stdout(`"--name-only"`, "README.md\nschema/slag-v3.schema.json\n")
	bundles(t, m)
	settledOn(t, "0", "neither has anything to publish")
	nothingPushed(t)
	if engine.chain(`"/work/planted-seams.json"`) == "" {
		t.Error("the composition gate did not run on a landing that publishes nothing")
	}
}

func TestAPolicyOnlyLandingLeavesTheRosterAlone(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.stdout(`"--name-only"`, "policy/authz/visible.rego\n")
	bundles(t, m)
	settledOn(t, "0", "published and signed")
	if pushed(bundlelane.PolicyDie, pinOf(t)) == "" {
		t.Error("the policy was not published")
	}
	if pushed(bundlelane.FleetDie, pinOf(t)) != "" {
		t.Error("the roster was republished on a landing that did not touch it")
	}
}

func TestARosterOnlyLandingPublishesTheRosterAndThePolicy(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.stdout(`"--name-only"`, "fleet/data.json\n")
	bundles(t, m)
	settledOn(t, "0", "published and signed")
	for _, die := range []string{bundlelane.PolicyDie, bundlelane.FleetDie} {
		if pushed(die, pinOf(t)) == "" {
			t.Errorf("%s was not published", die)
		}
	}
}

// A previous tip that cannot be read cannot tell, and cannot-tell publishes.
func TestALandingWhoseParentCannotBeReadPublishesBoth(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.exitCode(`"--verify"`, 1)
	bundles(t, m)
	settledOn(t, "0", "published and signed")
	for _, die := range []string{bundlelane.PolicyDie, bundlelane.FleetDie} {
		if pushed(die, pinOf(t)) == "" {
			t.Errorf("%s was not published", die)
		}
	}
	if engine.chain(`"--name-only"`) != "" {
		t.Error("a parent that does not exist was diffed against")
	}
}

func TestADryRunGatesBothTwinsWithAThrowawayKeyAndPublishesNothing(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	bundleWith(t, m, true, nil, nil, nil, nil)
	settledOn(t, "0", "dry run")
	nothingPushed(t)
	if engine.chain(`"--signing-key"`, `"/work/fleet-bundle-signed.tar.gz"`) == "" {
		t.Error("a dry run must build the signed twins for real")
	}
}

// Every gate bundle.sh ran refuses the tree it was written to refuse.
func TestEveryGateRefusesWhatItGuards(t *testing.T) {
	cases := []struct {
		name   string
		tree   map[string]string
		script func()
		code   string
		reason string
	}{
		{"the policy fails its own suite", nil, func() { engine.exitCode(`"-v"`, 2) }, "1", "does not pass its own suite"},
		{"a zero-test suite", nil, func() { engine.stdout(`"-v"`, "no tests") }, "2", "zero-test"},
		{"the repo's own star would not admit", nil, func() {
			engine.stdout(`"tests/fixtures/ouranos-self.json"`, `{"result":[{"expressions":[{"value":["ouranos has no interface"]}]}]}`)
		}, "1", "1 admission denial"},
		{"the dogfood does not evaluate", nil, func() { engine.exitCode(`"tests/fixtures/ouranos-self.json"`, 1) }, "1", "did not evaluate"},
		{"the dogfood answers an unreadable shape", nil, func() { engine.stdout(`"tests/fixtures/ouranos-self.json"`, `{}`) }, "2", "cannot read"},
		{"the policy does not build", nil, func() { engine.exitCode(`"--signing-key"`, 1) }, "1", "opa build over policy/ failed"},
		{"the twin carries no signature", nil, func() { engine.stdout(`"tzf"`, "/data.json\n/.manifest\n") }, "1", "no .signatures.json"},
		{"the twin cannot be listed", nil, func() { engine.exitCode(`"tzf"`, 2) }, "1", "no .signatures.json"},
		{"the twins drifted", nil, func() {
			engine.stdout(`"xzOf","/work/bundle-signed.tar.gz"`, `{"authz_audience":{"star_only":{"chaos":["other"]}}}`)
			scriptTheFleet()
		}, "1", "different data.json"},
		{"the bundle carries no data.json", nil, func() {
			engine.exitCode(`"xzOf","/work/bundle.tar.gz"`, 2)
			engine.stderr(`"xzOf","/work/bundle.tar.gz"`, "tar: /data.json: Not found in archive")
		}, "1", "carries no /data.json"},
		{"an unreadable data.json", nil, func() {
			engine.stdout(`"/work/bundle.tar.gz"`, "{not json")
			scriptTheFleet()
			engine.stdout(`"xzOf","/work/bundle-signed.tar.gz"`, "{not json")
		}, "1", "not JSON"},
		{"an empty star_only", nil, func() {
			engine.stdout(`"/work/bundle.tar.gz"`, `{"authz_audience":{"star_only":{}}}`)
			scriptTheFleet()
		}, "1", "empty star_only"},
		{"chaos curates nothing", nil, func() {
			engine.stdout(`"/work/bundle.tar.gz"`, `{"authz_audience":{"star_only":{"athena":["task_create"]}}}`)
			scriptTheFleet()
		}, "1", "chaos has no star_only row"},
		{"a curated verb is visible to a session", nil, func() {
			engine.stdout(`"/work/probe-session.json"`, `{"result":[{"expressions":[{"value":["search","graph_commit_graph"]}]}]}`)
		}, "1", "visible to a session principal"},
		{"the session probe does not evaluate", nil, func() { engine.exitCode(`"/work/probe-session.json"`, 1) }, "1", "did not evaluate against the built bundle"},
		{"the fleet tier is absent", map[string]string{"fleet/data.json": ""}, nil, "1", "fleet/data.json is absent"},
		{"the fleet tier is not JSON", map[string]string{"fleet/data.json": "{"}, nil, "1", "not JSON"},
		{"a shard the map lacks", map[string]string{"fleet/stars/zeus/slag.json": "{}"}, nil, "1", "no fleet/data.json map entry: zeus"},
		{"the roster does not build", nil, func() { engine.exitCode(`"/work/fleet-bundle-signed.tar.gz"`, 1) }, "1", "opa build over the staged roster failed"},
		{"the signed roster's roots moved", nil, func() {
			engine.stdout(`"/.manifest"`, `{"roots":["fleet","authz"]}`)
			engine.stdout(`"tzf"`, "/.signatures.json\n")
		}, "1", "no longer declares exactly [fleet]"},
		{"an empty roster", nil, func() {
			engine.stdout(`"/work/stage"`, `{"fleet":{"map":{},"topics":[],"stars":{}}}`)
			engine.stdout(`"/.manifest"`, bundlelane.FleetManifest)
			engine.stdout(`"/work/planted-seams.json"`, plantedDenied)
			engine.stdout(`"tzf"`, "/.signatures.json\n")
		}, "1", "empty data.fleet"},
		{"an unreadable roster", nil, func() {
			engine.stdout(`"/work/stage"`, "{")
			engine.stdout(`"/.manifest"`, bundlelane.FleetManifest)
			engine.stdout(`"/work/planted-seams.json"`, plantedDenied)
			engine.stdout(`"tzf"`, "/.signatures.json\n")
		}, "1", "not JSON"},
		{"the guards stopped firing", nil, func() {
			engine.stdout(`"/work/planted-seams.json"`, `{"result":[{"expressions":[{"value":["seam to definitely-not-a-real-star is not in the fleet roster"]}]}]}`)
			engine.stdout(`"tzf"`, "/.signatures.json\n")
		}, "1", "1/2 roster denials"},
		{"the composition probe answers an unreadable shape", nil, func() {
			engine.stdout(`"/work/planted-seams.json"`, `{}`)
			engine.stdout(`"tzf"`, "/.signatures.json\n")
		}, "2", "cannot read"},
		{"the dies do not compose", nil, func() { engine.exitCode(`"/work/planted-seams.json"`, 1) }, "1", "does not compose"},
		{"the policy will not build for the composition", nil, func() { engine.exitCode(`"gate"`, 1) }, "1", "for the composition gate failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := bundleOn(t, c.tree)
			scriptAGreenBundle()
			if c.script != nil {
				c.script()
			}
			bundles(t, m)
			settledOn(t, c.code, c.reason)
			nothingPushed(t)
		})
	}
}

// A step the engine never ran is could-not-run, whichever step it was.
func TestAStepTheEngineLostCouldNotRun(t *testing.T) {
	for name, needle := range map[string]string{
		"opa test":             `"-v"`,
		"the dogfood":          `"tests/fixtures/ouranos-self.json"`,
		"the policy build":     `"--ignore"`,
		"the twin listing":     `"tzf"`,
		"the session probe":    `"/work/probe-session.json"`,
		"the history":          `"--verify"`,
		"the change set":       `"--name-only"`,
		"the composition gate": `"/work/planted-seams.json"`,
	} {
		t.Run(name, func(t *testing.T) {
			m := bundleOn(t, nil)
			scriptAGreenBundle()
			engine.failLeaf(needle, "exitCode", "the engine went away")
			bundles(t, m)
			settledOn(t, "2", "could not run")
			nothingPushed(t)
		})
	}
}

// A pin is immutable: one that already resolves is never pushed again, and
// the channel is re-pointed at it all the same.
func TestAPinThatAlreadyResolvesIsNeverPushedAgain(t *testing.T) {
	for name, layer := range map[string]string{
		"the same layer":      "sha256:" + strings.Repeat("c7", 32),
		"a rebuild's new one": "sha256:" + strings.Repeat("99", 32),
	} {
		t.Run(name, func(t *testing.T) {
			m := bundleOn(t, nil)
			scriptAGreenBundle()
			engine.exitCode(`"fetch"`, 0)
			engine.stdout(`"fetch"`, `{"layers":[{"digest":"`+layer+`"}]}`)
			bundles(t, m)
			settledOn(t, "0", "published and signed")
			if chain := engine.chain(`"push"`, `"--artifact-type"`); chain != "" {
				t.Fatalf("a pin that resolves was pushed again:\n%s", chain)
			}
			if engine.chain(`"tag"`, `"stable"`) == "" {
				t.Error("the channel was not re-pointed at the standing pin")
			}
		})
	}
}

func TestADigestThatAlreadyVerifiesIsNotSignedTwice(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.exitCode(`"verify"`, 0)
	bundles(t, m)
	settledOn(t, "0", "published and signed")
	if chain := engine.chain(`"--yes"`); chain != "" {
		t.Fatalf("a verified digest was signed again:\n%s", chain)
	}
}

func TestTheRegistryAndTheSignerSettleByWhatTheyAnswered(t *testing.T) {
	cases := []struct {
		name   string
		script func()
		code   string
		reason string
	}{
		{"the registry refuses the push", func() {
			engine.exitCode(`"--artifact-type"`, 1)
			engine.stderr(`"--artifact-type"`, "Error: unauthorized: authentication required")
		}, "1", "findings in oras push"},
		{"the registry does not answer the push", func() {
			engine.exitCode(`"--artifact-type"`, 1)
			engine.stderr(`"--artifact-type"`, "Error: dial tcp: connection refused")
		}, "2", "network fault"},
		{"the channel cannot be moved", func() {
			engine.exitCode(`"tag"`, 1)
			engine.stderr(`"tag"`, "Error: denied")
		}, "1", "findings in oras tag"},
		{"the pin resolves to nothing", func() { engine.stdout(`"resolve"`, "") }, "2", "did not resolve to a digest"},
		{"the resolve fails", func() { engine.exitCode(`"resolve"`, 1) }, "2", "did not resolve to a digest"},
		{"the build cannot be hashed", func() { engine.stdout(`"sha256sum"`, "") }, "2", "could not be hashed"},
		{"cosign refuses to sign", func() {
			engine.exitCode(`"--yes"`, 1)
			engine.stderr(`"--yes"`, "Error: signing: getting key: decrypt: encrypted: decryption failed")
		}, "1", "findings in cosign sign"},
		{"the fresh signature does not verify", func() { engine.exitCode(`"after-sign"`, 1) }, "1", "findings in cosign verify"},
		{"the registry token does not read", func() { engine.failLeaf(`"registry-token"`, "plaintext", "secret vanished") }, "2", "registry token did not read"},
		{"the cosign key does not read", func() { engine.failLeaf(`"cosign-key"`, "plaintext", "secret vanished") }, "2", "cosign key did not read"},
		{"the engine loses the hash", func() { engine.failLeaf(`"sha256sum"`, "exitCode", "engine went away") }, "2", "never ran"},
		{"the engine loses the manifest fetch", func() { engine.failLeaf(`"fetch"`, "exitCode", "engine went away") }, "2", "never ran"},
		{"the engine loses the push", func() { engine.failLeaf(`"--artifact-type"`, "exitCode", "engine went away") }, "2", "never ran"},
		{"the engine loses the tag", func() { engine.failLeaf(`"tag"`, "exitCode", "engine went away") }, "2", "never ran"},
		{"the engine loses the resolve", func() { engine.failLeaf(`"resolve"`, "exitCode", "engine went away") }, "2", "never ran"},
		{"the engine loses the signature check", func() { engine.failLeaf(`"verify"`, "exitCode", "engine went away") }, "2", "cosign verify never ran"},
		{"the engine loses the signature", func() { engine.failLeaf(`"--yes"`, "exitCode", "engine went away") }, "2", "cosign sign never ran"},
		{"the engine loses the check after signing", func() { engine.failLeaf(`"after-sign"`, "exitCode", "engine went away") }, "2", "cosign verify never ran"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := bundleOn(t, nil)
			scriptAGreenBundle()
			c.script()
			bundles(t, m)
			settledOn(t, c.code, c.reason)
		})
	}
}

func TestABadlyWrappedKeyCouldNotRun(t *testing.T) {
	for name, keys := range map[string][2]string{
		"the OPA key":    {"not base64!", pem64()},
		"the cosign key": {pem64(), base64.StdEncoding.EncodeToString([]byte("not a pem"))},
	} {
		t.Run(name, func(t *testing.T) {
			m := bundleOn(t, nil)
			scriptAGreenBundle()
			bundleWith(t, m, false, dag.SetSecret("opa-key", keys[0]), dag.SetSecret("registry-token", bundleToken),
				dag.SetSecret("cosign-key", keys[1]), dag.SetSecret("cosign-password", "pw"))
			settledOn(t, "2", "_KEY")
		})
	}
}

// An opa that is not the pin grades a different rego language, so nothing is
// built with it.
func TestAnOpaThatIsNotThePinCouldNotRun(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.stdout(`"version"`, "Version: 0.70.0\n")
	bundles(t, m)
	settledOn(t, "2", "could not run")
	if engine.chain(`"--revision"`) != "" {
		t.Error("the bundles were built by an opa that is not the pin")
	}
}

func TestTheOPAKeyThatDoesNotReadCouldNotRun(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.failLeaf(`"opa-key"`, "plaintext", "secret vanished")
	bundles(t, m)
	settledOn(t, "2", "OPA signing key did not read")
	if engine.chain(`"--revision"`) != "" {
		t.Error("the bundles were built without their signing key")
	}
}
