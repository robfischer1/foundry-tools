package main

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/publishlane"
)

const (
	npmHosted = "https://nexus.example/repository/npm-hosted/"
	npmPass   = "hunter3"

	// npmPublishNeedle is in every publish's chain and in no other.
	npmPublishNeedle = `"bun","publish"`
	// npmInstallNeedle is in the install's chain, and so in every build and
	// publish built on it.
	npmInstallNeedle = `"bun","install","--frozen-lockfile"`
	// npmBuildNeedle is in a package build run without turbo.
	npmBuildNeedle = `"bun","run","build"`
	// npmTurboNeedle is in a package build run through turbo.
	npmTurboNeedle = `"bunx","turbo","run","build"`
	// npmrcNeedle is in the chain that makes the .npmrc secret and in no other.
	npmrcNeedle = `"publish-npmrc-`
	// ignoreScripts is on a publish whose build the lane already ran.
	ignoreScripts = `"--ignore-scripts"`

	stellarCoreTS = `{"name": "@forge/stellar-core-ts", "version": "0.6.0",
		"publishConfig": {"registry": "` + npmHosted + `"},
		"scripts": {"build": "tsc -p tsconfig.build.json", "prepublishOnly": "bun run build"}}`
	stellarCoreTSPackument = "https://nexus.example/repository/npm-hosted/@forge%2Fstellar-core-ts"
)

// npmOn is the module on a fetched commit whose root is an npm package.
func npmOn(t *testing.T, manifest string, extra map[string]string) *FoundryTools {
	t.Helper()
	tree := map[string]string{"package.json": manifest, "bun.lock": ""}
	for k, v := range extra {
		tree[k] = v
	}
	return npmTreeOn(t, tree)
}

func npmTreeOn(t *testing.T, tree map[string]string) *FoundryTools {
	t.Helper()
	engine.reset()
	engine.withTree(tree)
	return &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/rob/stellar-core-ts.git", Sha: buildSha}
}

// declared is a workspace member's manifest that declares the lane's registry.
func declared(name, version, scripts string) string {
	s := ""
	if scripts != "" {
		s = `, "scripts": {` + scripts + `}`
	}
	return `{"name": "` + name + `", "version": "` + version + `", "publishConfig": {"registry": "` + npmHosted + `"}` + s + `}`
}

