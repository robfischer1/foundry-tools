package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/dagger"
)

// THE BAKE — any Dockerfile in the bound tree, built with named args and
// labels, published or exported. The image lane (image.go) is one shape of
// it, the star's; this is the general one the forge's seam drives for the
// base images and the blades: a context directory under the tree, a
// Dockerfile inside it, `KEY=VALUE` build args, `key=value` labels.
//
// ONE BUILDKIT (2026-09-12). forge-bases and forge-blades built through
// tools/forge/internal/buildkit — docker's argv translated to buildctl
// against a second daemon. The engine takes no buildctl build, so the seam
// translates to `dagger call bake …` instead, and this is what it calls.
type Bake struct {
	// +private
	Context *dagger.Directory
	// +private
	Dockerfile string
	// +private
	BuildArgs []dagger.BuildArg
	// Labels as key=value, validated at construction; the SDK's codegen
	// takes no array type on an object, so the pairs stay strings here.
	// +private
	Labels []string
}

// Bake binds a build to a context under the tree.
func (m *FoundryTools) Bake(
	// The build context, a directory under the tree; "" or "." is the root.
	// +optional
	context string,
	// The Dockerfile, relative to the context.
	// +optional
	// +default="Dockerfile"
	dockerfile string,
	// Build args as KEY=VALUE, one per entry.
	// +optional
	buildArgs []string,
	// OCI labels as key=value, one per entry, applied on the config.
	// +optional
	labels []string,
) (*Bake, error) {
	args, err := buildArgsOf(buildArgs)
	if err != nil {
		return nil, err
	}
	for _, kv := range labels {
		if k, _, ok := strings.Cut(kv, "="); !ok || k == "" {
			return nil, fmt.Errorf("bake: label %q is not key=value", kv)
		}
	}
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	dir := m.Source
	if context != "" && context != "." {
		dir = m.Source.Directory(context)
	}
	return &Bake{Context: dir, Dockerfile: dockerfile, BuildArgs: args, Labels: labels}, nil
}

// buildArgsOf reads KEY=VALUE entries into the SDK's shape.
func buildArgsOf(kvs []string) ([]dagger.BuildArg, error) {
	args := make([]dagger.BuildArg, 0, len(kvs))
	for _, kv := range kvs {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("build arg %q is not KEY=VALUE", kv)
		}
		args = append(args, dagger.BuildArg{Name: name, Value: value})
	}
	return args, nil
}

// Container is the built image with the labels on its config.
func (b *Bake) Container() *dagger.Container {
	c := b.Context.DockerBuild(dagger.DirectoryDockerBuildOpts{Dockerfile: b.Dockerfile, BuildArgs: b.BuildArgs})
	for _, kv := range b.Labels {
		k, v, _ := strings.Cut(kv, "=")
		c = c.WithLabel(k, v)
	}
	return c
}

// Publish pushes the image under ref as an OCI manifest and answers the
// coordinate the registry minted, `<repo>@sha256:…`. The registry is read
// off the ref; the credential is its publisher's.
func (b *Bake) Publish(
	ctx context.Context,
	// Where to push, `<registry>/<repo>:<tag>`.
	ref string,
	// The registry account to publish as.
	registryUser string,
	// That account's password.
	registryPassword *dagger.Secret,
) (string, error) {
	return publish(ctx, b.Container(), ref, registryUser, registryPassword)
}

// Tarball is the built image as an OCI layout tar — for a scanner, or for a
// dry run that publishes nothing.
func (b *Bake) Tarball() *dagger.File {
	return b.Container().AsTarball(dagger.ContainerAsTarballOpts{MediaTypes: dagger.ImageMediaTypesOcimediaTypes})
}

// publish is the one push: registry auth read off the ref, OCI media types.
func publish(ctx context.Context, c *dagger.Container, ref, user string, password *dagger.Secret) (string, error) {
	host, _, ok := strings.Cut(ref, "/")
	if !ok || host == "" {
		return "", fmt.Errorf("%q names no registry host", ref)
	}
	return c.
		WithRegistryAuth(host, user, password).
		Publish(ctx, ref, dagger.ContainerPublishOpts{MediaTypes: dagger.ImageMediaTypesOcimediaTypes})
}
