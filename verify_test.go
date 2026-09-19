package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
)

// The base a star pins, its SBOM referrer in the registry, and the document
// that referrer stores.
const (
	pinnedBase       = "registry.notusmi.com/rob/stellar_core:python-runtime@sha256:9a21aefeb6af6265024a4aa614b66607a3eb65810d8739e0c8a410af51b74459"
	pinnedBaseRepo   = "registry.notusmi.com/rob/stellar_core"
	pinnedBaseDigest = "sha256:9a21aefeb6af6265024a4aa614b66607a3eb65810d8739e0c8a410af51b74459"
	baseArtifact     = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	baseBlob         = "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	// baseSBOM carries the runtime package the image's SBOM also carries, so
	// composing drops it from the star's and links the base's instead.
	baseSBOM = `{"bomFormat":"CycloneDX","components":[{"name":"runtime","version":"1","purl":"pkg:deb/runtime@1"},{"name":"libc","version":"2","purl":"pkg:deb/libc@2"}]}`
	// pinnedDockerfile is a star built on the pinned base, with a builder stage.
	pinnedDockerfile = "FROM docker.notusmi.com/library/golang:1.26 AS builder\nRUN go build ./...\nFROM " + pinnedBase + "\nCOPY --from=builder /out/x /x\n"
)

const (
	discoverNeedle = `"oras","discover"`
	// imageReportNeedle and baseReportNeedle are each in one scan alone.
	imageReportNeedle = `"--input","/scan/image.tar"`
	baseReportNeedle  = `"--input","/scan/base.tar"`
)

func report(findings ...string) string {
	if len(findings) == 0 {
		return `{"Results":[{"Target":"img","Vulnerabilities":null}]}`
	}
	return `{"Results":[{"Target":"img","Vulnerabilities":[` + strings.Join(findings, ",") + `]}]}`
}

const (
	grpcFinding = `{"VulnerabilityID":"GO-2026-1","PkgName":"google.golang.org/grpc","InstalledVersion":"v1.83.1","FixedVersion":"1.83.2","Severity":"HIGH"}`
	perlFinding = `{"VulnerabilityID":"CVE-2026-2","PkgName":"perl-base","InstalledVersion":"5.40.1-6","FixedVersion":"5.40.1-6+deb13u1","Severity":"HIGH"}`
)

// scriptTheBase scripts the base's SBOM in the registry: one referrer, its
// manifest, its blob.
func scriptTheBase() {
	engine.stdout(discoverNeedle, `{"referrers":[{"digest":"`+baseArtifact+`","artifactType":"application/vnd.cyclonedx+json","annotations":{"org.opencontainers.image.created":"2026-09-16T22:30:13Z"}}]}`)
	engine.stdout(`"oras","manifest","fetch","`+pinnedBaseRepo+`@`+baseArtifact, `{"schemaVersion":2,"layers":[{"mediaType":"application/vnd.cyclonedx+json","digest":"`+baseBlob+`","size":3414}]}`)
	engine.stdout(`"oras","blob","fetch","--output","-","`+pinnedBaseRepo+`@`+baseBlob, baseSBOM)
}

func verify(t *testing.T, m *FoundryTools) *StageResult {
	t.Helper()
	res, err := m.Verify(context.Background(), "", "https://forgejo.notusmi.com/rob")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return res
}

func atomOf(t *testing.T, rows []AtomResult, id string) AtomResult {
	t.Helper()
	for _, a := range rows {
		if a.Atom == id {
			return a
		}
	}
	t.Fatalf("no %s among %+v", id, rows)
	return AtomResult{}
}

// A tree with no Dockerfile ships no image: both atoms are absent, the stage
// is clean, and the log says why rather than nothing.
func TestVerifyOnATreeWithNoImageIsAbsent(t *testing.T) {
	m := buildOn(t, map[string]string{"README.md": "# ares\n"})
	res := verify(t, m)
	if res.Stage != "verify" || res.State != 0 || len(res.Atoms) != 0 || len(res.Omitted) != 2 {
		t.Fatalf("result = %+v, want a clean stage with both atoms omitted", res)
	}
	if !strings.Contains(res.Log, "no Dockerfile at its root") {
		t.Errorf("log does not say why:\n%s", res.Log)
	}
	if engine.chain("dockerBuild(") != "" {
		t.Error("a tree with no Dockerfile was built")
	}
}

// A repository of bases is not a star: the bases lane scans each of those.
func TestVerifyOnABasesTreeIsAbsentAndSaysSo(t *testing.T) {
	m := basesOn(t, baseTree())
	res := verify(t, m)
	if res.State != 0 || len(res.Omitted) != 2 || !strings.Contains(res.Log, "forges 2 base image(s)") {
		t.Fatalf("result = %+v, want both atoms absent naming the two bases:\n%s", res, res.Log)
	}
}

