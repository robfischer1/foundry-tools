package main

import (
	"context"
	"strings"
	"testing"
)

// A bake is any Dockerfile under the tree: the context directory, the file
// inside it, the args by name, the labels on the config — published as an
// OCI manifest under the registry's publisher, or exported as a layout.
func TestTheBakeBuildsAContextUnderTheTreeAndPublishes(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"bases/go-ci/Dockerfile.ci": "FROM scratch\n"})
	m := &FoundryTools{Source: dag.Directory()}
	b, err := m.Bake("bases/go-ci", "Dockerfile.ci", []string{"GO_VERSION=1.26.6"}, []string{"org.opencontainers.image.title=stellar-base-go-ci", "org.opencontainers.image.revision=abc"})
	if err != nil {
		t.Fatalf("bake: %v", err)
	}
	engine.script(script{leaf: "publish", value: "registry.notusmi.com/rob/stellar_core@sha256:" + strings.Repeat("c", 64)})
	out, err := b.Publish(context.Background(), "registry.notusmi.com/rob/stellar_core:go-ci", "publisher", dag.SetSecret("registry-password", "hunter2"))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !strings.HasSuffix(out, strings.Repeat("c", 64)) {
		t.Fatalf("publish answered %q", out)
	}
	chain := engine.chain("publish(")
	wantCalls(t, chain,
		[]string{"directory", `"bases/go-ci"`},
		[]string{"dockerBuild", `"Dockerfile.ci"`, `"GO_VERSION"`, `"1.26.6"`},
		[]string{"withLabel", `"org.opencontainers.image.title"`, `"stellar-base-go-ci"`},
		[]string{"withLabel", `"org.opencontainers.image.revision"`, `"abc"`},
		[]string{"withRegistryAuth", `"registry.notusmi.com"`, `"publisher"`},
		[]string{"publish", `"registry.notusmi.com/rob/stellar_core:go-ci"`, "OCIMediaTypes"},
	)
	if strings.Contains(chain, "STELLAR_REVISION") || strings.Contains(chain, "created") {
		t.Fatalf("a bake carries only the labels it was given:\n%s", chain)
	}
}

// The root is the context when none is named; the Dockerfile defaults; the
// tarball is the image, unpublished.
func TestTheBakeDefaultsToTheRootAndExportsATarball(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"Dockerfile": "FROM scratch\n"})
	m := &FoundryTools{Source: dag.Directory()}
	b, err := m.Bake("", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Tarball().Export(context.Background(), "/tmp/bake.tar"); err != nil {
		t.Fatalf("tarball: %v", err)
	}
	chain := engine.chain("export(")
	wantCalls(t, chain,
		[]string{"dockerBuild", `"Dockerfile"`},
		[]string{"asTarball", "OCIMediaTypes"},
		[]string{"export", `"/tmp/bake.tar"`},
	)
	if strings.Contains(chain, "directory(") || strings.Contains(chain, "publish(") || strings.Contains(chain, "withLabel(") {
		t.Fatalf("a root bake with no labels names no subdirectory, no label and no push:\n%s", chain)
	}
	b2, err := m.Bake(".", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b2.Tarball().Export(context.Background(), "/tmp/bake2.tar"); err != nil {
		t.Fatal(err)
	}
	if c := engine.chain("export(", "/tmp/bake2.tar"); strings.Contains(c, "directory(") {
		t.Fatalf("\".\" is the root too:\n%s", c)
	}
}

// What the bake refuses before it asks the engine for anything.
func TestTheBakeRefusesMalformedArgsLabelsAndRefs(t *testing.T) {
	engine.reset()
	m := &FoundryTools{Source: dag.Directory()}
	if _, err := m.Bake("", "", []string{"NOVALUE"}, nil); err == nil || !strings.Contains(err.Error(), "KEY=VALUE") {
		t.Fatalf("a build arg without a value: %v", err)
	}
	if _, err := m.Bake("", "", nil, []string{"novalue"}); err == nil || !strings.Contains(err.Error(), "key=value") {
		t.Fatalf("a label without a value: %v", err)
	}
	if _, err := m.Bake("", "", nil, []string{"=v"}); err == nil {
		t.Fatal("a label without a key was accepted")
	}
	b, err := m.Bake("", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Publish(context.Background(), "x:latest", "u", dag.SetSecret("p", "s")); err == nil || !strings.Contains(err.Error(), "registry host") {
		t.Fatalf("a ref with no host: %v", err)
	}
	if n := len(engine.chains()); n != 0 {
		t.Fatalf("%d queries reached the engine for refused inputs", n)
	}
}
