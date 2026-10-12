package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/svidcred"
)

// REGISTRY-SVID. The flag changes nothing unless a socket came with it, and
// with both the credential is a helper the tool runs at push time: no token in
// any env var, file or mounted secret the module builds.

const (
	svidAudience = "registry.notusmi.com"
	svidHelper   = `"/usr/local/bin/docker-credential-svid"`
	// svidGetAnswer is the helper's `get` answer, with a fake token.
	svidFakeToken = "eyJ.fake.jwt-svid"
	svidGetAnswer = `{"ServerURL":"registry.notusmi.com","Username":"spiffe://notusmi.com/ci/x","Secret":"` + svidFakeToken + `"}`
)

func TestSvidLoginIsOnOnlyWithTheFlagAndTheSocket(t *testing.T) {
	sock := dag.LoadSocketFromID("spire-agent-socket")
	for name, c := range map[string]struct {
		s    svidLogin
		want bool
	}{
		"both":      {svidLogin{svidAudience, sock}, true},
		"no flag":   {svidLogin{"", sock}, false},
		"no socket": {svidLogin{svidAudience, nil}, false},
		"neither":   {svidLogin{}, false},
	} {
		if got := c.s.on(); got != c.want {
			t.Errorf("%s: on() = %v", name, got)
		}
	}
}

func TestSvidLoginOffChangesNothing(t *testing.T) {
	engine.reset()
	ctr := svidLogin{svidAudience, nil}.login(dag.Container().From("alpine"), "h")
	if _, err := ctr.Stdout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c := engine.chain(`from(address:"alpine")`); strings.Contains(c, "withFile") || strings.Contains(c, "withMountedSecret") || strings.Contains(c, "SVIDCRED") {
		t.Errorf("an off login touched the container:\n%s", c)
	}
}