// A module constructed on no commit has nothing fetched to scan.
func TestVerifyRefusesATreeItDidNotFetch(t *testing.T) {
	engine.reset()
	m := &FoundryTools{Source: dag.Directory()}
	res := verify(t, m)
	if res.State != 2 || len(res.Atoms) != 2 {
		t.Fatalf("result = %+v, want could-not-run on both atoms", res)
	}
	if !strings.Contains(res.Log, "--repo and --sha") {
		t.Errorf("log does not name the fix:\n%s", res.Log)
	}
}

// THE STAR'S OWN FINDING IS A FINDING; THE BASE'S IS NAMED AND NOT COUNTED.
// The image carries grpc (its own binary's) and perl (its base's); the base
// carries perl. The stage settles on grpc alone.
func TestVerifyGatesOnTheStarsOwnLayerAndNamesTheBases(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": pinnedDockerfile})
	engine.script(script{match: imageReportNeedle, leaf: "contents", value: report(grpcFinding, perlFinding)})
	engine.script(script{match: baseReportNeedle, leaf: "contents", value: report(perlFinding)})
	engine.stdout(imageScanNeedle, imageSBOM)
	engine.stdout(`"oci-archive:/in/builder.tar"`, builderSBOM)
	scriptTheBase()

	res := verify(t, m)
	if res.State != 1 {
		t.Fatalf("state = %d, want findings:\n%s", res.State, res.Log)
	}
	scan := atomOf(t, res.Atoms, atomScan)
	if scan.State != 1 || !strings.Contains(scan.Reason, "1 fixable HIGH or CRITICAL vulnerabilities in this image's own layer") || !strings.Contains(scan.Reason, "google.golang.org/grpc") {
		t.Errorf("scan = %+v, want grpc as the star's own finding", scan)
	}
	if !strings.Contains(scan.Reason, "1 inherited from the base "+pinnedBase) || !strings.Contains(scan.Reason, "perl-base") {
		t.Errorf("scan = %+v, want perl named as the base's", scan)
	}
	// The base was scanned with the same flags, from its own tarball — pulled
	// by digest, which is the tarball chain's own call.
	wantCalls(t, engine.chain(baseReportNeedle),
		[]string{"withExec", `"image"`, `"--severity"`, `"HIGH,CRITICAL"`, `"--ignore-unfixed"`},
	)
	if engine.chain(`from(address:"`+pinnedBaseRepo+`@`+pinnedBaseDigest+`")`, "asTarball") == "" {
		t.Error("the base was not pulled by its digest for the scan")
	}
	// The SBOM is composed: the builder's dependency kept, the base's runtime
	// package dropped, the base's document linked.
	sbom := atomOf(t, res.Atoms, atomSBOM)
	if sbom.State != 0 || !strings.Contains(sbom.Reason, "1 components kept of 2") || !strings.Contains(sbom.Reason, "1 shared with the base") {
		t.Errorf("sbom = %+v, want gomod kept and runtime linked away", sbom)
	}
	if !strings.Contains(sbom.Reason, "linked at oci://"+pinnedBaseRepo+"@"+baseBlob) {
		t.Errorf("sbom = %+v, want the link named", sbom)
	}
	if !strings.Contains(sbom.Reason, "SBOM components: image=1 builder=1 merged=2") {
		t.Errorf("sbom = %+v, want the builder merge counted", sbom)
	}
	if res.Lanes[0] != "image" || len(res.Lanes) != 1 {
		t.Errorf("lanes = %v, want image", res.Lanes)
	}
}

// Findings the base already carries leave the star clean, and named.
func TestVerifyIsCleanWhenEveryFindingIsTheBases(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM " + pinnedBase + "\nCOPY x /x\n"})
	engine.script(script{match: imageReportNeedle, leaf: "contents", value: report(perlFinding)})
	engine.script(script{match: baseReportNeedle, leaf: "contents", value: report(perlFinding)})
	engine.stdout(imageScanNeedle, imageSBOM)
	scriptTheBase()

	res := verify(t, m)
	if res.State != 0 {
		t.Fatalf("state = %d, want clean:\n%s", res.State, res.Log)
	}
	scan := atomOf(t, res.Atoms, atomScan)
	if !strings.Contains(scan.Reason, "scan clean") || !strings.Contains(scan.Reason, "1 inherited from the base") {
		t.Errorf("scan = %+v, want clean with the inherited finding named", scan)
	}
}

