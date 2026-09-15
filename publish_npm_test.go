package main

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

const (
	npmHosted = "https://nexus.example/repository/npm-hosted/"
	npmPass   = "hunter3"

	// npmPublishNeedle is in the publish's chain and in no other.
	npmPublishNeedle = `"bun","publish"`
	// npmInstallNeedle is in the install's chain, and so in the publish built on it.
	npmInstallNeedle = `"bun","install","--frozen-lockfile"`
	// npmrcNeedle is in the chain that makes the .npmrc secret and in no other.
	npmrcNeedle = `"publish-npmrc-`

	stellarCoreTS = `{"name": "@forge/stellar-core-ts", "version": "0.6.0",
		"publishConfig": {"registry": "` + npmHosted + `"},
		"scripts": {"build": "tsc -p tsconfig.build.json", "prepublishOnly": "bun run build"}}`
	stellarCoreTSPackument = "https://nexus.example/repository/npm-hosted/@forge%2Fstellar-core-ts"
)

// npmOn is the module on a fetched commit whose root is an npm package.
func npmOn(t *testing.T, manifest string, extra map[string]string) *FoundryTools {
	t.Helper()
	engine.reset()
	tree := map[string]string{"package.json": manifest, "bun.lock": ""}
	for k, v := range extra {
		tree[k] = v
	}
	engine.withTree(tree)
	return &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/rob/stellar-core-ts.git", Sha: buildSha}
}

