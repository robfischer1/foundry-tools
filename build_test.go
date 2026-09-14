package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// buildSha is the image lane's test revision: a commit the fetched tree is at.
const buildSha = imageRevision

// buildOn is the module constructed on a commit the engine fetched, standing
// on the tree a test declares.
func buildOn(t *testing.T, tree map[string]string) *FoundryTools {
	t.Helper()
	engine.reset()
	engine.withTree(tree)
	return &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/rob/ares.git", Sha: buildSha}
}

// pull runs the lane the way a pull does: no credentials and no socket.
func pull(t *testing.T, m *FoundryTools) {
	t.Helper()
	if err := m.Build(context.Background(), false, nil, nil, nil, nil, "",
		"registry.notusmi.com", "https://forgejo.notusmi.com/rob", "https://hades:8102", "spiffe://notusmi.com/star/hades"); err != nil {
		t.Fatalf("build: %v", err)
	}
}

// tip runs the lane the way the default branch's tip does: the registry
// credential, the CI key and its password, and the pod's SPIRE socket.
func tip(t *testing.T, m *FoundryTools) {
	t.Helper()
	auth := dag.SetSecret("registry-auth", `{"auths":{"registry.notusmi.com":{"username":"publisher","password":"hunter2"}}}`)
	key := dag.SetSecret("cosign-key", base64.StdEncoding.EncodeToString([]byte("-----BEGIN ENCRYPTED SIGSTORE PRIVATE KEY-----")))
	password := dag.SetSecret("cosign-password", "pw")
	// A module has no Host to open a socket on; the socket a caller forwards
	// arrives as an id, which is what the lane receives.
	if err := m.Build(context.Background(), true, dag.LoadSocketFromID("spire-agent-socket"), auth, key, password, "https://nexus.example/simple",
		"registry.notusmi.com", "https://forgejo.notusmi.com/rob", "https://hades:8102", "spiffe://notusmi.com/star/hades"); err != nil {
		t.Fatalf("build: %v", err)
	}
}

// toolAnswer is hades's tool-call envelope around text.
func toolAnswer(isError bool, text string) string {
	b, _ := json.Marshal(map[string]any{"isError": isError, "content": []map[string]string{{"type": "text", "text": text}}})
	return string(b)
}

// scriptATip scripts every step of a tip that goes through: a source change,
// the public key, the SBOM, and hades stamping the permit.
func scriptATip() {
	engine.stdout("--name-only", "cmd/ares/main.go\n")
	engine.stdout(`"public-key"`, "-----BEGIN PUBLIC KEY-----\nabc\n-----END PUBLIC KEY-----")
	engine.stdout(`"registry:registry.notusmi.com/rob/ares@sha256:`, `{"bomFormat":"CycloneDX","components":[]}`)
	engine.stdout(`"forge_mold"`, "HTTP 200\n"+toolAnswer(false, `{"digest":"sha256:eee","pushed_ref":"registry.notusmi.com/rob/ares:stable"}`))
}

// settledOn asserts the verdict exec the lane ended on: its code and a piece
// of its reason.
func settledOn(t *testing.T, code, reason string) {
	t.Helper()
	chain := engine.chain(`"/usr/local/bin/verdict"`)
	if chain == "" {
		t.Fatal("the lane never settled: no verdict exec reached the engine")
	}
	wantCalls(t, chain, []string{"withExec", `"/usr/local/bin/verdict"`, `"` + code + `"`, reason})
}

// A tree the engine did not fetch has no commit to build: could-not-run, and
// nothing is built.
func TestTheBuildLaneRefusesATreeItDidNotFetch(t *testing.T) {
	engine.reset()
	pull(t, &FoundryTools{Source: dag.Directory()})
	settledOn(t, "2", "construct the module with --repo and --sha")
	if engine.chain("dockerBuild") != "" {
		t.Fatal("an unfetched tree was built")
	}
}

// A tip publishes, signs and permits; without the credentials and the socket
// for that it cannot run, and it says so before looking at the tree.
func TestATipBuildWithoutItsCredentialsIsCouldNotRun(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	if err := m.Build(context.Background(), true, nil, nil, nil, nil, "",
		"registry.notusmi.com", "https://forgejo.notusmi.com/rob", "https://hades:8102", "spiffe://notusmi.com/star/hades"); err != nil {
		t.Fatal(err)
	}
	settledOn(t, "2", "are all required")
	if engine.chain("dockerBuild") != "" || engine.chain("--name-only") != "" {
		t.Fatal("a tip with no credentials went on to read or build the tree")
	}
}

// A push that changed only inert paths builds nothing and settles clean.
func TestAnInertOnlyPushStandsDownWithoutBuilding(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	engine.stdout("--name-only", "README.md\ndocs/guide.md\n.claude/settings.json\n")
	pull(t, m)
	settledOn(t, "0", "stood down: inert-only push")
	if engine.chain("dockerBuild") != "" {
		t.Fatal("an inert-only push was built")
	}
}

// A pull builds the image — its build args and labels — and publishes nothing.
func TestAPullBuildsTheImageAndPublishesNothing(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n", ".forgejo/build-args.env": "# the star's args\nFOO=bar\n"})
	engine.stdout("--name-only", "cmd/ares/main.go\n")
	pull(t, m)
	wantCalls(t, engine.chain("dockerBuild", "sync"),
		[]string{"dockerBuild", `"FOO"`, `"bar"`},
		[]string{"withLabel", labelRevision, buildSha},
		[]string{"withLabel", labelTitle, `"ares"`},
	)
	settledOn(t, "0", "clean: built ares at 0123456789ab")
	if engine.chain("publish(") != "" {
		t.Fatal("a pull published")
	}
}

