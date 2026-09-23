package main

import "strings"

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
