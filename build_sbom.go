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

// THE SBOM (CA master-plan F14, "F14's SBOM contract"). One document per
// image: its own components, the builder stage's folded in when the Dockerfile
// names one, and its base image's document LINKED rather than copied. The
// measurements behind each line are in internal/buildlane/sbomcompose.go.

// orasIn is the fleet image with the oras client on its path; the registry
// login, when given, is mounted where oras reads a docker config.
func orasIn(ctx context.Context, registryAuth *dagger.Secret) (*dagger.Container, error) {
	tarball, err := fetchTool(ctx, checks.OrasURL)
	if err != nil {
		return nil, fmt.Errorf("oras could not be provisioned: %w", err)
	}
	oras := dag.Container().From(checks.ImageFleet).
		WithFile("/tmp/oras.tgz", tarball).
		WithExec([]string{"tar", "-xzf", "/tmp/oras.tgz", "-C", "/usr/local/bin", "oras"})
	if registryAuth != nil {
		oras = oras.WithMountedSecret("/run/docker/config.json", registryAuth)
	}
	return oras, nil
}

// orasRead runs one anonymous oras read. The fleet's registry answers reads
// without a login, and a read that needs none carries none: a star's Verify
// runs with no secrets at all.
func orasRead(oras *dagger.Container, stamp string, args ...string) *dagger.Container {
	return oras.WithEnvVariable("BUILD_RUN", stamp).WithExec(append([]string{"oras"}, args...), anyExit)
}

// syftIn is the syft image, told not to write file entries.
//
// SYFT_FILE_METADATA_SELECTION=none IS CONTRACT LINE 1. Measured across 12
// stars, syft's per-file SHA-1/SHA-256 components were 7.5 MB of 13.3 MB of
// SBOM — 56% of the fleet's volume — and the one reader takes components
// carrying a purl, so nothing had ever read one. iris: 2,819 KB -> 1,916 KB.
func syftIn() *dagger.Container {
	return toolOnFleetBase(checks.ImageSyft, "/syft", "syft").
		WithEnvVariable("SYFT_FILE_METADATA_SELECTION", "none")
}

// syftArgs is the scan's shape: the bun cache excluded (a builder stage's
// download cache is not a dependency), CycloneDX 1.6 out.
var syftArgs = []string{"--exclude", "/root/.bun/**", "-o", "cyclonedx-json@1.6"}

// sbomOf reads the image's composed SBOM.
//
// THE IMAGE IS SCANNED AS THE TARBALL THE ENGINE BUILT, not read back from the
// registry after a push: Verify runs before anything is published, and
// Image.Container() stamps its config once so the tarball scanned and the
// manifest published are one digest. The builder stage folds in as before —
// it is the only place a bundled star's dependencies exist (rob/calliope's
// published image has zero npm packages; its builder stage has 452).
//
// The answer is the document, a line for the log saying what composing did,
// and the lane's three-state code: could-not-run when a tool did not run, a
// finding when syft refused the image, clean otherwise.
func sbomOf(ctx context.Context, src *dagger.Directory, img *Image, oras *dagger.Container, stamp string) (sbom string, note string, code int, why string) {
	syft := syftIn()
	image, rc, err := output(ctx, syft.WithMountedFile("/in/image.tar", img.Tarball()).
		WithExec(append([]string{"oci-archive:/in/image.tar"}, syftArgs...), entrypointAnyExit))
	if err != nil {
		return "", "", buildlane.CouldNotRun, fmt.Sprintf("could not run: syft did not run: %v", err)
	}
	if rc != 0 {
		c, why := buildlane.ToolFailed("SBOM", image)
		return "", "", c, why
	}
	own, builderNote := withBuilder(ctx, src, img, syft, image)

	dockerfile, _, _ := fileIn(ctx, src, img.dockerfilePath())
	base, baseDigest, known := buildlane.RuntimeBase(dockerfile)
	var link *buildlane.BaseLink
	var baseDoc []byte
	baseNote := "no pinned base in the Dockerfile to link"
	if known && baseDigest != "" {
		var c int
		baseDoc, link, baseNote, c, why = baseDocument(ctx, oras, base, baseDigest, stamp)
		if c != buildlane.Clean {
			return "", "", c, why
		}
	}
	composed, st, err := buildlane.ComposeSBOM([]byte(own), baseDoc, link)
	if err != nil {
		return "", "", buildlane.CouldNotRun, "could not run: " + err.Error()
	}
	return string(composed), strings.TrimSpace(builderNote + "\n" + st.String() + "; " + baseNote), buildlane.Clean, ""
}