// An image that does not build is a finding about the tree; one that failed on
// the registry or the network is a could-not-run, because running again can
// change it.
func TestAnImageThatDoesNotBuildIsFindingsUnlessTheNetworkFailed(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	engine.stdout("--name-only", "Dockerfile\n")
	engine.fail("dockerBuild", "failed to solve: dockerfile parse error on line 1")
	pull(t, m)
	settledOn(t, "1", "findings in the image build")

	m = buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	engine.stdout("--name-only", "Dockerfile\n")
	engine.fail("dockerBuild", "failed to resolve source metadata for docker.notusmi.com/x: 503 Service Unavailable")
	pull(t, m)
	settledOn(t, "2", "network fault")
}

// A commit with no first parent has nothing to diff against, so it builds.
func TestACommitWithNoFirstParentBuildsToBeSafe(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	engine.exitCode("HEAD^1^{commit}", 1)
	pull(t, m)
	settledOn(t, "0", "clean: built ares")
	if engine.chain("--name-only") != "" {
		t.Fatal("the lane diffed against a parent that does not exist")
	}
}

// A tip that goes through: published under the g-pin as the registry's
// publisher with the runner's index crossing the seam, signed with the CI key,
// its SBOM read by syft and attested, and the permit asked of hades as the
// pod the socket came from.
func TestATipPublishesSignsAttestsAndIsPermitted(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	tip(t, m)
	ref := "registry.notusmi.com/rob/ares@sha256:" + strings.Repeat("d", 64)

	wantCalls(t, engine.chain("publish("),
		[]string{"dockerBuild", `"UV_INDEX_URL"`, `"https://nexus.example/simple"`},
		[]string{"withRegistryAuth", `"registry.notusmi.com"`, `"publisher"`},
		[]string{"publish", `"registry.notusmi.com/rob/ares:g0123456789ab"`},
	)
	wantCalls(t, engine.chain(`"sign","--key"`),
		[]string{"from", checks.ImageCosign},
		[]string{"withMountedSecret", `"/run/cosign/key"`},
		[]string{"withSecretVariable", `"COSIGN_PASSWORD"`},
		[]string{"withExec", `"sign"`, `"--tlog-upload=false"`, ref},
	)
	wantCalls(t, engine.chain(`"registry:registry.notusmi.com`),
		[]string{"from", checks.ImageSyft},
		[]string{"withExec", `"registry:` + ref + `"`, `"cyclonedx-json@1.6"`},
	)
	wantCalls(t, engine.chain(`"attest"`),
		[]string{"withNewFile", `"/in/sbom.cdx.json"`},
		[]string{"withExec", `"attest"`, `"cyclonedx"`, ref},
	)
	wantCalls(t, engine.chain(`"forge_mold"`),
		[]string{"from", checks.ImageStatic},
		[]string{"withUnixSocket", `"/run/spire/agent.sock"`},
		[]string{"withEnvVariable", `"HADESCALL_HADES"`, `"https://hades:8102"`},
		[]string{"withExec", `"/usr/local/bin/hadescall"`, `"forge_mold"`, "ares"},
	)
	settledOn(t, "0", "clean: published and signed "+ref)
}

// A registry that is down at publish is a could-not-run, and nothing is signed.
func TestATipWhosePublishHitsARegistryOutageIsCouldNotRun(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	engine.fail("publish(", "failed to push registry.notusmi.com/rob/ares:g0123456789ab: 503 Service Unavailable")
	tip(t, m)
	settledOn(t, "2", "network fault")
	if engine.chain(`"sign","--key"`) != "" {
		t.Fatal("an image that was never published was signed")
	}
}

// A signature that neither lands nor is already there is a finding, and no
// permit is asked for.
func TestATipThatCannotBeSignedIsFindingsAndAsksNoPermit(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	engine.exitCode(`"sign","--key"`, 1)
	engine.exitCode(`"verify","--key"`, 1)
	tip(t, m)
	settledOn(t, "1", "findings in sign")
	if engine.chain(`"forge_mold"`) != "" {
		t.Fatal("an unsigned image was sent for a permit")
	}
}

// hades's refusal of the permit is the lane's finding; its answer is read the
// way permit.py read it.
func TestATipWhosePermitIsRefusedIsFindings(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	engine.stdout(`"forge_mold"`, "HTTP 200\n"+toolAnswer(true, "no CI artifact at g0123456789ab"))
	tip(t, m)
	settledOn(t, "1", "PERMIT REFUSED")
}

// hadescall that could not ask — no SVID, hades unreachable — is a
// could-not-run, never a finding.
func TestATipThatCannotAskHadesIsCouldNotRun(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	engine.exitCode(`"forge_mold"`, 2)
	engine.stdout(`"forge_mold"`, "")
	engine.stderr(`"forge_mold"`, "hadescall forge_mold: no identity from unix:///run/spire/agent.sock within 2m0s")
	tip(t, m)
	settledOn(t, "2", "could not ask hades")
}

// The star is the repository's last path segment, from a clone URL or a
// custody key.
func TestTheStarIsTheRepositorysName(t *testing.T) {
	for in, want := range map[string]string{
		"http://ourea.default.svc.cluster.local:8215/rob/ares.git": "ares",
		"rob/ares": "ares",
		"ares":     "ares",
	} {
		if got := starOf(in); got != want {
			t.Errorf("starOf(%q) = %q, want %q", in, got, want)
		}
	}
}
