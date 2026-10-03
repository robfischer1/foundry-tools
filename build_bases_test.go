package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// baseTree is a repository of base images: no root Dockerfile, two bases and
// the source every base builds its entrypoint from.
func baseTree() map[string]string {
	return map[string]string{
		"bases/go/Dockerfile":   "FROM docker.notusmi.com/library/golang:1.27 AS boot\nFROM scratch\n",
		"bases/rust/Dockerfile": "FROM docker.notusmi.com/library/golang:1.27 AS boot\nFROM scratch\n",
		"bases/README.md":       "not a base\n",
		"stellar-boot/main.go":  "package main\n",
		"README.md":             "# base-images\n",
	}
}

// basesOn is the module on a base-image repository the engine fetched.
func basesOn(t *testing.T, tree map[string]string) *FoundryTools {
	t.Helper()
	m := buildOn(t, tree)
	m.Repo = "http://door:8215/foundry/base-images.git"
	return m
}

const cleanReport = `{"Results":[{"Target":"img","Vulnerabilities":null}]}`

// A pull builds every base from its own Dockerfile against the whole tree,
// scans each with trivy through the mirror, publishes nothing, and settles
// clean when both pass. bases/README.md is not a base.
func TestAPullBuildsAndScansEveryBaseAndPublishesNothing(t *testing.T) {
	m := basesOn(t, baseTree())
	engine.stdout("--name-only", "stellar-boot/main.go\n")
	engine.script(script{match: trivyReport, leaf: "contents", value: cleanReport})
	pull(t, m)
	for _, b := range []string{"go", "rust"} {
		wantCalls(t, engine.chain(`dockerfile:"bases/`+b+`/Dockerfile"`, "sync"),
			[]string{"dockerBuild", `dockerfile:"bases/` + b + `/Dockerfile"`},
			[]string{"withLabel", labelTitle, `"base-images/` + b + `"`},
		)
	}
	if engine.chain(`dockerfile:"bases/README.md`) != "" {
		t.Fatal("a file beside the bases was built as one")
	}
	wantCalls(t, engine.chain(`"--input","/scan/image.tar"`),
		[]string{"from", checks.ImageTrivy},
		[]string{"withEnvVariable", `"TRIVY_DB_REPOSITORY"`, checks.TrivyDBRepo},
		[]string{"withEnvVariable", `"TRIVY_JAVA_DB_REPOSITORY"`, checks.TrivyJavaDBRepo},
		[]string{"withMountedCache", `path:"/cache"`},
		[]string{"withMountedFile", `"/scan/image.tar"`},
		[]string{"withExec", `"image"`, `"--severity"`, `"HIGH,CRITICAL"`, `"--ignore-unfixed"`, `"--exit-code"`, `"0"`, `"--format"`, `"json"`},
	)
	settledOn(t, "0", "go: clean: built and scanned go at 0123456789ab")
	settledOn(t, "0", "rust: clean: built and scanned rust")
	if engine.chain("publish(") != "" {
		t.Fatal("a pull published a base")
	}
}

// A fixable HIGH or CRITICAL vulnerability is a finding, named, and nothing
// after the scan runs for that base.
func TestAScanFindingIsAFindingAndStopsThatBase(t *testing.T) {
	m := basesOn(t, map[string]string{"bases/go/Dockerfile": "FROM scratch\n"})
	engine.stdout("--name-only", "bases/go/Dockerfile\n")
	engine.script(script{match: trivyReport, leaf: "contents", value: `{"Results":[{"Target":"img","Vulnerabilities":[{"VulnerabilityID":"CVE-9","PkgName":"zlib","InstalledVersion":"1","FixedVersion":"2","Severity":"CRITICAL"}]}]}`})
	scriptABaseTip()
	tip(t, m)
	settledOn(t, "1", "findings in the scan: 1 fixable HIGH or CRITICAL vulnerabilities")
	settledOn(t, "1", "CRITICAL CVE-9 zlib 1 -> 2")
	if engine.chain("publish(") != "" {
		t.Fatal("a base with a finding was published")
	}
}