// An unpinned base has nothing to subtract: every finding is the star's, and
// the SBOM is self-contained because there is no digest to link.
func TestVerifyWithNoPinnedBaseOwnsEveryFinding(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\nCOPY x /x\n"})
	engine.script(script{match: imageReportNeedle, leaf: "contents", value: report(perlFinding)})
	engine.stdout(imageScanNeedle, imageSBOM)

	res := verify(t, m)
	if res.State != 1 {
		t.Fatalf("state = %d, want findings:\n%s", res.State, res.Log)
	}
	if engine.chain(discoverNeedle) != "" {
		t.Error("a base with no digest was looked up in the registry")
	}
	sbom := atomOf(t, res.Atoms, atomSBOM)
	if !strings.Contains(sbom.Reason, "self-contained") || !strings.Contains(sbom.Reason, "no pinned base") {
		t.Errorf("sbom = %+v, want self-contained, saying why", sbom)
	}
}

// A base with no SBOM of its own leaves the star's self-contained and says so;
// the scan is still subtracted, because the base image itself is there.
func TestVerifyOnABaseWithNoSBOMIsSelfContained(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM " + pinnedBase + "\nCOPY x /x\n"})
	engine.script(script{match: imageReportNeedle, leaf: "contents", value: report()})
	engine.script(script{match: baseReportNeedle, leaf: "contents", value: report()})
	engine.stdout(imageScanNeedle, imageSBOM)
	engine.stdout(discoverNeedle, `{"referrers":[]}`)

	res := verify(t, m)
	sbom := atomOf(t, res.Atoms, atomSBOM)
	if res.State != 0 || sbom.State != 0 || !strings.Contains(sbom.Reason, "has no SBOM referrer, so the SBOM is self-contained") {
		t.Errorf("sbom = %+v, want self-contained naming the base:\n%s", sbom, res.Log)
	}
}

// A registry that cannot be read is could-not-run for the SBOM, and the scan
// still answers: the two atoms are independent.
func TestVerifyRegistryFailuresAreCouldNotRunForTheSBOMAlone(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM " + pinnedBase + "\nCOPY x /x\n"})
	engine.script(script{match: imageReportNeedle, leaf: "contents", value: report()})
	engine.script(script{match: baseReportNeedle, leaf: "contents", value: report()})
	engine.stdout(imageScanNeedle, imageSBOM)
	engine.exitCode(discoverNeedle, 1)
	engine.stdout(discoverNeedle, "Error: 502 Bad Gateway")

	res := verify(t, m)
	if res.State != 2 {
		t.Fatalf("state = %d, want could-not-run:\n%s", res.State, res.Log)
	}
	if scan := atomOf(t, res.Atoms, atomScan); scan.State != 0 {
		t.Errorf("scan = %+v, want the scan unaffected", scan)
	}
	if sbom := atomOf(t, res.Atoms, atomSBOM); sbom.State != 2 || !strings.Contains(sbom.Reason, "referrers could not be listed") {
		t.Errorf("sbom = %+v, want could-not-run naming the listing", sbom)
	}
}

// Trivy that did not scan is could-not-run, never a pass — its own exit code
// is not the verdict, and an image whose scan failed must not read clean.
func TestVerifyATrivyThatDoesNotRunIsCouldNotRun(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\nCOPY x /x\n"})
	engine.exitCode(imageReportNeedle, 1)
	engine.stdout(imageReportNeedle, "FATAL failed to download the vulnerability DB")
	engine.stdout(imageScanNeedle, imageSBOM)

	res := verify(t, m)
	scan := atomOf(t, res.Atoms, atomScan)
	if scan.State != 2 || !strings.Contains(scan.Reason, "trivy exited 1 scanning the image") {
		t.Errorf("scan = %+v, want could-not-run naming trivy's exit", scan)
	}
	if res.State != 2 {
		t.Errorf("state = %d, want the stage could-not-run", res.State)
	}
}

// A report that is not JSON is a scan that did not happen.
func TestVerifyAnUnreadableReportIsCouldNotRun(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\nCOPY x /x\n"})
	engine.script(script{match: imageReportNeedle, leaf: "contents", value: "not json"})
	engine.stdout(imageScanNeedle, imageSBOM)
	res := verify(t, m)
	if scan := atomOf(t, res.Atoms, atomScan); scan.State != 2 || !strings.Contains(scan.Reason, "trivy's report is not JSON") {
		t.Errorf("scan = %+v", scan)
	}
}

// An image that does not build fails both atoms with the build's own reason.
func TestVerifyAnImageThatDoesNotBuildFailsBothAtoms(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\nCOPY x /x\n"})
	engine.fail("dockerBuild", "failed to solve: COPY x: not found")
	res := verify(t, m)
	if res.State != 1 || len(res.Atoms) != 2 {
		t.Fatalf("result = %+v, want findings on both atoms:\n%s", res, res.Log)
	}
	for _, a := range res.Atoms {
		if !strings.Contains(a.Reason, "the image build") {
			t.Errorf("%s = %+v, want the build named", a.Atom, a)
		}
	}
}