// withBuilder folds the builder stage's SBOM into the image's when the
// Dockerfile names one and it builds. Every way the builder side can fail
// leaves the image's own document, said aloud: the builder is extra coverage,
// and a runtime image that scanned is still an answer.
func withBuilder(ctx context.Context, src *dagger.Directory, img *Image, syft *dagger.Container, image string) (string, string) {
	dockerfile, _, err := fileIn(ctx, src, img.dockerfilePath())
	if err != nil || !buildlane.HasBuilderStage(dockerfile) {
		return image, "no builder stage in this Dockerfile — the SBOM covers the runtime image only"
	}
	tar := img.Builder()
	if _, err := tar.Size(ctx); err != nil {
		return image, fmt.Sprintf("WARNING the builder stage did not build — the SBOM covers the runtime image only: %v", err)
	}
	builder, code, err := output(ctx, syft.WithMountedFile("/in/builder.tar", tar).
		WithExec(append([]string{"oci-archive:/in/builder.tar"}, syftArgs...), entrypointAnyExit))
	if err != nil || code != 0 {
		return image, "WARNING the builder stage's SBOM did not read — the SBOM covers the runtime image only"
	}
	merged, in, bn, mn, err := buildlane.MergeSBOM([]byte(image), []byte(builder))
	if err != nil {
		return image, fmt.Sprintf("WARNING the SBOMs did not merge (%v) — the SBOM covers the runtime image only", err)
	}
	return string(merged), fmt.Sprintf("SBOM components: image=%d builder=%d merged=%d", in, bn, mn)
}

// baseDocument finds the runtime base's own SBOM in the registry: the newest
// CycloneDX referrer on the base's digest, its one layer, and that blob.
//
// A BASE WITH NO SBOM IS NOT A FAULT OF THIS TREE. Of 403 stellar_core tags
// measured on 2026-09-17, 97 carried none; a star built on one gets a
// self-contained document and a line saying why. A registry that cannot be
// read is could-not-run, as every registry read is.
func baseDocument(ctx context.Context, oras *dagger.Container, base, digest, stamp string) (doc []byte, link *buildlane.BaseLink, note string, code int, why string) {
	repo := buildlane.RepoOf(base)
	at := repo + "@" + digest
	listed, rc, err := output(ctx, orasRead(oras, stamp, "discover", "--format", "json", "--artifact-type", buildlane.SBOMMediaType, at))
	if err != nil || rc != 0 {
		return nil, nil, "", buildlane.CouldNotRun, fmt.Sprintf("could not run: the base %s's referrers could not be listed (exit %d): %v %.200s", at, rc, err, listed)
	}
	artifact, found, err := buildlane.NewestReferrer([]byte(listed), buildlane.SBOMMediaType)
	if err != nil {
		return nil, nil, "", buildlane.CouldNotRun, "could not run: " + err.Error()
	}
	if !found {
		return nil, nil, fmt.Sprintf("base %s has no SBOM referrer, so the SBOM is self-contained", at), buildlane.Clean, ""
	}
	manifest, rc, err := output(ctx, orasRead(oras, stamp, "manifest", "fetch", repo+"@"+artifact))
	if err != nil || rc != 0 {
		return nil, nil, "", buildlane.CouldNotRun, fmt.Sprintf("could not run: the base's SBOM referrer %s could not be read (exit %d): %v %.200s", artifact, rc, err, manifest)
	}
	blob, size, err := buildlane.SBOMLayer(manifest)
	if err != nil {
		return nil, nil, "", buildlane.CouldNotRun, "could not run: " + err.Error()
	}
	body, rc, err := output(ctx, orasRead(oras, stamp, "blob", "fetch", "--output", "-", repo+"@"+blob))
	if err != nil || rc != 0 {
		return nil, nil, "", buildlane.CouldNotRun, fmt.Sprintf("could not run: the base's SBOM %s could not be read (exit %d): %v %.200s", blob, rc, err, body)
	}
	l := &buildlane.BaseLink{Repo: repo, Blob: blob, Size: size}
	return []byte(body), l, fmt.Sprintf("base %s's SBOM linked at %s", at, l.Locator()), buildlane.Clean, ""
}

// composedSBOM provisions oras with the registry login and reads the image's
// composed SBOM (sbomOf), saying what composing did.
func (l *buildLane) composedSBOM(ctx context.Context, img *Image) (*dagger.Container, string, *laneStop) {
	oras, err := orasIn(ctx, l.registryAuth)
	if err != nil {
		return nil, "", &laneStop{buildlane.CouldNotRun, "could not run: " + err.Error()}
	}
	sbom, note, code, why := sbomOf(ctx, l.m.Source, img, oras, l.stamp)
	if code != buildlane.Clean {
		return nil, "", &laneStop{code, why}
	}
	say("%s", note)
	return oras, sbom, nil
}

// publishSBOM is a star tip's SBOM, UNSIGNED (Scheduler Redistribution Part
// II, D11): the composed document attached beside the image as a plain OCI
// referrer, read back, and its blob checked readable. A star's image carries
// no CI signature and no signed pointer any more — nothing verifies either
// since the permit retired (Phase 13) — but the SBOM is still owed to every
// image this lane publishes.
func (l *buildLane) publishSBOM(ctx context.Context, img *Image, ref string) *laneStop {
	oras, sbom, failed := l.composedSBOM(ctx, img)
	if failed != nil {
		return failed
	}
	oras = sbomIn(oras, sbom, l.stamp)
	_, blob, _, failed := attachSBOM(ctx, oras, ref)
	if failed != nil {
		return failed
	}
	return blobReadable(ctx, oras, ref, blob)
}