// A scan that could not scan is could-not-run, never a pass: trivy exiting
// non-zero, and a report that is not JSON.
func TestAScanThatCannotScanIsCouldNotRun(t *testing.T) {
	m := basesOn(t, map[string]string{"bases/go/Dockerfile": "FROM scratch\n"})
	engine.stdout("--name-only", "bases/go/Dockerfile\n")
	engine.exitCode(`"--input","/scan/image.tar"`, 1)
	engine.stdout(`"--input","/scan/image.tar"`, "FATAL failed to download vulnerability DB")
	pull(t, m)
	settledOn(t, "2", "trivy exited 1: FATAL failed to download vulnerability DB")

	m = basesOn(t, map[string]string{"bases/go/Dockerfile": "FROM scratch\n"})
	engine.stdout("--name-only", "bases/go/Dockerfile\n")
	engine.script(script{match: trivyReport, leaf: "contents", value: "not json"})
	pull(t, m)
	settledOn(t, "2", "trivy's report is not JSON")
}

// A base that does not build is a finding about the tree, like a star's.
func TestABaseThatDoesNotBuildIsFindings(t *testing.T) {
	m := basesOn(t, map[string]string{"bases/go/Dockerfile": "FROM scratch\n"})
	engine.stdout("--name-only", "bases/go/Dockerfile\n")
	engine.fail(`dockerfile:"bases/go/Dockerfile"`, "failed to solve: dockerfile parse error on line 1")
	pull(t, m)
	settledOn(t, "1", "findings in the image build")
}

// Each base stands or builds on its own change set since its own :stable: a
// change under bases/go/ builds go and stands rust down; the shared source
// builds both.
func TestEachBaseStandsOnItsOwnChangesSinceItsStable(t *testing.T) {
	m := basesOn(t, baseTree())
	engine.label("foundry/base-images/go:stable", permittedSha)
	engine.label("foundry/base-images/rust:stable", permittedSha)
	engine.stdout("--name-only", "bases/go/Dockerfile\nREADME.md\n")
	engine.script(script{match: trivyReport, leaf: "contents", value: cleanReport})
	pull(t, m)
	wantCalls(t, engine.chain("foundry/base-images/rust:stable"), []string{"from", "registry.notusmi.com/foundry/base-images/rust:stable"})
	settledOn(t, "0", "go: clean: built and scanned go")
	settledOn(t, "0", "rust: stood down: every change since fedcba98fedc")
	if engine.chain(`dockerfile:"bases/rust/Dockerfile"`) != "" {
		t.Fatal("rust was built for a change that was go's alone")
	}
}

// scriptABaseTip scripts every step a base's tip goes through after its scan.
func scriptABaseTip() {
	engine.stdout(`"public-key"`, "-----BEGIN PUBLIC KEY-----\nabc\n-----END PUBLIC KEY-----")
	engine.stdout(imageScanNeedle, imageSBOM)
	engine.stdout(sbomAttachNeedle, sbomArtifact+"\n")
	engine.stdout(sbomManifestNeedle, `{"schemaVersion":2,"artifactType":"application/vnd.cyclonedx+json","layers":[{"mediaType":"application/vnd.cyclonedx+json","digest":"`+sbomBlob+`","size":4812}]}`)
}

// A tip publishes the scanned base under its repository's own path, attaches
// its SBOM UNSIGNED (D13: base signing is off by default), moves :stable to
// that exact digest, and asks no permit: a base is not a star.
func TestATipPublishesUnsignedWithItsSBOMAndMovesStableWithoutAPermit(t *testing.T) {
	m := basesOn(t, map[string]string{"bases/go/Dockerfile": "FROM scratch\n", "stellar-boot/main.go": "package main\n"})
	engine.stdout("--name-only", "stellar-boot/main.go\n")
	engine.script(script{match: trivyReport, leaf: "contents", value: cleanReport})
	scriptABaseTip()
	engine.script(script{leaf: "publish", match: "foundry/base-images/go", value: "registry.notusmi.com/foundry/base-images/go@sha256:" + strings.Repeat("d", 64)})
	tip(t, m)
	ref := "registry.notusmi.com/foundry/base-images/go@sha256:" + strings.Repeat("d", 64)
	wantCalls(t, engine.chain("publish(", ":g0123456789ab"),
		[]string{"dockerBuild", `dockerfile:"bases/go/Dockerfile"`, `"UV_INDEX_URL"`},
		[]string{"withRegistryAuth", `"registry.notusmi.com"`, `"publisher"`},
		[]string{"publish", `"registry.notusmi.com/foundry/base-images/go:g0123456789ab"`},
	)
	// The SBOM rides as a plain referrer — what a star built FROM this base
	// links to — and its blob is checked readable.
	wantCalls(t, engine.chain(sbomAttachNeedle), []string{"withExec", `"--artifact-type"`, `"application/vnd.cyclonedx+json"`, ref})
	wantCalls(t, engine.chain(sbomBlobNeedle), []string{"withExec", `"--descriptor"`, `"registry.notusmi.com/foundry/base-images/go@` + sbomBlob + `"`})
	for _, signing := range []string{checks.ImageCosign, `"sign","--key"`, pointerNeedle, `"verify-attestation"`, `"public-key"`} {
		if engine.chain(signing) != "" {
			t.Errorf("an unsigned base tip reached %s: base signing is the explicit --sign-bases act (D13)", signing)
		}
	}
	wantCalls(t, engine.chain("publish(", "go:stable"), []string{"publish", `"registry.notusmi.com/foundry/base-images/go:stable"`})
	if engine.chain(`"forge_mold"`) != "" {
		t.Fatal("a base asked hades for a star's permit")
	}
	settledOn(t, "0", "go: clean: published and scanned "+ref+" with its SBOM, unsigned; :stable moved to it")
}