func npmPublishWith(t *testing.T, m *FoundryTools, token *dagger.Secret, registry string, dryRun bool) {
	t.Helper()
	if err := m.Publish(context.Background(), nil, publishURL, checkURL, "publisher", buildIndex, token, registry, dryRun); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

// npmPublishes runs the lane the way a landing does: the publisher's password
// and the hosted registry.
func npmPublishes(t *testing.T, m *FoundryTools) {
	t.Helper()
	npmPublishWith(t, m, dag.SetSecret("npm-token", npmPass), npmHosted, false)
}

// registryAnswers scripts the packument probe: the document, then its status.
func registryAnswers(packument, status string) { engine.stdout(probeNeedle, packument+"\n"+status) }

func noNpmPublish(t *testing.T) {
	t.Helper()
	if chain := engine.chain(npmPublishNeedle); chain != "" {
		t.Fatalf("something was published:\n%s", chain)
	}
	if chain := engine.chain(npmrcNeedle); chain != "" {
		t.Fatalf("a credential was written for a publish that must not happen:\n%s", chain)
	}
}

// The trigger is the tree: a version the registry holds publishes nothing, and
// the probe runs in the ts lane against the packument, scope escaped.
func TestAnNpmVersionTheRegistryHoldsPublishesNothing(t *testing.T) {
	m := npmOn(t, stellarCoreTS, nil)
	registryAnswers(`{"versions": {"0.5.0": {}, "0.6.0": {}}}`, "200")
	npmPublishes(t, m)
	settledOn(t, "0", "nothing to do — @forge/stellar-core-ts@0.6.0 is already on "+npmHosted)
	noNpmPublish(t)
	if engine.chain(buildNeedle) != "" {
		t.Error("an npm tree was built with uv")
	}
	wantCalls(t, engine.chain(probeNeedle),
		[]string{"from", `"` + checks.ImageTS + `"`},
		[]string{"withExec", `"curl"`, `"` + stellarCoreTSPackument + `"`},
	)
}

// A version the registry lacks installs the lockfile's dependencies (its
// prepublishOnly builds), then publishes to the lane's registry with the
// credential in a mounted .npmrc — the tree's own lines kept, the password in
// no exec.
func TestANewNpmVersionInstallsThenPublishesWithTheCredentialAsASecret(t *testing.T) {
	m := npmOn(t, stellarCoreTS, map[string]string{".npmrc": "@forge:registry=" + npmHosted + "\n"})
	registryAnswers(`{"versions": {"0.5.0": {}}}`, "200")
	npmPublishes(t, m)
	settledOn(t, "0", "@forge/stellar-core-ts@0.6.0 — published to "+npmHosted)

	chain := engine.chain(npmPublishNeedle)
	if chain == "" {
		t.Fatal("the missing version was not published")
	}
	wantCalls(t, chain,
		[]string{"from", `"` + checks.ImageTS + `"`},
		[]string{"withExec", `"bun"`, `"install"`, `"--frozen-lockfile"`},
		[]string{"withMountedSecret", `"/src/.npmrc"`},
		[]string{"withEnvVariable", `"PUBLISH_UPLOADED_AT"`},
		[]string{"withExec", `"bun"`, `"publish"`, `"--registry"`, `"` + npmHosted + `"`},
	)
	auth := base64.StdEncoding.EncodeToString([]byte("publisher:" + npmPass))
	if strings.Contains(chain, npmPass) || strings.Contains(chain, auth) {
		t.Errorf("the credential reached the publish in the clear:\n%s", chain)
	}
	secret := engine.chain(npmrcNeedle)
	if !strings.Contains(secret, "//nexus.example/repository/npm-hosted/:_auth="+auth) {
		t.Errorf("the .npmrc secret does not carry the publisher's Basic auth line:\n%s", secret)
	}
	if !strings.Contains(secret, "@forge:registry=") {
		t.Errorf("the tree's own .npmrc lines were dropped:\n%s", secret)
	}
	if n := probes(); n != 1 {
		t.Errorf("the registry was asked %d times about one package, want once", n)
	}
}

// A package with nothing to build installs nothing, and a tree with no .npmrc
// gets the auth line alone; a packument that 404s is a package never published.
func TestAnNpmPackageThatBuildsNothingPublishesWithoutAnInstall(t *testing.T) {
	m := npmOn(t, `{"name": "@theia/theme-gijmo", "version": "0.2.1"}`, nil)
	registryAnswers("Not Found", "404")
	npmPublishes(t, m)
	settledOn(t, "0", "@theia/theme-gijmo@0.2.1 — published to "+npmHosted)
	if chain := engine.chain(npmInstallNeedle); chain != "" {
		t.Errorf("a package with no prepublishOnly installed its dependencies:\n%s", chain)
	}
	if engine.chain(npmPublishNeedle) == "" {
		t.Error("the never-published package was not published")
	}
}

// Every refusal about the tree is a finding, reached before a probe or a
// credential.
func TestAnNpmTreeTheLaneMustNotPublishIsFindingsBeforeAnyProbe(t *testing.T) {
	for name, tc := range map[string]struct{ manifest, want string }{
		"a private root":     {`{"name": "stele", "version": "0.1.0", "private": true}`, "(stele) is private"},
		"a workspace root":   {`{"name": "theia", "version": "0.9.0", "private": true, "workspaces": ["packages/*"]}`, "theia is a workspace root"},
		"a foreign registry": {`{"name": "@forge/x", "version": "1.0.0", "publishConfig": {"registry": "https://registry.npmjs.org/"}}`, "publishConfig.registry is https://registry.npmjs.org/, and this lane publishes only to " + npmHosted},
		"no version":         {`{"name": "@forge/x"}`, "names no package or no version"},
		"a nameless root":    {`{"version": "1.0.0", "workspaces": ["a"]}`, "the root package is a workspace root"},
		"not JSON":           {`{"name": `, "package.json does not parse"},
	} {
		t.Run(name, func(t *testing.T) {
			m := npmOn(t, tc.manifest, nil)
			npmPublishes(t, m)
			settledOn(t, "1", tc.want)
			if n := probes(); n != 0 {
				t.Errorf("a tree the lane refuses was probed %d times", n)
			}
			noNpmPublish(t)
		})
	}
}

// npm has no --check-url: a probe that cannot answer leaves nothing to catch a
// wrong guess, so it is could-not-run and nothing is published.
func TestAnNpmProbeThatCannotAnswerIsCouldNotRun(t *testing.T) {
	for name, tc := range map[string]struct {
		script func()
		want   string
	}{
		"curl failed":                     {func() { engine.exitCode(probeNeedle, 7) }, "(curl exit 7)"},
		"an error status":                 {func() { registryAnswers("Service Unavailable", "503") }, "answered HTTP 503"},
		"a packument that does not parse": {func() { registryAnswers("<html>garbled</html>", "200") }, "does not parse"},
		"no status line":                  {func() { engine.stdout(probeNeedle, "<html>garbled") }, "no status"},
		"the engine lost the probe":       {func() { engine.failLeaf(probeNeedle, "exitCode", "connection to the engine was lost") }, "the registry probe never ran"},
	} {
		t.Run(name, func(t *testing.T) {
			m := npmOn(t, stellarCoreTS, nil)
			tc.script()
			npmPublishes(t, m)
			settledOn(t, "2", tc.want)
			noNpmPublish(t)
		})
	}
}

func TestAnNpmPublishWithNoTokenProbesNothing(t *testing.T) {
	m := npmOn(t, stellarCoreTS, nil)
	npmPublishWith(t, m, nil, npmHosted, false)
	settledOn(t, "2", "--npm-token is required")
	if n := probes(); n != 0 {
		t.Errorf("a publish that could never upload probed %d times", n)
	}
}

func TestAnNpmPublishWithARegistryThatIsNotAURLProbesNothing(t *testing.T) {
	m := npmOn(t, stellarCoreTS, nil)
	npmPublishWith(t, m, dag.SetSecret("npm-token", npmPass), "npm-hosted", false)
	settledOn(t, "2", "--npm-registry")
	if n := probes(); n != 0 {
		t.Errorf("a lane with no usable registry probed %d times", n)
	}
}

func TestAnNpmDryRunProbesAndPublishesNothing(t *testing.T) {
	m := npmOn(t, stellarCoreTS, nil)
	registryAnswers("Not Found", "404")
	npmPublishWith(t, m, nil, npmHosted, true)
	settledOn(t, "0", "@forge/stellar-core-ts@0.6.0 — would publish to "+npmHosted+" as publisher")
	noNpmPublish(t)
	if probes() != 1 {
		t.Error("a dry run must ask the registry for real")
	}
}

func TestAnNpmInstallThatFailsPublishesNothing(t *testing.T) {
	m := npmOn(t, stellarCoreTS, nil)
	registryAnswers("Not Found", "404")
	engine.exitCode(npmInstallNeedle, 1)
	engine.stderr(npmInstallNeedle, "error: lockfile had changes, but lockfile is frozen")
	npmPublishes(t, m)
	settledOn(t, "1", "findings in bun install --frozen-lockfile")
	noNpmPublish(t)
}

func TestAnNpmPublishTheRegistryRefusesIsFindingsAndOneItNeverReachedCouldNotRun(t *testing.T) {
	m := npmOn(t, stellarCoreTS, nil)
	registryAnswers("Not Found", "404")
	engine.exitCode(npmPublishNeedle, 1)
	engine.stderr(npmPublishNeedle, "error: publish failed: 403 Forbidden")
	npmPublishes(t, m)
	settledOn(t, "1", "findings in bun publish @forge/stellar-core-ts@0.6.0")

	m = npmOn(t, stellarCoreTS, nil)
	registryAnswers("Not Found", "404")
	engine.exitCode(npmPublishNeedle, 1)
	engine.stderr(npmPublishNeedle, "error: ConnectionRefused: connection refused")
	npmPublishes(t, m)
	settledOn(t, "2", "network fault")

	m = npmOn(t, stellarCoreTS, nil)
	registryAnswers("Not Found", "404")
	engine.failLeaf(npmPublishNeedle, "exitCode", "connection to the engine was lost")
	npmPublishes(t, m)
	settledOn(t, "2", "bun publish never ran for @forge/stellar-core-ts@0.6.0")
}

// The tree decides the half: a Python project stays Python even beside a
// package.json, setup.py is Python, and a tree with neither is a finding.
func TestTheTreeDecidesWhichHalfPublishes(t *testing.T) {
	m := publishOn(t, publishSdist)
	engine.withTree(map[string]string{"package.json": stellarCoreTS})
	indexAnswers("", "404")
	publishes(t, m)
	settledOn(t, "0", " 1 uploaded")
	if engine.chain(npmPublishNeedle) != "" || probes() != 1 {
		t.Error("a Python project with a package.json beside it took the npm half")
	}

	engine.reset()
	engine.withTree(map[string]string{"setup.py": "", distDir + "/" + publishSdist: "artifact"})
	indexAnswers("", "404")
	publishes(t, &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/rob/legacy.git", Sha: buildSha})
	if engine.chain(buildNeedle) == "" {
		t.Error("a setup.py project was not built with uv")
	}
	settledOn(t, "0", " 1 uploaded")

	engine.reset()
	engine.withTree(map[string]string{"README.md": "# nothing to ship\n"})
	publishes(t, &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/rob/docs.git", Sha: buildSha})
	settledOn(t, "1", "neither a pyproject.toml nor a package.json")
	if engine.chain(buildNeedle) != "" || probes() != 0 {
		t.Error("a tree with nothing to publish was built or probed")
	}
}
