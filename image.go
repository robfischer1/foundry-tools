package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/dagger"
)

// THE IMAGE LANE — the star's own Dockerfile, built, labelled and published by
// the engine.
//
// ONE BUILDKIT. Until 2026-09-12 the build lane ran three buildctl builds
// against a second BuildKit daemon (infra flux/apps/buildkitd.yaml) while
// every gate ran here, and the manifest recorded two reasons the two could
// not be one: that mold read a two-manifest OCI index buildctl's provenance
// attestation produced, and that the engine could not take a buildctl build.
// Both were measured false that day — mold resolves the g-pin by descriptor
// and reads config with `oras manifest fetch-config --platform`, which answers
// on a bare manifest; and the engine answers ListWorkers but not Solve, so no
// buildctl could ever have built against it. What the lane needs is here:
// the Dockerfile built by the engine, six OCI labels and STELLAR_REVISION on
// the config, the image published under the g-pin as an OCI manifest, and the
// `builder` stage exported for the SBOM. Rob, 2026-09-12: "Dagger is the
// build engine, that was the original goal."
//
// THE TREE IS BOUND AT CONSTRUCTION, exactly as the atoms' is: nothing here
// takes a directory, so the lane builds the repository the caller stands in
// and no other.
type Image struct {
	// +private
	Source *dagger.Directory
	// +private
	Revision string
	// +private
	SourceURL string
	// +private
	Title string
	// +private
	BuildArgs []dagger.BuildArg
	// Dockerfile is the path of the Dockerfile inside Source; empty is the
	// tree's root Dockerfile. A base image in a repo of several builds from
	// bases/<lang>/Dockerfile against the whole tree, so a builder stage can
	// reach source beside it (the shared stellar-boot).
	// +private
	Dockerfile string
	// Created is the image's creation time, stamped ONCE, when the image is
	// bound. Container() used to stamp the clock on every call, so two
	// evaluations of one image — the tarball a scan read and the manifest a
	// publish pushed — were two configs with two digests, and the image the
	// registry held was not byte-for-byte the image that was scanned.
	// +private
	Created string
	// Base is the image the runtime stage is built on — the Dockerfile's last
	// FROM, as written — and BaseDigest the digest it pins, or empty. Read
	// once, when the image is bound, for the two base labels and for the
	// SBOM's link to the base's own document.
	// +private
	Base string
	// +private
	BaseDigest string
}

// The OCI labels the lane writes — the pair mold reads back (revision,
// source), the four the retired Tekton step wrote beside them, and the base
// pair (CA master-plan F14, contract line 4).
//
// THE BASE LABELS WERE ABSENT FROM EVERY STAR IMAGE, measured 2026-09-17 on
// athena and iris: six and seven labels, only revision, source and version.
// hephaestus mold's ContractDrift reads them as UNKNOWN when empty and, in its
// own words, "arms itself the moment build.yml starts emitting them"; the SBOM
// link needs the digest to name its target. After F17 the base is the FROM the
// Build stage just used; until then it is the FROM the Dockerfile pins.
const (
	labelRevision   = "org.opencontainers.image.revision"
	labelCreated    = "org.opencontainers.image.created"
	labelSource     = "org.opencontainers.image.source"
	labelVersion    = "org.opencontainers.image.version"
	labelTitle      = "org.opencontainers.image.title"
	labelURL        = "org.opencontainers.image.url"
	labelBaseName   = "org.opencontainers.image.base.name"
	labelBaseDigest = "org.opencontainers.image.base.digest"
)

// Image binds the build lane to the tip it is building.
func (m *FoundryTools) Image(
	ctx context.Context,
	// The source commit — org.opencontainers.image.revision, .version (its
	// first twelve) and the STELLAR_REVISION the star reports at boot.
	revision string,
	// The repository URL — org.opencontainers.image.source and .url.
	sourceURL string,
	// The star's name — org.opencontainers.image.title.
	title string,
	// Build args as KEY=VALUE, one per entry: the star's
	// .forgejo/build-args.env and the runner's own index (UV_INDEX_URL),
	// which a RUN sees only when it crosses the seam by name.
	// +optional
	buildArgs []string,
	// The Dockerfile's path inside the tree; empty builds the root Dockerfile.
	// +optional
	dockerfile string,
) (*Image, error) {
	if len(revision) < 12 {
		return nil, fmt.Errorf("image: the revision %q is too short for a version label", revision)
	}
	args, err := buildArgsOf(buildArgs)
	if err != nil {
		return nil, fmt.Errorf("image: %w", err)
	}
	img := &Image{Source: m.Source, Revision: revision, SourceURL: sourceURL, Title: title, BuildArgs: args,
		Dockerfile: dockerfile, Created: time.Now().UTC().Format(time.RFC3339)}
	// A Dockerfile that cannot be read, or names no base the lane can name (a
	// build argument, a stage that is not there), leaves the pair empty: the
	// build itself says what is wrong with such a Dockerfile, and a label the
	// lane guessed would be worse than none.
	body, _, err := fileIn(ctx, m.Source, img.dockerfilePath())
	if err != nil {
		return nil, fmt.Errorf("image: the Dockerfile could not be read: %w", err)
	}
	if base, digest, ok := buildlane.RuntimeBase(body); ok {
		img.Base, img.BaseDigest = base, digest
	}
	return img, nil
}