// --sign-bases is the explicit act: the same tip signs the base, attests the
// pointer to its SBOM, and says it signed.
func TestATipToldToSignBasesSignsAndMovesStable(t *testing.T) {
	m := basesOn(t, map[string]string{"bases/go/Dockerfile": "FROM scratch\n"})
	engine.stdout("--name-only", "bases/go/Dockerfile\n")
	engine.script(script{match: trivyReport, leaf: "contents", value: cleanReport})
	scriptABaseTip()
	engine.script(script{leaf: "publish", match: "foundry/base-images/go", value: "registry.notusmi.com/foundry/base-images/go@sha256:" + strings.Repeat("d", 64)})
	tipSigned(t, m)
	ref := "registry.notusmi.com/foundry/base-images/go@sha256:" + strings.Repeat("d", 64)
	wantCalls(t, engine.chain(`"sign","--key"`), []string{"withExec", `"sign"`, ref})
	wantCalls(t, engine.chain(pointerNeedle), []string{"withExec", `"attest"`, ref})
	wantCalls(t, engine.chain("publish(", "go:stable"), []string{"publish", `"registry.notusmi.com/foundry/base-images/go:stable"`})
	settledOn(t, "0", "go: clean: published, scanned and signed "+ref+"; :stable moved to it")
}

// A base whose unsigned SBOM does not attach is not promoted.
func TestAnUnsignedBaseWhoseSBOMDoesNotAttachDoesNotPromote(t *testing.T) {
	m := basesOn(t, map[string]string{"bases/go/Dockerfile": "FROM scratch\n"})
	engine.stdout("--name-only", "bases/go/Dockerfile\n")
	engine.script(script{match: trivyReport, leaf: "contents", value: cleanReport})
	scriptABaseTip()
	engine.exitCode(sbomAttachNeedle, 1)
	engine.stdout(sbomAttachNeedle, "Error: failed to push: denied\n")
	tip(t, m)
	settledOn(t, "1", "go: findings in SBOM attach")
	if engine.chain("publish(", "go:stable") != "" {
		t.Fatal("a base whose SBOM did not attach moved :stable")
	}
}

// :stable must land on the published digest; a push that minted another is a
// finding, not a success.
func TestAStableThatNamesAnotherDigestIsAFinding(t *testing.T) {
	m := basesOn(t, map[string]string{"bases/go/Dockerfile": "FROM scratch\n"})
	engine.stdout("--name-only", "bases/go/Dockerfile\n")
	engine.script(script{match: trivyReport, leaf: "contents", value: cleanReport})
	scriptABaseTip()
	engine.script(script{leaf: "publish", match: "foundry/base-images/go", value: "registry.notusmi.com/foundry/base-images/go@sha256:" + strings.Repeat("d", 64)})
	engine.script(script{leaf: "publish", match: "go:stable", value: "registry.notusmi.com/foundry/base-images/go@sha256:" + strings.Repeat("e", 64)})
	tip(t, m)
	settledOn(t, "1", "findings in :stable: the push minted")
}

// Could-not-run outranks a finding across bases: rerunning can change it.
func TestTheWorstBaseSettlesTheLane(t *testing.T) {
	m := basesOn(t, baseTree())
	engine.stdout("--name-only", "stellar-boot/main.go\n")
	engine.fail(`dockerfile:"bases/go/Dockerfile"`, "failed to solve: dockerfile parse error on line 1")
	engine.fail(`dockerfile:"bases/rust/Dockerfile"`, "failed to resolve source metadata for docker.notusmi.com/x: 503 Service Unavailable")
	pull(t, m)
	settledOn(t, "2", "go: findings in the image build")
}