// Exit ends on the stage's state with its log, as the other stages do.
func TestVerifyExitsOnTheStagesState(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\nCOPY x /x\n"})
	engine.script(script{match: imageReportNeedle, leaf: "contents", value: report(grpcFinding)})
	engine.stdout(imageScanNeedle, imageSBOM)
	res := verify(t, m)
	if err := res.Exit(context.Background()); err != nil {
		t.Fatalf("exit: %v", err)
	}
	settledOn(t, "1", "image:trivy · findings")
}

func TestVerdictShapes(t *testing.T) {
	v := atomVerdict(atomScan, buildlane.Findings, "findings in the scan: 1\n")
	if v.State != 1 || v.Result != "findings" || v.Stage != "verify" || v.Lane != "image" || v.Reason != "image:trivy: findings in the scan: 1" {
		t.Errorf("atomVerdict = %+v", v)
	}
	a := absent(atomSBOM, "no Dockerfile")
	if a.State != 0 || a.Result != "absent" || !strings.HasPrefix(a.Reason, "image:sbom: ABSENT - ") {
		t.Errorf("absent = %+v", a)
	}
	if _, ok := checks.AnnouncedAbsence(atomSBOM, a.Reason); !ok {
		t.Errorf("absent's reason is not the shape AnnouncedAbsence reads: %q", a.Reason)
	}
}

// oras that cannot be provisioned fails the SBOM atom alone, and says so with
// the fetch's own reason; the scan has already answered.
func TestVerifyOrasThatCannotBeProvisionedIsCouldNotRunForTheSBOM(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM " + pinnedBase + "\nCOPY x /x\n"})
	engine.script(script{match: imageReportNeedle, leaf: "contents", value: report(grpcFinding)})
	engine.script(script{match: baseReportNeedle, leaf: "contents", value: report()})
	engine.fail(`http(url:"`+checks.OrasURL+`")`, "502 from upstream")

	res := verify(t, m)
	if res.State != 2 {
		t.Fatalf("state = %d, want could-not-run:\n%s", res.State, res.Log)
	}
	if scan := atomOf(t, res.Atoms, atomScan); scan.State != 1 {
		t.Errorf("scan = %+v, want its own finding kept", scan)
	}
	sbom := atomOf(t, res.Atoms, atomSBOM)
	if sbom.State != 2 || !strings.Contains(sbom.Reason, "could not run: oras could not be provisioned: could not fetch "+checks.OrasURL) {
		t.Errorf("sbom = %+v, want could-not-run naming the fetch", sbom)
	}
	if engine.chain(imageScanNeedle) != "" {
		t.Error("syft ran with no oras to read the base's document with")
	}
}

// Every way the base's document can fail to read is could-not-run for the
// SBOM atom, naming what was wrong: a listing that is not JSON, a referrer
// manifest that is not one layer of CycloneDX, a blob that is not a document.
func TestVerifyABaseDocumentThatDoesNotReadIsCouldNotRun(t *testing.T) {
	cases := map[string]struct {
		script func()
		names  string
	}{
		"the listing is not JSON": {func() {
			engine.stdout(discoverNeedle, "not json")
		}, "oras discover's answer is not JSON"},
		"the referrer's manifest is not one CycloneDX layer": {func() {
			engine.stdout(discoverNeedle, `{"referrers":[{"digest":"`+baseArtifact+`","artifactType":"application/vnd.cyclonedx+json"}]}`)
			engine.stdout(`"oras","manifest","fetch","`+pinnedBaseRepo+`@`+baseArtifact, `{"schemaVersion":2,"layers":[]}`)
		}, "the SBOM referrer's manifest carries 0 layers, not one"},
		"the blob is not a document": {func() {
			scriptTheBase()
			engine.stdout(`"oras","blob","fetch","--output","-","`+pinnedBaseRepo+`@`+baseBlob, "{")
		}, "the base SBOM is not JSON"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := buildOn(t, map[string]string{"Dockerfile": "FROM " + pinnedBase + "\nCOPY x /x\n"})
			engine.script(script{match: imageReportNeedle, leaf: "contents", value: report()})
			engine.script(script{match: baseReportNeedle, leaf: "contents", value: report()})
			engine.stdout(imageScanNeedle, imageSBOM)
			c.script()
			res := verify(t, m)
			sbom := atomOf(t, res.Atoms, atomSBOM)
			if sbom.State != 2 || !strings.Contains(sbom.Reason, "could not run: "+c.names) {
				t.Errorf("sbom = %+v, want could-not-run naming %q", sbom, c.names)
			}
			if scan := atomOf(t, res.Atoms, atomScan); scan.State != 0 {
				t.Errorf("scan = %+v, want the scan unaffected", scan)
			}
		})
	}
}