// dockerfilePath is the Dockerfile inside the tree: the bound path, or the
// root's.
func (i *Image) dockerfilePath() string {
	if i.Dockerfile == "" {
		return "Dockerfile"
	}
	return i.Dockerfile
}

// Container is the built image: the bound Dockerfile (the tree's root one by
// default), the labels and STELLAR_REVISION on its config. Publish, Tarball
// and Builder all hang off this one chain, so the engine builds the Dockerfile
// once, and every evaluation carries the same config.
//
// The base pair is written only when known: base.name is the reference
// without its digest (the OCI annotation's shape), base.digest the pin.
func (i *Image) Container() *dagger.Container {
	c := i.Source.DockerBuild(dagger.DirectoryDockerBuildOpts{BuildArgs: i.BuildArgs, Dockerfile: i.Dockerfile})
	c = c.
		WithLabel(labelRevision, i.Revision).
		WithLabel(labelCreated, i.Created).
		WithLabel(labelSource, i.SourceURL).
		WithLabel(labelVersion, i.Revision[:12]).
		WithLabel(labelTitle, i.Title).
		WithLabel(labelURL, i.SourceURL)
	c = withBaseLabels(c, i.Base, i.BaseDigest)
	return c.WithEnvVariable("STELLAR_REVISION", i.Revision)
}

// withBaseLabels writes the base pair on a container: base.name is the
// reference without its digest (the OCI annotation's shape), base.digest the
// pin. PROVENANCE, NOT A PERMIT INPUT (Rob, 2026-09-17): the first tip that
// carried the pair (nereus da34be5) was refused its permit by hephaestus
// mold's contractDriftBase, dormant until then, because every record in the
// fleet named a base its Dockerfile no longer pulled. The record no longer
// names one and the check is gone (hephaestus aaf3ce8, live 22:54Z), so the
// pair says what the image was built on and nothing compares it. Withheld
// between foundry-tools 51c71f6 and this landing.
func withBaseLabels(c *dagger.Container, base, digest string) *dagger.Container {
	if base != "" {
		name, _, _ := strings.Cut(base, "@")
		c = c.WithLabel(labelBaseName, name)
	}
	if digest != "" {
		c = c.WithLabel(labelBaseDigest, digest)
	}
	return c
}

// Publish pushes the image under ref — the g-pin — as an OCI manifest and
// answers the coordinate the registry minted, `<repo>@sha256:…`. The
// credential is the registry's publisher; the registry is read off the ref.
func (i *Image) Publish(
	ctx context.Context,
	// Where to push: registry.notusmi.com/rob/<star>:g<12>.
	ref string,
	// The registry account the lane publishes as.
	registryUser string,
	// That account's password.
	registryPassword *dagger.Secret,
) (string, error) {
	out, err := publish(ctx, i.Container(), ref, registryUser, registryPassword)
	if err != nil {
		return "", fmt.Errorf("image: %w", err)
	}
	return out, nil
}

// Tarball is the built image as an OCI layout tar — the pull-time run's
// artifact, which settles on the build and publishes nothing.
func (i *Image) Tarball() *dagger.File {
	return i.Container().AsTarball(dagger.ContainerAsTarballOpts{MediaTypes: dagger.ImageMediaTypesOcimediaTypes})
}

// Builder is the Dockerfile's `builder` stage as an OCI layout tar, for the
// SBOM to cover the bundled dependencies and not only the runtime image.
// The caller asks only when the Dockerfile names the stage.
func (i *Image) Builder() *dagger.File {
	return i.Source.
		DockerBuild(dagger.DirectoryDockerBuildOpts{BuildArgs: i.BuildArgs, Dockerfile: i.Dockerfile, Target: "builder"}).
		AsTarball(dagger.ContainerAsTarballOpts{MediaTypes: dagger.ImageMediaTypesOcimediaTypes})
}