// theiaTree is theia's shape: a private workspace root over apps/* and
// packages/*, turbo, a tracked .npmrc, and members that do and do not declare.
func theiaTree() map[string]string {
	return map[string]string{
		"package.json":                       `{"name": "theia", "version": "0.9.0", "private": true, "workspaces": ["apps/*", "packages/*"]}`,
		"bun.lock":                           "",
		"turbo.json":                         "{}",
		".npmrc":                             "@forge:registry=" + npmHosted + "\n",
		"apps/shell/package.json":            `{"name": "@theia/shell", "version": "0.1.0", "private": true, "publishConfig": {"registry": "` + npmHosted + `"}}`,
		"packages/aglaia/package.json":       declared("@forge/aglaia", "0.19.0", `"build": "tsc", "prepublishOnly": "pnpm run build"`),
		"packages/chrome/package.json":       declared("@forge/chrome", "0.7.0", `"build": "tsc", "prepublishOnly": "bun run build"`),
		"packages/theme-gijmo/package.json":  declared("@theia/theme-gijmo", "0.2.1", ""),
		"packages/graph-verbs/package.json":  `{"name": "@theia/graph-verbs", "version": "0.1.0"}`,
		"packages/arrangements/package.json": `{"name": "@tantalus/arrangements", "version": "0.1.0", "private": true}`,
	}
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

// registryAnswers scripts every packument probe: the document, then its status.
func registryAnswers(packument, status string) { engine.stdout(probeNeedle, packument+"\n"+status) }

// packumentAnswers scripts one package's probe, by its packument URL.
func packumentAnswers(name, packument, status string) {
	engine.stdout(`"`+publishlane.PackumentURL(npmHosted, name)+`"`, packument+"\n"+status)
}

// publishOf is the publish chain run in dir.
func publishOf(dir string) string {
	return engine.chain(npmPublishNeedle, `"`+strings.TrimSuffix("/src/"+dir, "/")+`"`)
}

// installs counts the lockfile installs evaluated on their own: each is read
// for its exit code once, and the builds and publishes built on one carry its
// argv too, so those are not counted.
func installs() int {
	n := 0
	for _, q := range engine.chains() {
		if strings.Contains(q, npmInstallNeedle) && strings.Contains(q, "exitCode") &&
			!strings.Contains(q, npmTurboNeedle) && !strings.Contains(q, npmBuildNeedle) && !strings.Contains(q, npmPublishNeedle) {
			n++
		}
	}
	return n
}

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

// A version the registry lacks installs the lockfile's dependencies, runs the
// build its prepublishOnly asks for, then publishes to the lane's registry with
// lifecycle scripts skipped and the credential in a mounted .npmrc — the tree's
// own lines kept, the password in no exec.
func TestANewNpmVersionInstallsBuildsThenPublishesWithTheCredentialAsASecret(t *testing.T) {
	m := npmOn(t, stellarCoreTS, map[string]string{".npmrc": "@forge:registry=" + npmHosted + "\n"})
	registryAnswers(`{"versions": {"0.5.0": {}}}`, "200")
	npmPublishes(t, m)
	settledOn(t, "0", "@forge/stellar-core-ts@0.6.0 — published to "+npmHosted)

	chain := publishOf("")
	if chain == "" {
		t.Fatal("the missing version was not published")
	}
	wantCalls(t, chain,
		[]string{"from", `"` + checks.ImageTS + `"`},
		[]string{"withExec", `"bun"`, `"install"`, `"--frozen-lockfile"`},
		[]string{"withExec", `"bun"`, `"run"`, `"build"`},
		[]string{"withMountedSecret", `"/src/.npmrc"`},
		[]string{"withEnvVariable", `"PUBLISH_UPLOADED_AT"`},
		[]string{"withExec", `"bun"`, `"publish"`, `"--registry"`, `"` + npmHosted + `"`, ignoreScripts},
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
	if engine.chain(npmTurboNeedle) != "" {
		t.Error("a tree without turbo.json was built through turbo")
	}
}

// A package with nothing to build installs nothing and runs its own lifecycle,
// and a tree with no .npmrc gets the auth line alone; a packument that 404s is a
// package never published.
func TestAnNpmPackageThatBuildsNothingPublishesWithoutAnInstall(t *testing.T) {
	m := npmOn(t, `{"name": "@theia/theme-gijmo", "version": "0.2.1"}`, nil)
	registryAnswers("Not Found", "404")
	npmPublishes(t, m)
	settledOn(t, "0", "@theia/theme-gijmo@0.2.1 — published to "+npmHosted)
	if n := installs(); n != 0 {
		t.Errorf("a package with no prepublishOnly installed its dependencies %d times", n)
	}
	chain := publishOf("")
	if chain == "" {
		t.Fatal("the never-published package was not published")
	}
	if strings.Contains(chain, ignoreScripts) || strings.Contains(chain, npmBuildNeedle) {
		t.Errorf("a package the lane did not build skipped its own lifecycle or was built:\n%s", chain)
	}
}

// Every refusal about the tree is a finding, reached before a probe or a
// credential.
func TestAnNpmTreeTheLaneMustNotPublishIsFindingsBeforeAnyProbe(t *testing.T) {
	for name, tc := range map[string]struct{ manifest, want string }{
		"a private root":     {`{"name": "stele", "version": "0.1.0", "private": true}`, "(stele) is private"},
		"a foreign registry": {`{"name": "@forge/x", "version": "1.0.0", "publishConfig": {"registry": "https://registry.npmjs.org/"}}`, "publishConfig.registry is https://registry.npmjs.org/, and this lane publishes only to " + npmHosted},
		"no version":         {`{"name": "@forge/x"}`, "names no package or no version"},
		"a nameless root":    {`{"version": "1.0.0", "workspaces": ["a"]}`, "the root package is a workspace root, and none of its members declares where it publishes"},
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

// A workspace publishes exactly the members that declare the registry and are
// not private — the root never — and builds each through turbo, once installed.
func TestAWorkspacePublishesEveryMemberThatDeclaresTheRegistry(t *testing.T) {
	m := npmTreeOn(t, theiaTree())
	packumentAnswers("@forge/aglaia", `{"versions": {"0.19.0": {}}}`, "200")
	packumentAnswers("@forge/chrome", "Not Found", "404")
	packumentAnswers("@theia/theme-gijmo", `{"versions": {"0.2.0": {}}}`, "200")
	npmPublishes(t, m)
	settledOn(t, "0", "@forge/chrome@0.7.0, @theia/theme-gijmo@0.2.1 — published to "+npmHosted+"; already released: @forge/aglaia@0.19.0")

	if n := probes(); n != 3 {
		t.Errorf("the registry was asked %d times, want once per declared member (3)", n)
	}
	for _, undeclared := range []string{"@theia/shell", "@theia/graph-verbs", "@tantalus/arrangements", "theia"} {
		if chain := engine.chain(`"` + publishlane.PackumentURL(npmHosted, undeclared) + `"`); chain != "" {
			t.Errorf("%s declares nothing, or is private, and was probed:\n%s", undeclared, chain)
		}
	}
	if n := installs(); n != 1 {
		t.Errorf("the workspace installed %d times, want once", n)
	}
	wantCalls(t, publishOf("packages/chrome"),
		[]string{"withExec", `"bun"`, `"install"`, `"--frozen-lockfile"`},
		[]string{"withExec", `"bunx"`, `"turbo"`, `"run"`, `"build"`, `"--filter=@forge/chrome"`},
		[]string{"withMountedSecret", `"/src/.npmrc"`},
		[]string{"withWorkdir", `"/src/packages/chrome"`},
		[]string{"withExec", `"bun"`, `"publish"`, `"--registry"`, `"` + npmHosted + `"`, ignoreScripts},
	)
	theme := publishOf("packages/theme-gijmo")
	if theme == "" {
		t.Fatal("the theme was not published")
	}
	if strings.Contains(theme, ignoreScripts) || strings.Contains(theme, npmTurboNeedle) {
		t.Errorf("a member with nothing to build was built or skipped its lifecycle:\n%s", theme)
	}
	if publishOf("packages/aglaia") != "" || engine.chain(`"--filter=@forge/aglaia"`) != "" {
		t.Error("a released member was built or published")
	}
}

// A member's pnpm prepublishOnly is still only a build: the lane builds it and
// skips the script, so no package manager the image lacks is ever run.
func TestAPnpmBuildIsRunByTheLaneNotByPnpm(t *testing.T) {
	m := npmTreeOn(t, theiaTree())
	packumentAnswers("@forge/aglaia", "Not Found", "404")
	packumentAnswers("@forge/chrome", `{"versions": {"0.7.0": {}}}`, "200")
	packumentAnswers("@theia/theme-gijmo", `{"versions": {"0.2.1": {}}}`, "200")
	npmPublishes(t, m)
	settledOn(t, "0", "@forge/aglaia@0.19.0 — published to "+npmHosted)
	chain := publishOf("packages/aglaia")
	if !hasCall(chain, "withExec", `"--filter=@forge/aglaia"`) || !hasCall(chain, "withExec", ignoreScripts) {
		t.Errorf("aglaia's pnpm prepublishOnly was not replaced by the lane's build:\n%s", chain)
	}
	if strings.Contains(strings.Join(engine.chains(), "\n"), `"pnpm"`) {
		t.Error("pnpm was executed")
	}
}

// Without turbo, a member's build runs in its own directory.
func TestWithoutTurboAMemberBuildsInItsOwnDirectory(t *testing.T) {
	tree := theiaTree()
	delete(tree, "turbo.json")
	m := npmTreeOn(t, tree)
	packumentAnswers("@forge/aglaia", `{"versions": {"0.19.0": {}}}`, "200")
	packumentAnswers("@forge/chrome", "Not Found", "404")
	packumentAnswers("@theia/theme-gijmo", `{"versions": {"0.2.1": {}}}`, "200")
	npmPublishes(t, m)
	settledOn(t, "0", "@forge/chrome@0.7.0 — published to "+npmHosted)
	wantCalls(t, publishOf("packages/chrome"),
		[]string{"withWorkdir", `"/src/packages/chrome"`},
		[]string{"withExec", `"bun"`, `"run"`, `"build"`},
		[]string{"withExec", `"bun"`, `"publish"`, ignoreScripts},
	)
	if engine.chain(npmTurboNeedle) != "" {
		t.Error("a workspace without turbo.json was built through turbo")
	}
}

// A prepublishOnly that does more than build is the package's to run.
func TestAPrepublishOnlyThatIsNotOnlyABuildRunsUnderThePublish(t *testing.T) {
	m := npmTreeOn(t, map[string]string{
		"package.json":              `{"name": "w", "private": true, "workspaces": ["packages/*"]}`,
		"bun.lock":                  "",
		"turbo.json":                "{}",
		"packages/odd/package.json": declared("@forge/odd", "1.0.0", `"prepublishOnly": "node check.js && bun run build"`),
	})
	packumentAnswers("@forge/odd", "Not Found", "404")
	npmPublishes(t, m)
	settledOn(t, "0", "@forge/odd@1.0.0 — published to "+npmHosted)
	chain := publishOf("packages/odd")
	if strings.Contains(chain, ignoreScripts) || strings.Contains(chain, npmTurboNeedle) {
		t.Errorf("a prepublishOnly that is more than a build was replaced by the lane's build:\n%s", chain)
	}
	if n := installs(); n != 1 {
		t.Errorf("a member whose publish runs a script installed %d times, want once", n)
	}
}

// One member that will not build does not strand the others; a failure the tree
// caused is a finding, and the settle names what did publish.
func TestAMemberThatFailsDoesNotStrandTheOthers(t *testing.T) {
	m := npmTreeOn(t, theiaTree())
	packumentAnswers("@forge/aglaia", `{"versions": {"0.19.0": {}}}`, "200")
	packumentAnswers("@forge/chrome", "Not Found", "404")
	packumentAnswers("@theia/theme-gijmo", "Not Found", "404")
	engine.exitCode(`"--filter=@forge/chrome"`, 2)
	engine.stderr(`"--filter=@forge/chrome"`, "error TS2307: Cannot find module '@forge/aglaia'")
	npmPublishes(t, m)
	settledOn(t, "1", "findings in the build of @forge/chrome@0.7.0")
	settledOn(t, "1", "published @theia/theme-gijmo@0.2.1")
	if publishOf("packages/chrome") != "" {
		t.Error("a member whose build failed was published")
	}
	if publishOf("packages/theme-gijmo") == "" {
		t.Error("a failed member stranded one that could publish")
	}
}

// When nothing failed for the tree's sake — only the registry or the engine did
// not answer — the lane is re-run, not red.
func TestMembersThatFailOnlyOnTheNetworkCouldNotRun(t *testing.T) {
	m := npmTreeOn(t, theiaTree())
	packumentAnswers("@forge/aglaia", `{"versions": {"0.19.0": {}}}`, "200")
	packumentAnswers("@forge/chrome", `{"versions": {"0.7.0": {}}}`, "200")
	packumentAnswers("@theia/theme-gijmo", "Not Found", "404")
	engine.exitCode(npmPublishNeedle, 1)
	engine.stderr(npmPublishNeedle, "error: ConnectionRefused: connection refused")
	npmPublishes(t, m)
	settledOn(t, "2", "network fault")
	if n := installs(); n != 0 {
		t.Errorf("a member with nothing to build installed %d times", n)
	}
}

// The member-level refusals: a member the lane cannot read, list or trust.
func TestAWorkspaceMemberTheLaneCannotPublishSettlesBeforeAnyProbe(t *testing.T) {
	root := `{"name": "w", "private": true, "workspaces": ["packages/*"]}`
	for name, tc := range map[string]struct {
		member string
		script func()
		code   string
		want   string
	}{
		"a declared member with no version": {`{"name": "@forge/x", "publishConfig": {"registry": "` + npmHosted + `"}}`, func() {}, "1", "packages/x/package.json declares a registry and names no package or no version"},
		"a member that does not parse":      {`{"name": `, func() {}, "1", "packages/x/package.json does not parse"},
		"a member naming another registry":  {`{"name": "@forge/x", "version": "1.0.0", "publishConfig": {"registry": "https://registry.npmjs.org/"}}`, func() {}, "1", "@forge/x's publishConfig.registry is https://registry.npmjs.org/"},
		"no declared member at all":         {`{"name": "@forge/x", "version": "1.0.0"}`, func() {}, "1", "w is a workspace root, and none of its members declares where it publishes"},
		"the workspace cannot be listed":    {declared("@forge/x", "1.0.0", ""), func() { engine.fail(`glob(`, "the engine went away") }, "2", "the workspace packages/* could not be listed"},
		"a member that cannot be read": {declared("@forge/x", "1.0.0", ""), func() {
			engine.failLeaf(`"packages/x/package.json"`, "contents", "the engine went away")
		}, "2", "packages/x/package.json could not be read"},
	} {
		t.Run(name, func(t *testing.T) {
			m := npmTreeOn(t, map[string]string{"package.json": root, "bun.lock": "", "packages/x/package.json": tc.member})
			tc.script()
			npmPublishes(t, m)
			settledOn(t, tc.code, tc.want)
			if n := probes(); n != 0 {
				t.Errorf("a workspace the lane refuses was probed %d times", n)
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

	m = npmOn(t, stellarCoreTS, nil)
	registryAnswers("Not Found", "404")
	engine.failLeaf(npmBuildNeedle, "exitCode", "connection to the engine was lost")
	npmPublishes(t, m)
	settledOn(t, "2", "the build of @forge/stellar-core-ts@0.6.0 never ran")
	noNpmPublish(t)
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

// settledWithout asserts the verdict's reason does not say text.
func settledWithout(t *testing.T, text string) {
	t.Helper()
	if chain := engine.chain(`"/usr/local/bin/verdict"`); strings.Contains(chain, text) {
		t.Errorf("the settle says %q, and nothing it counted is there:\n%s", text, chain)
	}
}

// The settle names only what happened: nothing released is not "already
// released", and nothing published is not "published".
func TestTheSettleNamesOnlyWhatHappened(t *testing.T) {
	m := npmOn(t, stellarCoreTS, nil)
	registryAnswers("Not Found", "404")
	npmPublishes(t, m)
	settledOn(t, "0", "@forge/stellar-core-ts@0.6.0 — published to "+npmHosted)
	settledWithout(t, "already released")

	m = npmOn(t, stellarCoreTS, nil)
	registryAnswers("Not Found", "404")
	engine.exitCode(npmPublishNeedle, 1)
	engine.stderr(npmPublishNeedle, "error: publish failed: 403 Forbidden")
	npmPublishes(t, m)
	settledOn(t, "1", "findings in bun publish @forge/stellar-core-ts@0.6.0")
	settledWithout(t, "; published")
}
