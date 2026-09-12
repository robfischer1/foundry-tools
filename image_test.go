package main

import (
	"context"
	"strings"
	"testing"
)

const imageRevision = "0123456789abcdef0123456789abcdef01234567"

func newImage(t *testing.T, buildArgs ...string) *Image {
	t.Helper()
	engine.reset()
	engine.withTree(map[string]string{"Dockerfile": "FROM scratch AS builder\nRUN true\nFROM scratch\n"})
	img, err := (&FoundryTools{Source: dag.Directory()}).Image(imageRevision, "https://forgejo.notusmi.com/rob/x", "x", buildArgs)
	if err != nil {
		t.Fatalf("image: %v", err)
	}
	return img
}

// The published image is the Dockerfile built by the engine with the build
// args crossing the seam by name, six OCI labels and STELLAR_REVISION on its
// config, pushed as an OCI manifest under the registry's publisher.
func TestTheImageLaneBuildsLabelsAndPublishes(t *testing.T) {
	img := newImage(t, "FOO=bar", "UV_INDEX_URL=https://nexus.example/simple", "EQ=a=b")
	engine.script(script{leaf: "publish", value: "registry.notusmi.com/rob/x@sha256:" + strings.Repeat("b", 64)})
	out, err := img.Publish(context.Background(), "registry.notusmi.com/rob/x:g0123456789ab", "publisher", dag.SetSecret("registry-password", "hunter2"))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if out != "registry.notusmi.com/rob/x@sha256:"+strings.Repeat("b", 64) {
		t.Fatalf("publish answered %q, not the registry's coordinate", out)
	}
	chain := engine.chain("publish(")
	wantCalls(t, chain,
		[]string{"dockerBuild", `"FOO"`, `"bar"`, `"UV_INDEX_URL"`, `"https://nexus.example/simple"`, `"EQ"`, `"a=b"`},
		[]string{"withLabel", labelRevision, imageRevision},
		[]string{"withLabel", labelVersion, `"0123456789ab"`},
		[]string{"withLabel", labelSource, "https://forgejo.notusmi.com/rob/x"},
		[]string{"withLabel", labelURL, "https://forgejo.notusmi.com/rob/x"},
		[]string{"withLabel", labelTitle, `"x"`},
		[]string{"withLabel", labelCreated},
		[]string{"withEnvVariable", `"STELLAR_REVISION"`, imageRevision},
		[]string{"withRegistryAuth", `"registry.notusmi.com"`, `"publisher"`},
		[]string{"publish", `"registry.notusmi.com/rob/x:g0123456789ab"`, "OCIMediaTypes"},
	)
	if strings.Contains(chain, "target:") {
		t.Fatalf("the image build named a target:\n%s", chain)
	}
}

// The builder stage is its own build — target=builder — exported as an OCI
// layout tar for the SBOM; the pull-time tarball is the image itself.
func TestTheImageLaneExportsTheBuilderStageAndTheTarball(t *testing.T) {
	img := newImage(t)
	if _, err := img.Builder().Export(context.Background(), "/tmp/builder.tar"); err != nil {
		t.Fatalf("builder: %v", err)
	}
	wantCalls(t, engine.chain("export("),
		[]string{"dockerBuild", `target:"builder"`},
		[]string{"asTarball", "OCIMediaTypes"},
		[]string{"export", `"/tmp/builder.tar"`},
	)
	if _, err := img.Tarball().Export(context.Background(), "/tmp/final.tar"); err != nil {
		t.Fatalf("tarball: %v", err)
	}
	chain := engine.chain("export(", "/tmp/final.tar")
	wantCalls(t, chain,
		[]string{"withEnvVariable", `"STELLAR_REVISION"`},
		[]string{"asTarball", "OCIMediaTypes"},
	)
	if strings.Contains(chain, "target:") || strings.Contains(chain, "publish(") {
		t.Fatalf("the pull-time tarball must be the image, unpublished:\n%s", chain)
	}
}

// What the lane refuses before it asks the engine for anything.
func TestTheImageLaneRefusesWhatItCannotLabelOrPush(t *testing.T) {
	engine.reset()
	m := &FoundryTools{Source: dag.Directory()}
	if _, err := m.Image("abc", "https://x", "x", nil); err == nil || !strings.Contains(err.Error(), "too short") {
		t.Fatalf("a short revision: %v", err)
	}
	if _, err := m.Image(imageRevision, "https://x", "x", []string{"NOVALUE"}); err == nil || !strings.Contains(err.Error(), "KEY=VALUE") {
		t.Fatalf("a build arg without a value: %v", err)
	}
	if _, err := m.Image(imageRevision, "https://x", "x", []string{"=v"}); err == nil {
		t.Fatal("a build arg without a name was accepted")
	}
	// Twelve characters is the version label whole, and enough.
	if _, err := m.Image("0123456789ab", "https://x", "x", nil); err != nil {
		t.Fatalf("a twelve-character revision: %v", err)
	}
	img, err := m.Image(imageRevision, "https://x", "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := img.Publish(context.Background(), "x:latest", "u", dag.SetSecret("p", "s")); err == nil || !strings.Contains(err.Error(), "registry host") {
		t.Fatalf("a ref with no host: %v", err)
	}
	if n := len(engine.chains()); n != 0 {
		t.Fatalf("%d queries reached the engine for refused inputs", n)
	}
}