func TestSvidLoginInstallsTheHelperAndCarriesNoToken(t *testing.T) {
	engine.reset()
	s := svidLogin{svidAudience, dag.LoadSocketFromID("spire-agent-socket")}
	if _, err := s.login(dag.Container().From("alpine"), "registry.notusmi.com").Stdout(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := engine.chain(`from(address:"alpine")`)
	wantCalls(t, c,
		[]string{"withFile", `path:"/usr/local/bin/docker-credential-svid"`, "permissions:493"},
		[]string{"withUnixSocket", `path:"/run/spire/agent.sock"`, `owner:"65532:65532"`},
		[]string{"withEnvVariable", `"SVIDCRED_SOCKET"`, `"unix:///run/spire/agent.sock"`},
		[]string{"withEnvVariable", `"SVIDCRED_AUDIENCE"`, `"registry.notusmi.com"`},
		[]string{"withMountedSecret", `path:"/run/docker/config.json"`},
		[]string{"withEnvVariable", `"DOCKER_CONFIG"`, `"/run/docker"`},
	)
	if strings.Contains(c, "eyJ") {
		t.Error("a token-shaped string is in the chain")
	}
}

func TestSvidConfigNamesTheHelper(t *testing.T) {
	cfg := svidcred.Config("foundry.notusmi.com")
	if !strings.Contains(cfg, `"foundry.notusmi.com":"svid"`) || strings.Contains(cfg, "auth") {
		t.Errorf("config = %s", cfg)
	}
}

// The token for an engine-side push is fetched by an exec of the helper, fed
// the host on stdin, cache-busted, and handed on as a secret.
func TestSvidTokenIsFetchedByAFreshExecAndHeldAsASecret(t *testing.T) {
	engine.reset()
	engine.stdout(svidHelper+`,"get"`, svidGetAnswer+"\n")
	s := svidLogin{svidAudience, dag.LoadSocketFromID("spire-agent-socket")}
	user, secret, err := s.token(context.Background(), "registry.notusmi.com", "t")
	if err != nil || user != "spiffe://notusmi.com/ci/x" || secret == nil {
		t.Fatalf("user %q secret %v err %v", user, secret, err)
	}
	c := engine.chain(svidHelper + `,"get"`)
	wantCalls(t, c,
		[]string{"withUnixSocket", `owner:"65532:65532"`},
		[]string{"withEnvVariable", `"SVIDCRED_FETCHED_AT"`},
		[]string{"withExec", svidHelper, `"get"`, `stdin:"registry.notusmi.com"`},
	)
	if strings.Contains(c, svidFakeToken) {
		t.Error("the token is in the chain that fetched it")
	}
	// An answer with no token is refused.
	engine.reset()
	engine.stdout(svidHelper+`,"get"`, "{}")
	if _, _, err := s.token(context.Background(), "h", "t"); err == nil {
		t.Error("an empty answer was accepted")
	}
}

// ---- cast

func castSvid(t *testing.T, m *FoundryTools, spire *dagger.Socket, token *dagger.Secret) {
	t.Helper()
	if err := m.Cast(context.Background(), spire, token, castDoorbell, "https://hades:8102", "spiffe://notusmi.com/star/hades", false, nil, svidAudience); err != nil {
		t.Fatalf("cast: %v", err)
	}
}

// With the flag and the socket, a cast needs no registry token, and every
// registry container gets the helper and the config that names it.
func TestACastWithRegistrySvidNeedsNoToken(t *testing.T) {
	m := castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	castSvid(t, m, dag.LoadSocketFromID("spire-agent-socket"), nil)
	settledOn(t, "0", "clean: cast app/tongs:stable")
	stage := engine.chain(stageNeedle)
	wantCalls(t, stage,
		[]string{"withFile", `path:"/usr/local/bin/docker-credential-svid"`},
		[]string{"withEnvVariable", `"SVIDCRED_AUDIENCE"`, `"registry.notusmi.com"`},
		[]string{"withMountedSecret", `path:"/run/docker/config.json"`},
	)
	verify := engine.chain(`"verify","--key","/run/cosign/cosign.pub"`)
	wantCalls(t, verify, []string{"withFile", `path:"/usr/local/bin/docker-credential-svid"`})
}

// The flag with no socket is not a login: the static token is still required.
func TestACastWithRegistrySvidButNoSocketKeepsTheStaticPath(t *testing.T) {
	m := castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	castSvid(t, m, nil, dag.SetSecret("registry-token", "tok"))
	settledOn(t, "2", "--spire and --registry-token are both required")
}

// Without the flag, the static path is untouched: no helper anywhere.
func TestACastWithoutRegistrySvidInstallsNoHelper(t *testing.T) {
	m := castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	casts(t, m)
	settledOn(t, "0", "clean: cast app/tongs:stable")
	for _, needle := range []string{stageNeedle, `"verify","--key","/run/cosign/cosign.pub"`} {
		if c := engine.chain(needle); strings.Contains(c, "docker-credential-svid") || strings.Contains(c, "SVIDCRED") {
			t.Errorf("the static path carries the helper:\n%s", c)
		}
	}
}

// ---- build

func tipSvid(t *testing.T, m *FoundryTools, spire *dagger.Socket, auth *dagger.Secret) {
	t.Helper()
	if err := m.Build(context.Background(), true, spire, auth, nil, nil, "",
		"registry.notusmi.com", "https://forgejo.notusmi.com/rob", "https://hades:8102", "spiffe://notusmi.com/star/hades", false, nil, false, svidAudience); err != nil {
		t.Fatalf("build: %v", err)
	}
}

// A tip with the flag and the socket publishes with no --registry-auth at all:
// the engine's push takes its login from a fresh helper exec, and the SBOM
// attach's oras gets the helper rather than a mounted credential.
func TestATipWithRegistrySvidPublishesWithoutRegistryAuth(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	engine.stdout(svidHelper+`,"get"`, svidGetAnswer+"\n")
	tipSvid(t, m, dag.LoadSocketFromID("spire-agent-socket"), nil)
	if engine.chain(svidHelper+`,"get"`) == "" {
		t.Fatal("no JWT-SVID was fetched for the push")
	}
	if engine.chain("build-registry-svid-ares") == "" {
		t.Error("the token was not handed to the push as a secret")
	}
	if engine.chain("build-registry-password") != "" {
		t.Error("the static login was read")
	}
	attach := engine.chain(sbomAttachNeedle)
	wantCalls(t, attach, []string{"withEnvVariable", `"SVIDCRED_AUDIENCE"`, `"registry.notusmi.com"`})
	if strings.Contains(attach, svidFakeToken) {
		t.Error("the token is in the attach chain")
	}
}

// The flag with no socket falls back: --registry-auth is required as ever.
func TestATipWithRegistrySvidButNoSocketStillNeedsRegistryAuth(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	tipSvid(t, m, nil, nil)
	settledOn(t, "2", "--registry-auth is required")
}

// A tip without the flag fetches no token and installs no helper.
func TestATipWithoutRegistrySvidFetchesNoToken(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	tip(t, m)
	if c := engine.chain(sbomAttachNeedle); strings.Contains(c, "docker-credential-svid") || strings.Contains(c, "SVIDCRED") {
		t.Errorf("the static path carries the helper:\n%s", c)
	}
	if engine.chain(svidHelper) != "" {
		t.Error("a token was fetched without the flag")
	}
}

// ---- bundle

func bundleSvid(t *testing.T, m *FoundryTools, spire *dagger.Socket, token *dagger.Secret) {
	t.Helper()
	if err := m.Bundle(context.Background(), dag.SetSecret("opa-key", pem64()), token, dag.SetSecret("cosign-key", pem64()),
		dag.SetSecret("cosign-password", "pw"), false, "", nil, spire, svidAudience); err != nil {
		t.Fatalf("bundle: %v", err)
	}
}

func TestSvidAudienceReachesTheVisualPush(t *testing.T) {
	visualTree(map[string]string{visualReport: visualFailReport})
	engine.exitCode(visualCheckNeedle, 1)
	engine.stdout(svidHelper+`,"get"`, svidGetAnswer+"\n")
	r := newRun(dag.Directory(), visualRepo, "").withArtifacts(nil, visualSha).
		withSpire(dag.LoadSocketFromID("spire-agent-socket")).withRegistrySvid(svidAudience)
	v := registry["ts:visual"](context.Background(), r)
	if strings.Contains(v.Reason, "no --artifact-auth") {
		t.Fatalf("the svid did not stand in for --artifact-auth:\n%s", v.Reason)
	}
	if engine.chain("visual-registry-svid") == "" || engine.chain(svidHelper+`,"get"`) == "" {
		t.Error("the artifact push did not take a fetched token")
	}
	// Flag with no socket: still not pushed.
	visualTree(map[string]string{visualReport: visualFailReport})
	engine.exitCode(visualCheckNeedle, 1)
	r = newRun(dag.Directory(), visualRepo, "").withArtifacts(nil, visualSha).withRegistrySvid(svidAudience)
	if v := registry["ts:visual"](context.Background(), r); !strings.Contains(v.Reason, "no --artifact-auth") {
		t.Errorf("no socket, and the push was attempted:\n%s", v.Reason)
	}
}

func TestGateFileKeepsTheRegistrySvidAudience(t *testing.T) {
	m := gateOn(t, cleanVector)
	if _, err := m.GateFile(context.Background(), fakeTree, gatePin, "base-sha", "visual", nil, false, nil, nil, "", nil, svidAudience); err != nil || m.registrySvid != svidAudience {
		t.Fatalf("registrySvid %q err %v", m.registrySvid, err)
	}
	again := newRun(dag.Directory(), "", "").withRegistrySvid(svidAudience).reaskedAgain("n")
	if again.registrySvid != svidAudience {
		t.Error("a re-asked run dropped the audience")
	}
}

// A bundle with the flag and the socket needs no registry token; oras and
// cosign get the helper, and no token-bearing docker config is built.
func TestABundleWithRegistrySvidNeedsNoToken(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	bundleSvid(t, m, dag.LoadSocketFromID("spire-agent-socket"), nil)
	settledOn(t, "0", "published and signed")
	push := pushed(bundlelane.PolicyDie, pinOfDie(t, bundlelane.PolicyDie))
	wantCalls(t, push,
		[]string{"withFile", `path:"/usr/local/bin/docker-credential-svid"`},
		[]string{"withEnvVariable", `"SVIDCRED_AUDIENCE"`, `"registry.notusmi.com"`},
	)
	wantCalls(t, engine.chain(`"--yes"`), []string{"withFile", `path:"/usr/local/bin/docker-credential-svid"`})
	if engine.chain("bundle-registry-config") != "" {
		t.Error("the static docker config was built")
	}
}

// The flag with no socket keeps the static path: the token is still required.
func TestABundleWithRegistrySvidButNoSocketStillNeedsTheToken(t *testing.T) {
	m := bundleOn(t, nil)
	bundleSvid(t, m, nil, nil)
	settledOn(t, "2", "--opa-signing-key and --registry-token are both required")
}

// An engine that fails the token exec, or answers nothing usable, stops the
// push as a could-not-run; the token is never guessed.
func TestARegistrySvidThatCannotBeFetchedStopsTheTip(t *testing.T) {
	for name, script := range map[string]func(){
		"exec fails":   func() { engine.fail(svidHelper+`,"get"`, "no identity") },
		"empty answer": func() { engine.stdout(svidHelper+`,"get"`, "{}") },
	} {
		m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
		scriptATip()
		script()
		tipSvid(t, m, dag.LoadSocketFromID("spire-agent-socket"), nil)
		settledOn(t, "2", "could not run")
		if engine.chain(`"publish"`) != "" && strings.Contains(engine.chain(`"publish"`), "build-registry-svid") {
			t.Errorf("%s: published anyway", name)
		}
	}
}

// Without the flag, an unreadable static credential is still a could-not-run.
func TestAnUnreadableStaticCredentialStillStopsThePush(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	engine.failLeaf("", "plaintext", "denied")
	tip(t, m)
	settledOn(t, "2", "the registry credential did not read")
}

// --sign-bases with the flag: cosign signs with the helper, not a mounted login.
func TestSigningBasesWithRegistrySvidUsesTheHelper(t *testing.T) {
	l := &buildLane{svid: svidLogin{svidAudience, dag.LoadSocketFromID("spire-agent-socket")}, registry: "registry.notusmi.com"}
	if _, err := l.registryLogin(dag.Container().From("alpine")).Stdout(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, engine.chain(`from(address:"alpine")`),
		[]string{"withFile", `path:"/usr/local/bin/docker-credential-svid"`},
		[]string{"withEnvVariable", `"DOCKER_CONFIG"`, `"/run/docker"`})
	l = &buildLane{registryAuth: dag.SetSecret("a", "{}")}
	engine.reset()
	if _, err := l.registryLogin(dag.Container().From("alpine")).Stdout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c := engine.chain(`from(address:"alpine")`); strings.Contains(c, "SVIDCRED") || !strings.Contains(c, "withMountedSecret") {
		t.Errorf("the static login changed:\n%s", c)
	}
}

// ---- govern

func TestAGovernWithRegistrySvidNeedsNoToken(t *testing.T) {
	m := governOn(t, nil)
	scriptAGovern()
	if err := m.Govern(context.Background(), dag.LoadSocketFromID("spire-agent-socket"), nil, nil, "https://hades:8102", "spiffe://notusmi.com/star/hades", false, svidAudience); err != nil {
		t.Fatal(err)
	}
	settledOn(t, "0", "govern: governance/forge-root: clean")
	wantCalls(t, engine.chain(stageNeedle), []string{"withFile", `path:"/usr/local/bin/docker-credential-svid"`})
	if engine.chain("govern-registry-config") != "" {
		t.Error("the static docker config was built")
	}
}