// sbomIn is oras with the SBOM written where the attach reads it.
func sbomIn(oras *dagger.Container, sbom, stamp string) *dagger.Container {
	return oras.
		WithNewFile("/in/sbom.cdx.json", sbom).
		WithWorkdir("/in").
		WithEnvVariable("BUILD_RUN", stamp)
}

// orasExec runs one oras command with the registry login. The login is a flag
// of the command, so it follows the whole command path (`oras manifest fetch
// --registry-config …`), never the first word of it.
func orasExec(oras *dagger.Container, command []string, args ...string) *dagger.Container {
	argv := append(append([]string{"oras"}, command...), "--registry-config", "/run/docker/config.json")
	return oras.WithExec(append(argv, args...), anyExit)
}

// attachSBOM puts the image's SBOM beside it as a plain OCI referrer and reads
// back what the registry stored: the referrer's manifest digest and its one
// layer's blob and size.
func attachSBOM(ctx context.Context, oras *dagger.Container, ref string) (artifact, blob string, size int64, failed *laneStop) {
	repo, _, _ := strings.Cut(ref, "@")
	// The layer is titled by its file name, so the attach runs where the file is,
	// and it prints the referrer's digest alone — its text output also names the
	// subject's digest, which is not the one the pointer needs.
	out, code, err := output(ctx, orasExec(oras, []string{"attach"},
		"--artifact-type", buildlane.SBOMMediaType,
		"--annotation", "org.opencontainers.image.created="+time.Now().UTC().Format(time.RFC3339),
		"--format", "go-template", "--template", "{{.digest}}",
		ref, "sbom.cdx.json:"+buildlane.SBOMMediaType))
	if err != nil {
		return "", "", 0, &laneStop{buildlane.CouldNotRun, fmt.Sprintf("could not run: the SBOM attach did not run: %v", err)}
	}
	if code != 0 {
		c, why := buildlane.ToolFailed("SBOM attach", out)
		return "", "", 0, &laneStop{c, why}
	}
	artifact = buildlane.DigestOf(out)
	if artifact == "" {
		return "", "", 0, &laneStop{buildlane.CouldNotRun, fmt.Sprintf("could not run: oras attached the SBOM and answered no digest: %.200s", out)}
	}
	// The pointer names what the registry stored, read back — never a hash of the
	// local file.
	manifest, code, err := output(ctx, orasExec(oras, []string{"manifest", "fetch"}, repo+"@"+artifact))
	if err != nil || code != 0 {
		return "", "", 0, &laneStop{buildlane.CouldNotRun, fmt.Sprintf("could not run: the SBOM referrer %s could not be read back (exit %d): %v %.200s", artifact, code, err, manifest)}
	}
	blob, size, err = buildlane.SBOMLayer(manifest)
	if err != nil {
		return "", "", 0, &laneStop{buildlane.CouldNotRun, "could not run: " + err.Error()}
	}
	say("attached the SBOM to %s as %s (blob %s, %d bytes)", ref, artifact, blob, size)
	return artifact, blob, size, nil
}

// blobReadable checks the SBOM's blob is readable from the registry: the
// reader that follows the referrer is owed a document it can fetch.
func blobReadable(ctx context.Context, oras *dagger.Container, ref, blob string) *laneStop {
	repo, _, _ := strings.Cut(ref, "@")
	found, code, err := output(ctx, orasExec(oras, []string{"blob", "fetch"}, "--descriptor", repo+"@"+blob))
	if err != nil || code != 0 {
		return &laneStop{buildlane.CouldNotRun, fmt.Sprintf("could not run: the SBOM blob the pointer names (%s@%s) is not readable from the registry (exit %d): %v %.200s", repo, blob, code, err, found)}
	}
	return nil
}

// attestSBOM is a BASE's SBOM: attached and read back as a star's is
// (attachSBOM), and a POINTER to it (buildlane.SBOMRefType) signed — never the
// SBOM itself, which zot copied into its metadata whole
// (internal/buildlane/sbomref.go has the measurement). Then it checks both
// halves: the pointer verifies against the CI key, and the blob it names is
// readable from the registry.
func (l *buildLane) attestSBOM(ctx context.Context, cosign, oras *dagger.Container, ref, sbom string) (int, string) {
	oras = sbomIn(oras, sbom, l.stamp)
	artifact, blob, size, failed := attachSBOM(ctx, oras, ref)
	if failed != nil {
		return failed.code, failed.why
	}
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
	if failed := blobReadable(ctx, oras, ref, blob); failed != nil {
		return failed.code, failed.why
	}
	say("attested a pointer to the SBOM (%s) on %s", buildlane.SBOMRefType, ref)
	return buildlane.Clean, ""
}