// A star's tree is not a base tree: a root Dockerfile builds as it always did,
// bases/ beside it or not.
func TestARootDockerfileIsAStarNotABaseTree(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n", "bases/x/Dockerfile": "FROM scratch\n"})
	engine.stdout("--name-only", "cmd/ares/main.go\n")
	pull(t, m)
	settledOn(t, "0", "clean: built ares at 0123456789ab")
	if engine.chain(`dockerfile:"bases/x/Dockerfile"`) != "" {
		t.Fatal("a star's bases/ directory was built as base images")
	}
}

// A python base that ships a pyproject.toml and no uv.lock is relocked before
// it builds, against the lane's own index, and builds from the locked tree; a
// base that commits its lock, or ships no pyproject, is left alone.
func TestAPythonBaseWithNoLockIsRelockedAgainstTheLanesIndex(t *testing.T) {
	m := basesOn(t, map[string]string{
		"bases/python/Dockerfile":     "FROM scratch\n",
		"bases/python/pyproject.toml": "[project]\nname = \"base\"\n",
		"bases/go/Dockerfile":         "FROM scratch\n",
	})
	engine.stdout("--name-only", "stellar-boot/main.go\n")
	engine.script(script{match: trivyReport, leaf: "contents", value: cleanReport})
	scriptABaseTip()
	tip(t, m)
	wantCalls(t, engine.chain(`"uv","lock"`),
		[]string{"from", checks.ImagePython},
		[]string{"withEnvVariable", `"UV_INDEX_URL"`, `"https://nexus.example/simple"`},
		[]string{"withEnvVariable", `"UV_NATIVE_TLS"`, `"1"`},
		[]string{"withExec", `"uv"`, `"lock"`},
	)
	// The locked directory arrives by id, so the build's chain names where it
	// was written, not what wrote it.
	if !strings.Contains(engine.chain(`dockerfile:"bases/python/Dockerfile"`, "sync"), `path:"bases/python"`) {
		t.Errorf("the python base did not build from the relocked tree:\n%s", engine.chain(`dockerfile:"bases/python/Dockerfile"`, "sync"))
	}
	if strings.Contains(engine.chain(`dockerfile:"bases/go/Dockerfile"`, "sync"), `path:"bases/`) {
		t.Error("a base with no pyproject was relocked")
	}
	settledOn(t, "0", "python: clean: published and scanned")

	m = basesOn(t, map[string]string{
		"bases/python/Dockerfile":     "FROM scratch\n",
		"bases/python/pyproject.toml": "[project]\nname = \"base\"\n",
		"bases/python/uv.lock":        "version = 1\n",
	})
	engine.stdout("--name-only", "bases/python/uv.lock\n")
	engine.script(script{match: trivyReport, leaf: "contents", value: cleanReport})
	pull(t, m)
	if engine.chain(`"uv","lock"`) != "" {
		t.Error("a base that commits its lock was relocked")
	}
}

// A relock that fails is the tree's finding, and a relock that cannot run is
// could-not-run; neither builds.
func TestARelockThatFailsStopsTheBase(t *testing.T) {
	tree := map[string]string{"bases/python/Dockerfile": "FROM scratch\n", "bases/python/pyproject.toml": "[project]\n"}
	m := basesOn(t, tree)
	engine.stdout("--name-only", "bases/python/pyproject.toml\n")
	engine.exitCode(`"uv","lock"`, 1)
	engine.stdout(`"uv","lock"`, "No solution found when resolving dependencies")
	pull(t, m)
	settledOn(t, "1", "findings in the relock")
	if engine.chain(`dockerfile:"bases/python/Dockerfile"`, "sync") != "" {
		t.Error("a base whose relock failed was built")
	}

	m = basesOn(t, tree)
	engine.stdout("--name-only", "bases/python/pyproject.toml\n")
	engine.fail(`"uv","lock"`, "engine went away")
	pull(t, m)
	settledOn(t, "2", "could not run: the relock did not run")
}

// A base whose history cannot be read is could-not-run, and builds nothing:
// the stop detect answers is the base's verdict, carried whole.
func TestABaseWhoseHistoryCannotBeReadIsCouldNotRun(t *testing.T) {
	m := basesOn(t, map[string]string{"bases/go/Dockerfile": "FROM scratch\n"})
	engine.label("foundry/base-images/go:stable", permittedSha)
	engine.fail(`"`+permittedSha+`^{commit}"`, "the engine went away")
	pull(t, m)
	settledOn(t, "2", "go: could not run: the history could not be read")
	if engine.chain(`dockerfile:"bases/go/Dockerfile"`) != "" {
		t.Fatal("a base whose history could not be read was built")
	}
}
