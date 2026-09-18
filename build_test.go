package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/buildlane"
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
	scanClean()
	return &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/rob/ares.git", Sha: buildSha}
}

// scanClean scripts the verify scan every build now runs before it publishes
// (F14) as clean, for the image and for a pinned base alike. A test about the
// scan scripts its own report after this; the last script wins.
func scanClean() {
	engine.script(script{match: imageReportNeedle, leaf: "contents", value: report()})
	engine.script(script{match: baseReportNeedle, leaf: "contents", value: report()})
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
	tipWith(t, m, `{"auths":{"registry.notusmi.com":{"username":"publisher","password":"hunter2"}}}`)
}

func tipWith(t *testing.T, m *FoundryTools, registryAuth string) {
	t.Helper()
	auth := dag.SetSecret("registry-auth", registryAuth)
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

const (
	imageSBOM         = `{"bomFormat":"CycloneDX","components":[{"name":"runtime","version":"1","purl":"pkg:deb/runtime@1"}]}`
	builderSBOM       = `{"components":[{"name":"gomod","version":"1","purl":"pkg:golang/gomod@1"}]}`
	builderDockerfile = "FROM docker.notusmi.com/library/golang:1.26 AS builder\nRUN go build ./...\nFROM scratch\n"
)

// scriptATip scripts every step of a tip that goes through: a source change,
// the public key, the SBOM, its referrer and stored layer, and hades stamping
// the permit.
func scriptATip() {
	engine.stdout("--name-only", "cmd/ares/main.go\n")
	engine.stdout(`"public-key"`, "-----BEGIN PUBLIC KEY-----\nabc\n-----END PUBLIC KEY-----")
	engine.stdout(imageScanNeedle, imageSBOM)
	engine.stdout(`"oci-archive:/in/builder.tar"`, builderSBOM)
	engine.stdout(sbomAttachNeedle, sbomArtifact+"\n")
	engine.stdout(sbomManifestNeedle, `{"schemaVersion":2,"artifactType":"application/vnd.cyclonedx+json","layers":[{"mediaType":"application/vnd.cyclonedx+json","digest":"`+sbomBlob+`","size":4812}]}`)
	engine.stdout(`"forge_mold"`, "HTTP 200\n"+toolAnswer(false, `{"digest":"sha256:eee","pushed_ref":"registry.notusmi.com/rob/ares:stable"}`))
}

var (
	// sbomArtifact is the SBOM referrer's manifest digest oras answers.
	sbomArtifact = "sha256:" + strings.Repeat("a", 64)
	// sbomBlob is the one layer that referrer stores.
	sbomBlob = "sha256:" + strings.Repeat("b", 64)
)

const (
	// imageScanNeedle is in the image's syft scan and in no other: the
	// builder's reads /in/builder.tar.
	imageScanNeedle = `"oci-archive:/in/image.tar"`
	// sbomAttachNeedle is in the SBOM attach's chain and in no other.
	sbomAttachNeedle = `"oras","attach"`
	// sbomManifestNeedle is in the referrer's read-back and in no other.
	sbomManifestNeedle = `"oras","manifest","fetch"`
	// sbomBlobNeedle is in the blob check and in no other.
	sbomBlobNeedle = `"oras","blob","fetch"`
	// pointerNeedle is in the pointer attestation's act and in no other.
	pointerNeedle = `"attest","--key"`
)

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

// The verdict binary is built inside the module with no proxy: it needs
// nothing fetched, so a lane that could not reach anything still settles.
func TestTheVerdictIsBuiltWithNothingFetched(t *testing.T) {
	engine.reset()
	pull(t, &FoundryTools{Source: dag.Directory()})
	wantCalls(t, engine.chain(`"./verdict"`),
		[]string{"withEnvVariable", `"GOPROXY"`, `"off"`},
		[]string{"withExec", `"go"`, `"build"`, `"./verdict"`},
	)
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

// permittedSha is the commit a star's :stable was built from: what the permit
// last delivered, and what a change set is taken since. Built, not written: a
// forty-hex literal reads as a secret to detect-secrets.
var permittedSha = strings.Repeat("fedcba98", 5)

// A commit whose every change since the last permitted build is inert builds
// nothing and settles clean: :stable already carries its source. The change
// set runs from :stable's commit, never from the parent.
func TestACommitInertSinceTheLastPermitStandsDownWithoutBuilding(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	engine.label(":stable", permittedSha)
	engine.stdout("--name-only", "README.md\ndocs/guide.md\n.claude/settings.json\n")
	pull(t, m)
	settledOn(t, "0", "stood down: every change since "+permittedSha[:12]+", the last permitted build (:stable), is inert")
	wantCalls(t, engine.chain("--is-ancestor"), []string{"withExec", `"merge-base"`, `"--is-ancestor"`, `"` + permittedSha + `"`, `"HEAD"`})
	wantCalls(t, engine.chain("--name-only"), []string{"withExec", `"diff"`, `"--name-only"`, `"` + permittedSha + `"`, `"HEAD"`})
	if engine.chain("dockerBuild") != "" {
		t.Fatal("a commit inert since the last permit was built")
	}
	if engine.chain("HEAD^1") != "" {
		t.Fatal("the lane took its change set from the parent")
	}
}

// A commit inert against its parent still builds when the parent's build never
// reached a permit, because the change set since :stable's commit carries the
// source that build did not deliver. Measured on athena 2026-09-14: a446514
// changed source and failed at sign; 570be49, quickstart.md alone on top of
// it, diffed inert against its parent and stood down.
func TestACommitInertAgainstAnUnpermittedParentStillBuilds(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	engine.label(":stable", permittedSha)
	engine.stdout("--name-only", "cmd/ares/main.go\ndocs/quickstart.md\n")
	pull(t, m)
	settledOn(t, "0", "clean: built ares")
	if engine.chain("dockerBuild") == "" {
		t.Fatal("source the last permit never delivered was not built")
	}
}

// With no permitted build to compare against the lane builds, and reads no
// history: no :stable, a :stable that does not read, or one whose image names
// no commit.
func TestWithNoPermittedBuildToCompareAgainstTheLaneBuilds(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	// The needle is the address, not ":stable": a stood-down verdict's reason
	// names :stable too, and would fail with it.
	engine.fail(`rob/ares:stable"`, "failed to resolve source metadata for registry.notusmi.com/rob/ares:stable: not found")
	engine.stdout("--name-only", "README.md\n")
	pull(t, m)
	settledOn(t, "0", "clean: built ares")
	if engine.chain("--is-ancestor") != "" || engine.chain("--name-only") != "" {
		t.Fatal("the lane read the history against a :stable it could not read")
	}

	for _, revision := range []string{"", "v1.4.0", permittedSha[:12]} {
		m = buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
		engine.label(":stable", revision)
		engine.stdout("--name-only", "README.md\n")
		pull(t, m)
		settledOn(t, "0", "clean: built ares")
		if engine.chain("--is-ancestor") != "" {
			t.Fatalf("the lane compared against a :stable whose revision is %q", revision)
		}
	}
}

// A permitted build outside this commit's history says nothing about its tree,
// so the lane builds without diffing: a permit this checkout does not carry at
// all (a branch older than it — rev-parse --verify exits 1) and one it carries
// that is not an ancestor (merge-base exits 1) alike.
//
// THE ABSENT COMMIT IS ASKED WITH rev-parse, NOT merge-base. This test used to
// script `--is-ancestor` exiting 128 and pass, because the paper engine answers
// an exit code literally. The cluster engine does not: Expect ANY covers exit
// codes 0-127 and 192-255 only, so merge-base on a commit it cannot name exits
// 128 and surfaces as an engine error — five real pulls settled could-not-run
// on 2026-09-15.
func TestAPermitOutsideThisHistoryBuildsWithoutDiffing(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	engine.label(":stable", permittedSha)
	engine.exitCode(`"`+permittedSha+`^{commit}"`, 1)
	engine.stdout("--name-only", "README.md\n")
	pull(t, m)
	settledOn(t, "0", "clean: built ares")
	wantCalls(t, engine.chain(`"`+permittedSha+`^{commit}"`), []string{"withExec", `"rev-parse"`, `"--verify"`, `"--quiet"`, `"` + permittedSha + `^{commit}"`})
	if engine.chain("--is-ancestor") != "" || engine.chain("--name-only") != "" {
		t.Fatal("the lane asked about ancestry or diffed against a permit this checkout does not carry")
	}

	m = buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	engine.label(":stable", permittedSha)
	engine.exitCode("--is-ancestor", 1)
	engine.stdout("--name-only", "README.md\n")
	pull(t, m)
	settledOn(t, "0", "clean: built ares")
	if engine.chain("--name-only") != "" {
		t.Fatal("the lane diffed against a permit that is not an ancestor")
	}
}

// A history or change set the engine cannot read is neither a reason to build
// nor one to stand down.
func TestAHistoryOrChangeSetThatCannotBeReadIsCouldNotRun(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	engine.label(":stable", permittedSha)
	engine.fail(`"`+permittedSha+`^{commit}"`, "the engine went away")
	pull(t, m)
	settledOn(t, "2", "the history could not be read")
	if engine.chain("--is-ancestor") != "" {
		t.Fatal("the lane asked about ancestry after the history could not be read")
	}

	m = buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	engine.label(":stable", permittedSha)
	engine.fail("--is-ancestor", "the engine went away")
	pull(t, m)
	settledOn(t, "2", "the history could not be read")

	m = buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	engine.label(":stable", permittedSha)
	engine.exitCode("--name-only", 128)
	pull(t, m)
	settledOn(t, "2", "the change set could not be read")
	if engine.chain("dockerBuild") != "" {
		t.Fatal("a commit whose change set could not be read was built")
	}
}

// The permit's output is read where the image is pushed: the compose file's
// declared image on the lane's registry, not the star's name.
func TestThePermittedRevisionIsReadWhereTheImageIsPushed(t *testing.T) {
	m := buildOn(t, map[string]string{
		"Dockerfile":   "FROM scratch\n",
		"compose.yaml": "services:\n  web:\n    image: registry.notusmi.com/rob/ares-web:latest\n",
	})
	engine.label(":stable", permittedSha)
	engine.stdout("--name-only", "README.md\n")
	pull(t, m)
	wantCalls(t, engine.chain("label("),
		[]string{"from", `"registry.notusmi.com/rob/ares-web:stable"`},
		[]string{"label", `"org.opencontainers.image.revision"`},
	)
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

// NOTHING IS PUBLISHED THAT THE SCAN DID NOT PASS (F14). A fixable HIGH in
// the star's own layer settles the tip as findings before the push: no
// publish, no signature, no permit — the registry never holds the image.
func TestATipWithAFixableFindingInItsOwnLayerPublishesNothing(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	engine.script(script{match: imageReportNeedle, leaf: "contents", value: report(grpcFinding)})
	tip(t, m)
	settledOn(t, "1", "verify: findings in the scan: 1 fixable HIGH or CRITICAL vulnerabilities in this image's own layer")
	settledOn(t, "1", "google.golang.org/grpc")
	settledOn(t, "1", "nothing published")
	for _, step := range []string{"publish(", `"sign","--key"`, `"forge_mold"`} {
		if engine.chain(step) != "" {
			t.Errorf("an image with a fixable finding reached %s", step)
		}
	}
	// The scan ran on the tarball of the image the engine built, with the
	// verify stage's flags.
	wantCalls(t, engine.chain(imageReportNeedle),
		[]string{"from", checks.ImageTrivy},
		[]string{"withExec", `"image"`, `"--severity"`, `"HIGH,CRITICAL"`, `"--ignore-unfixed"`, `"--exit-code"`, `"0"`},
	)
}

// The same finding on a pull is the pull's to see: findings, and the log
// names the package.
func TestAPullWithAFixableFindingIsFindings(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	engine.stdout("--name-only", "cmd/ares/main.go\n")
	engine.script(script{match: imageReportNeedle, leaf: "contents", value: report(grpcFinding)})
	pull(t, m)
	settledOn(t, "1", "verify: findings in the scan")
	settledOn(t, "1", "google.golang.org/grpc")
}

// A finding the base carries is the base lane's: named in the log, not
// counted, and the tip publishes.
func TestATipWhoseOnlyFindingsAreItsBasesPublishes(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": pinnedDockerfile})
	scriptATip()
	engine.stdout(`"oci-archive:/in/builder.tar"`, builderSBOM)
	scriptTheBase()
	engine.script(script{match: imageReportNeedle, leaf: "contents", value: report(perlFinding)})
	engine.script(script{match: baseReportNeedle, leaf: "contents", value: report(perlFinding)})
	tip(t, m)
	if engine.chain("publish(") == "" {
		t.Fatal("a tip whose only findings are its base's did not publish")
	}
	settledOn(t, "0", "clean: published and signed")
}

// A scan that did not run is not a pass: could-not-run, and nothing is
// published — the door asks again.
func TestATipWhoseScanCannotRunPublishesNothing(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	engine.failLeaf(imageReportNeedle, "contents", "no such file or directory: /scan/image.json")
	tip(t, m)
	settledOn(t, "2", "verify: could not run: trivy wrote no report for the image")
	if engine.chain("publish(") != "" {
		t.Fatal("an image whose scan did not run was published")
	}
}

// A Dockerfile that does not build is still classified as a build — findings,
// or could-not-run on a network fault — on the tip path too, never as a scan
// that could not run.
func TestATipWhoseImageDoesNotBuildIsClassifiedAsABuild(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	engine.fail("dockerBuild", "failed to solve: dockerfile parse error on line 1")
	tip(t, m)
	settledOn(t, "1", "findings in the image build")
	if engine.chain(imageReportNeedle) != "" || engine.chain("publish(") != "" {
		t.Fatal("an image that did not build was scanned or published")
	}
}

// A push touching many sources names the first eight and counts them all.
func TestAWidePushNamesItsFirstEightChanges(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	var files []string
	for _, f := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		files = append(files, "cmd/"+f+".go")
	}
	engine.label(":stable", permittedSha)
	engine.stdout("--name-only", strings.Join(files, "\n"))
	pull(t, m)
	settledOn(t, "0", "clean: built ares")
	if chain := engine.chain("--name-only"); chain == "" {
		t.Fatal("the change set was never read")
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

// A tip that goes through: published under the g-pin as the registry's
// publisher with the runner's index crossing the seam, signed with the CI key,
// its SBOM read by syft and attested, the signature verified, and the permit
// asked of hades as the pod the socket came from.
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
	// The image is scanned as the tarball the engine built — the same chain
	// the publish pushed — with syft told to write no file entries.
	wantCalls(t, engine.chain(imageScanNeedle),
		[]string{"from", checks.ImageSyft},
		[]string{"withEnvVariable", `"SYFT_FILE_METADATA_SELECTION"`, `"none"`},
		[]string{"withExec", imageScanNeedle, `"cyclonedx-json@1.6"`},
	)
	// The SBOM rides as a referrer, and what is signed is a pointer to it: the
	// referrer's manifest and the blob the registry stored, read back.
	wantCalls(t, engine.chain(sbomAttachNeedle),
		[]string{"from", checks.ImageFleet},
		[]string{"withMountedSecret", `"/run/docker/config.json"`},
		[]string{"withNewFile", `"/in/sbom.cdx.json"`, "pkg:deb/runtime@1"},
		[]string{"withExec", `"--registry-config"`, `"--artifact-type"`, `"application/vnd.cyclonedx+json"`, `"org.opencontainers.image.created=`, `"{{.digest}}"`, ref, `"sbom.cdx.json:application/vnd.cyclonedx+json"`},
	)
	wantCalls(t, engine.chain(sbomManifestNeedle), []string{"withExec", `"--registry-config"`, `"registry.notusmi.com/rob/ares@` + sbomArtifact + `"`})
	wantCalls(t, engine.chain(pointerNeedle),
		[]string{"withNewFile", `"/in/sbom-ref.json"`, sbomArtifact, sbomBlob, "4812", "CycloneDX"},
		[]string{"withExec", `"attest"`, `"https://notusmi.com/attestation/sbom-ref/v1"`, `"/in/sbom-ref.json"`, ref},
	)
	if strings.Contains(engine.chain(pointerNeedle), "pkg:deb/runtime@1") {
		t.Error("the SBOM itself was attested; only the pointer to it may be")
	}
	wantCalls(t, engine.chain(`"verify-attestation"`), []string{"withExec", `"https://notusmi.com/attestation/sbom-ref/v1"`, `"--insecure-ignore-tlog=true"`, ref})
	wantCalls(t, engine.chain(sbomBlobNeedle), []string{"withExec", `"--registry-config"`, `"--descriptor"`, `"registry.notusmi.com/rob/ares@` + sbomBlob + `"`})
	// Unless told not to, cosign 3's sign and attest fetch sigstore's signing
	// config from its CDN and upload to Rekor. Nothing in the fleet reads Rekor,
	// and the fetch put an outside CDN in the build path (build-ourea-ms24d),
	// so both calls carry both flags. The guard is keyed on the pin: v2.5.3 had
	// no --use-signing-config and failed on it (athena a446514).
	if strings.Contains(checks.ImageCosign, "/cosign:v3.") {
		for _, chain := range []string{engine.chain(`"sign","--key"`), engine.chain(`"attest"`)} {
			for _, flag := range []string{`"--use-signing-config=false"`, `"--tlog-upload=false"`} {
				if !strings.Contains(chain, flag) {
					t.Errorf("%s reaches sigstore's CDN and Rekor unless told not to, and this call lacks %s:\n%s", checks.ImageCosign, flag, chain)
				}
			}
		}
	}
	if engine.chain(`"verify","--key","/run/cosign/key.pub"`) == "" {
		t.Error("the signature was never verified")
	}
	wantCalls(t, engine.chain(`"forge_mold"`),
		[]string{"from", checks.ImageStatic},
		// Owned by the static base's nonroot user: forwarded root-owned, the
		// socket refused hadescall's connect and the permit waited out two
		// minutes for an identity (athena b66d46f).
		[]string{"withUnixSocket", `"/run/spire/agent.sock"`, `owner:"65532:65532"`},
		[]string{"withEnvVariable", `"HADESCALL_HADES"`, `"https://hades:8102"`},
		[]string{"withExec", `"/usr/local/bin/hadescall"`, `"forge_mold"`, "ares"},
	)
	// hadescall is built with the Go lane's caches, the build cache by its
	// variable.
	wantCalls(t, engine.chain(`"./hadescall"`),
		[]string{"withEnvVariable", `"GOCACHE"`, `"/opt/go-build-cache"`},
		[]string{"withMountedCache", `path:"/go/pkg/mod"`},
	)
	settledOn(t, "0", "clean: published and signed "+ref)
}

// A registry credential with no entry for the push registry publishes
// nothing.
func TestATipWhoseCredentialNamesAnotherRegistryIsCouldNotRun(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	tipWith(t, m, `{"auths":{"other.host":{"username":"publisher","password":"hunter2"}}}`)
	settledOn(t, "2", "names no entry for registry.notusmi.com")
	if engine.chain("publish(") != "" {
		t.Fatal("an image was published without a credential for its registry")
	}
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

// A signature that neither lands nor is already there is a finding: nothing is
// attested and no permit is asked for.
func TestATipThatCannotBeSignedIsFindingsAndAsksNoPermit(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	engine.exitCode(`"sign","--key"`, 1)
	engine.exitCode(`"verify","--key"`, 1)
	tip(t, m)
	settledOn(t, "1", "findings in sign —")
	if engine.chain(sbomAttachNeedle) != "" || engine.chain(pointerNeedle) != "" || engine.chain(`"forge_mold"`) != "" {
		t.Fatal("an unsigned image went on to be attested or permitted")
	}
}

// A sign that refuses the lane's own arguments never looked at the image, so
// the lane cannot run; it is not a finding. Measured 2026-09-14 on athena
// a446514: cosign v2.5.3 answered "unknown flag: --use-signing-config", the
// verify fallback found nothing, and the lane settled "findings in sign".
func TestASignThatRefusesTheLanesArgumentsIsCouldNotRun(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	refusal := "Error: unknown flag: --use-signing-config\nerror during command execution: unknown flag: --use-signing-config\n"
	engine.exitCode(`"sign","--key"`, 1)
	engine.stdout(`"sign","--key"`, refusal)
	engine.stderr(`"sign","--key"`, refusal)
	engine.exitCode(`"verify","--key"`, 10)
	engine.stderr(`"verify","--key"`, "Error: no signatures found\n")
	tip(t, m)
	settledOn(t, "2", "sign refused the lane's own arguments (Error: unknown flag: --use-signing-config)")
	if engine.chain(sbomAttachNeedle) != "" || engine.chain(pointerNeedle) != "" || engine.chain(`"forge_mold"`) != "" {
		t.Fatal("a sign that never ran went on to be attested or permitted")
	}
}

// A signature that is already there verifies in place of a failed sign, and
// the lane goes on.
func TestAnImageAlreadySignedVerifiesInsteadOfFailing(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	engine.exitCode(`"sign","--key"`, 1)
	tip(t, m)
	settledOn(t, "0", "clean: published and signed")
}

// A check that could not run is not a pass.
func TestASignWhoseCheckCannotRunIsNotAPass(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	engine.exitCode(`"sign","--key"`, 1)
	engine.failLeaf(`"verify","--key"`, "exitCode", "the engine went away")
	tip(t, m)
	settledOn(t, "1", "findings in sign —")
	if engine.chain(sbomAttachNeedle) != "" || engine.chain(pointerNeedle) != "" {
		t.Fatal("a sign nobody could check went on to be attested")
	}
}

// A pointer already on the image verifies in place of a failed attestation, and
// the lane goes on.
func TestAnSBOMPointerAlreadyThereVerifiesInsteadOfFailing(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	engine.exitCode(pointerNeedle, 1)
	tip(t, m)
	settledOn(t, "0", "clean: published and signed")
}

// Every half of the SBOM's publish that fails withholds the permit: the reader
// that follows the pointer is owed a good one on every image this lane
// publishes (Zuse7's writer contract). Only a tool that answered about the
// image is a finding; every other failure is could-not-run.
func TestAnSBOMThatDoesNotPublishWithholdsThePermit(t *testing.T) {
	for _, c := range []struct {
		name, code, reason string
		script             func()
		pointed            bool
	}{
		{"oras cannot be provisioned", "2", "oras could not be provisioned", func() {
			// The fetch alone: the settle's reason names both URLs, and a bare
			// URL needle would fail the verdict exec too.
			engine.fail(`http(url:"`+checks.OrasMirror+`")`, "502 from the mirror")
			engine.fail(`http(url:"`+checks.OrasURL+`")`, "502 from upstream")
		}, false},
		{"the attach is refused", "1", "findings in SBOM attach", func() {
			engine.exitCode(sbomAttachNeedle, 1)
			engine.stdout(sbomAttachNeedle, "Error: failed to push: denied\n")
		}, false},
		{"the attach cannot run", "2", "the SBOM attach did not run", func() {
			engine.failLeaf(sbomAttachNeedle, "exitCode", "the engine went away")
		}, false},
		{"the attach answers no digest", "2", "oras attached the SBOM and answered no digest", func() {
			engine.stdout(sbomAttachNeedle, "Attached to [registry] registry.notusmi.com/rob/ares\n")
		}, false},
		{"the referrer does not read back", "2", "could not be read back (exit 1)", func() {
			engine.exitCode(sbomManifestNeedle, 1)
		}, false},
		{"the referrer read-back cannot run", "2", "could not be read back", func() {
			engine.failLeaf(sbomManifestNeedle, "exitCode", "the engine went away")
		}, false},
		{"the referrer is not one SBOM layer", "2", "carries 0 layers, not one", func() {
			engine.stdout(sbomManifestNeedle, `{"schemaVersion":2,"layers":[]}`)
		}, false},
		{"the pointer neither lands nor verifies", "1", "findings in sign (SBOM pointer)", func() {
			engine.exitCode(pointerNeedle, 1)
			engine.exitCode(`"verify-attestation"`, 1)
		}, true},
		{"the pointer lands and does not verify", "2", "the SBOM pointer was attested and does not verify against the CI key", func() {
			engine.exitCode(`"verify-attestation"`, 1)
		}, true},
		{"the pointer attestation cannot run", "2", "the SBOM pointer attestation did not run", func() {
			engine.failLeaf(pointerNeedle, "exitCode", "the engine went away")
		}, true},
		{"the pointer check cannot run", "2", "the SBOM pointer check did not run", func() {
			engine.failLeaf(`"verify-attestation"`, "exitCode", "the engine went away")
		}, true},
		{"the blob the pointer names is not readable", "2", "is not readable from the registry (exit 1)", func() {
			engine.exitCode(sbomBlobNeedle, 1)
		}, true},
		{"the blob check cannot run", "2", "is not readable from the registry", func() {
			engine.failLeaf(sbomBlobNeedle, "exitCode", "the engine went away")
		}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
			scriptATip()
			c.script()
			tip(t, m)
			settledOn(t, c.code, c.reason)
			if engine.chain(`"forge_mold"`) != "" {
				t.Fatal("an image whose SBOM did not publish was sent for a permit")
			}
			if pointed := engine.chain(pointerNeedle) != ""; pointed != c.pointed {
				t.Fatalf("the pointer attestation ran: %v, want %v", pointed, c.pointed)
			}
		})
	}
}

// A signature that does not verify after signing and attesting is a finding,
// and no permit is asked for.
func TestATipWhoseSignatureDoesNotVerifyIsFindings(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	engine.exitCode(`"verify","--key"`, 1)
	tip(t, m)
	settledOn(t, "1", "findings in sign (verify)")
	if engine.chain(`"forge_mold"`) != "" {
		t.Fatal("an unverified image was sent for a permit")
	}
}

// A Dockerfile with a builder stage has that stage's dependencies folded into
// the SBOM that is attached.
func TestABuilderStagesDependenciesAreAttachedWithTheImage(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": builderDockerfile})
	scriptATip()
	tip(t, m)
	if engine.chain(`target:"builder"`) == "" || engine.chain(`"oci-archive:/in/builder.tar"`) == "" {
		t.Fatal("the builder stage was not built and read")
	}
	wantCalls(t, engine.chain(sbomAttachNeedle), []string{"withNewFile", `"/in/sbom.cdx.json"`, "pkg:golang/gomod@1", "pkg:deb/runtime@1"})
	settledOn(t, "0", "clean: published and signed")
}

// attachesTheImageAlone asserts the attached SBOM is the runtime image's and
// nothing of the builder's.
func attachesTheImageAlone(t *testing.T) {
	t.Helper()
	attach := engine.chain(sbomAttachNeedle)
	wantCalls(t, attach, []string{"withNewFile", `"/in/sbom.cdx.json"`, "pkg:deb/runtime@1"})
	if strings.Contains(attach, "pkg:golang/gomod@1") {
		t.Fatalf("the builder's components were attached:\n%s", attach)
	}
	settledOn(t, "0", "clean: published and signed")
}

// A builder stage that does not build leaves the runtime image's SBOM.
func TestABuilderStageThatDoesNotBuildLeavesTheImagesSBOM(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": builderDockerfile})
	scriptATip()
	engine.failLeaf(`target:"builder"`, "size", "failed to solve: the builder stage")
	tip(t, m)
	if engine.chain(`"oci-archive:/in/builder.tar"`) != "" {
		t.Fatal("an unbuilt builder stage was read")
	}
	attachesTheImageAlone(t)
}

// A builder SBOM syft cannot read leaves the runtime image's SBOM.
func TestABuilderSBOMThatDoesNotReadLeavesTheImagesSBOM(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": builderDockerfile})
	scriptATip()
	engine.exitCode(`"oci-archive:/in/builder.tar"`, 1)
	tip(t, m)
	attachesTheImageAlone(t)
}

// Builder SBOMs that do not merge leave the runtime image's SBOM.
func TestABuilderSBOMThatDoesNotMergeLeavesTheImagesSBOM(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": builderDockerfile})
	scriptATip()
	engine.stdout(`"oci-archive:/in/builder.tar"`, "not an sbom")
	tip(t, m)
	attachesTheImageAlone(t)
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

// An answer that is not hadescall's shape is a could-not-run.
func TestAPermitAnswerWithNoStatusLineIsCouldNotRun(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	engine.stdout(`"forge_mold"`, "garbage")
	tip(t, m)
	settledOn(t, "2", "no status line")
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

// THE ATTACHED SBOM IS THE COMPOSED ONE (F14's contract, lines 1–4): the
// star's own components, the base's document linked in place of its copied
// inventory, and the image labelled with the base it was built on.
func TestATipAttachesTheComposedSBOMAndLabelsTheBase(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": pinnedDockerfile})
	scriptATip()
	engine.stdout(`"oci-archive:/in/builder.tar"`, builderSBOM)
	scriptTheBase()
	tip(t, m)

	attach := engine.chain(sbomAttachNeedle)
	// The builder's gomod is the star's own and stays; the runtime package the
	// base also carries goes, and the link and the declaration take its place.
	wantCalls(t, attach,
		[]string{"withNewFile", `"/in/sbom.cdx.json"`, "pkg:golang/gomod@1", `\"type\":\"bom\"`, "oci://" + pinnedBaseRepo + "@" + baseBlob, buildlane.SBOMAggregateFirstParty},
	)
	if strings.Contains(attach, "pkg:deb/runtime@1") {
		t.Fatalf("the base's component was attached with the star's:\n%s", attach)
	}
	// The base's document was read from ITS repository, by the digest the
	// referrer's manifest named, anonymously: a read needs no login.
	if engine.chain(`"oras","blob","fetch","--output","-","`+pinnedBaseRepo+`@`+baseBlob) == "" {
		t.Fatal("the base's SBOM was not read from the registry")
	}
	if strings.Contains(engine.chain(discoverNeedle), "--registry-config") {
		t.Error("an anonymous read carried the registry login")
	}
	// The image carries its base as the two OCI labels — provenance, which
	// nothing compares against a record any more (image.go: withBaseLabels).
	wantCalls(t, engine.chain("publish("),
		[]string{"withLabel", labelBaseName, `"registry.notusmi.com/rob/stellar_core:python-runtime"`},
		[]string{"withLabel", labelBaseDigest, `"` + pinnedBaseDigest + `"`},
	)
	settledOn(t, "0", "clean: published and signed")
}

// A base whose registry read fails withholds the publish's SBOM as
// could-not-run — a registry outage is not a fault of the tree.
func TestATipWhoseBaseCannotBeReadIsCouldNotRun(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": pinnedDockerfile})
	scriptATip()
	engine.stdout(`"oci-archive:/in/builder.tar"`, builderSBOM)
	engine.exitCode(discoverNeedle, 1)
	engine.stdout(discoverNeedle, "Error: response status code 502: Bad Gateway")
	tip(t, m)
	settledOn(t, "2", "could not run: the base registry.notusmi.com/rob/stellar_core@"+pinnedBaseDigest+"'s referrers could not be listed")
	if engine.chain(`"forge_mold"`) != "" {
		t.Fatal("a tip whose SBOM could not be composed asked for a permit")
	}
}

// An image built FROM scratch has no base to label and nothing to link.
func TestAnUnpinnedBaseLeavesTheLabelsOff(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	tip(t, m)
	if p := engine.chain("publish("); strings.Contains(p, labelBaseDigest) || strings.Contains(p, labelBaseName) {
		t.Fatalf("a base label was written for FROM scratch:\n%s", p)
	}
	if engine.chain(discoverNeedle) != "" {
		t.Fatal("scratch was looked up in the registry")
	}
	settledOn(t, "0", "clean: published and signed")
}

// THE IMAGE IS THE BASE PLUS THE ARTIFACT (F14/F17). A Dockerfile that copies
// from release/ gets the Gate's release build staged there — the go:release
// atom's own exec, so the engine answers it from cache — and the image
// compiles nothing itself.
const releaseDockerfile = "FROM registry.notusmi.com/foundry/base-images/go:stable@sha256:" + pinnedBaseDigest + "\nCOPY release/ares /ares\nCMD [\"/ares\"]\n"

func releaseTree() map[string]string {
	return map[string]string{
		"Dockerfile":          releaseDockerfile,
		".copier-answers.yml": "service_name: ares\n",
		"go.mod":              "module ares\n",
		"cmd/ares/main.go":    "package main\nfunc main() {}\n",
	}
}

func TestADockerfileThatCopiesFromReleaseGetsTheGatesArtifact(t *testing.T) {
	m := buildOn(t, releaseTree())
	engine.stdout("--name-only", "cmd/ares/main.go\n")
	pull(t, m)
	// The release build ran — the same argv the go:release atom runs — and
	// its /out is the build context's release/.
	release := engine.chain(`"-o","/out/ares"`)
	if release == "" {
		t.Fatalf("the release build never ran:\n%v", engine.chains())
	}
	wantCalls(t, release, []string{"withExec", `"go","build"`, `"-trimpath"`, `"-o","/out/ares"`, `"./cmd/ares"`})
	wantCalls(t, engine.chain("dockerBuild", "sync"),
		[]string{"withDirectory", `"release"`},
		[]string{"withLabel", labelBaseName, `"registry.notusmi.com/foundry/base-images/go:stable"`},
	)
	settledOn(t, "0", "clean: built ares at 0123456789ab")
}

// A Dockerfile that still carries its own build stage asks for nothing: no
// release build runs, and the context is the tree as fetched — which is what
// lets the fleet flip one star at a time.
func TestADockerfileThatCompilesItselfGetsNoRelease(t *testing.T) {
	tree := releaseTree()
	tree["Dockerfile"] = "FROM docker.notusmi.com/library/golang:1.27 AS build\nRUN go build -o /out/ares ./cmd/ares\nFROM scratch\nCOPY --from=build /out/ares /ares\n"
	m := buildOn(t, tree)
	engine.stdout("--name-only", "cmd/ares/main.go\n")
	pull(t, m)
	if engine.chain(`"-o","/out/ares"`) != "" {
		t.Fatal("a Dockerfile with its own build stage got a release build")
	}
	if strings.Contains(engine.chain("dockerBuild", "sync"), `"release"`) {
		t.Fatal("release/ was staged into a context nothing copies it from")
	}
	settledOn(t, "0", "clean: built ares at 0123456789ab")
}

// A release that does not compile is a build that failed: findings, in the
// compiler's words, and nothing is built, scanned or published on top of it.
func TestATipWhoseReleaseDoesNotBuildIsFindingsAndPublishesNothing(t *testing.T) {
	m := buildOn(t, releaseTree())
	scriptATip()
	engine.exitCode(`"go","build","-trimpath"`, 1)
	engine.stderr(`"go","build","-trimpath"`, "cmd/ares/main.go:2: undefined: x")
	tip(t, m)
	settledOn(t, "1", "findings in the release build")
	if engine.chain("dockerBuild", "sync") != "" || engine.chain(imageReportNeedle) != "" || engine.chain("publish(") != "" {
		t.Fatal("an image whose release did not build was built, scanned or published")
	}
}

// A release the engine could not run is a fault, not a finding.
func TestATipWhoseReleaseHitsAFaultIsCouldNotRun(t *testing.T) {
	m := buildOn(t, releaseTree())
	scriptATip()
	engine.fail(`"go","build","-trimpath"`, "dial tcp: i/o timeout")
	tip(t, m)
	settledOn(t, "2", "could not run: the release build failed on a network fault")
	if engine.chain("publish(") != "" {
		t.Fatal("a fault published")
	}
}

// A Dockerfile that copies from release/ in a repository that names no star
// is asking for a binary nothing can name: findings on the repository.
func TestADockerfileThatCopiesFromReleaseInARepoThatNamesNoStarIsFindings(t *testing.T) {
	tree := releaseTree()
	delete(tree, ".copier-answers.yml")
	m := buildOn(t, tree)
	engine.stdout("--name-only", "cmd/ares/main.go\n")
	pull(t, m)
	settledOn(t, "1", "findings in the release build")
}
