package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// attestSBOM puts the image's SBOM beside it as a plain OCI referrer and signs a
// POINTER to it (buildlane.SBOMRefType) — never the SBOM itself, which zot copied
// into its metadata whole (internal/buildlane/sbomref.go has the measurement).
// Then it checks both halves, every time: the pointer verifies against the CI
// key, and the blob it names is readable from the registry. Either failing
// withholds the permit as a could-not-run, as a failed sign does — the reader
// that follows the pointer (foundry-stocks' forge-portfolio) is owed a good one on
// every image this lane publishes (Zuse7, 2026-09-15).
func (l *buildLane) attestSBOM(ctx context.Context, cosign *dagger.Container, ref, sbom string) (int, string) {
	repo, _, _ := strings.Cut(ref, "@")
	tarball, err := fetchTool(ctx, checks.OrasMirror, checks.OrasURL)
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: oras could not be provisioned: %v", err)
	}
	oras := dag.Container().From(checks.ImageFleet).
		WithFile("/tmp/oras.tgz", tarball).
		WithExec([]string{"tar", "-xzf", "/tmp/oras.tgz", "-C", "/usr/local/bin", "oras"}).
		WithMountedSecret("/run/docker/config.json", l.registryAuth).
		WithNewFile("/in/sbom.cdx.json", sbom).
		WithWorkdir("/in").
		WithEnvVariable("BUILD_RUN", l.stamp)
	// orasExec runs one oras command with the registry login. The login is a flag
	// of the command, so it follows the whole command path (`oras manifest fetch
	// --registry-config …`), never the first word of it.
	orasExec := func(command []string, args ...string) *dagger.Container {
		argv := append(append([]string{"oras"}, command...), "--registry-config", "/run/docker/config.json")
		return oras.WithExec(append(argv, args...), anyExit)
	}

	// The layer is titled by its file name, so the attach runs where the file is,
	// and it prints the referrer's digest alone — its text output also names the
	// subject's digest, which is not the one the pointer needs.
	out, code, err := output(ctx, orasExec([]string{"attach"},
		"--artifact-type", buildlane.SBOMMediaType,
		"--annotation", "org.opencontainers.image.created="+time.Now().UTC().Format(time.RFC3339),
		"--format", "go-template", "--template", "{{.digest}}",
		ref, "sbom.cdx.json:"+buildlane.SBOMMediaType))
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: the SBOM attach did not run: %v", err)
	}
	if code != 0 {
		return buildlane.ToolFailed("SBOM attach", out)
	}
	artifact := buildlane.DigestOf(out)
	if artifact == "" {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: oras attached the SBOM and answered no digest: %.200s", out)
	}
	// The pointer names what the registry stored, read back — never a hash of the
	// local file.
	manifest, code, err := output(ctx, orasExec([]string{"manifest", "fetch"}, repo+"@"+artifact))
	if err != nil || code != 0 {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: the SBOM referrer %s could not be read back (exit %d): %v %.200s", artifact, code, err, manifest)
	}
	blob, size, err := buildlane.SBOMLayer(manifest)
	if err != nil {
		return buildlane.CouldNotRun, "could not run: " + err.Error()
	}
	say("attached the SBOM to %s as %s (blob %s, %d bytes)", ref, artifact, blob, size)

	pointing := cosign.WithNewFile("/in/sbom-ref.json", buildlane.SBOMRefPredicate(artifact, blob, size))
	attested, attestCode, err := output(ctx, pointing.WithExec([]string{"attest", "--key", "/run/cosign/key", "--type", buildlane.SBOMRefType, "--predicate", "/in/sbom-ref.json", "--yes", "--tlog-upload=false", "--use-signing-config=false", ref}, entrypointAnyExit))
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: the SBOM pointer attestation did not run: %v", err)
	}
	checked, checkCode, err := output(ctx, pointing.WithExec([]string{"verify-attestation", "--key", "/run/cosign/key.pub", "--type", buildlane.SBOMRefType, "--insecure-ignore-tlog=true", ref}, entrypointAnyExit))
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: the SBOM pointer check did not run: %v", err)
	}
	if checkCode != 0 {
		// Neither attested nor already there reads like a sign that did not land;
		// attested and still unverifiable is a publish no reader can follow.
		if attestCode != 0 {
			return buildlane.ToolFailed("sign (SBOM pointer)", attested+"\n"+checked)
		}
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: the SBOM pointer was attested and does not verify against the CI key: %.200s", checked)
	}
	found, code, err := output(ctx, orasExec([]string{"blob", "fetch"}, "--descriptor", repo+"@"+blob))
	if err != nil || code != 0 {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: the SBOM blob the pointer names (%s@%s) is not readable from the registry (exit %d): %v %.200s", repo, blob, code, err, found)
	}
	say("attested a pointer to the SBOM (%s) on %s", buildlane.SBOMRefType, ref)
	return buildlane.Clean, ""
}
