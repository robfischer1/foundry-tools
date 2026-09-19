package buildlane

import "testing"

// A Dockerfile that copies from release/ is asking for the Gate's artifact;
// one that compiles itself, or copies release-looking paths out of another
// stage, is not.
func TestCopiesReleaseReadsTheDockerfilesAsk(t *testing.T) {
	for _, c := range []struct {
		name       string
		dockerfile string
		want       bool
	}{
		{"the three-line shape", "FROM registry.notusmi.com/foundry/base-images/go:stable@sha256:abc\nCOPY release/ares /ares\nCMD [\"/ares\"]\n", true},
		{"a nested binary", "FROM x\nCOPY release/bin/ares /ares\n", true},
		{"flags before the source", "FROM x\nCOPY --chown=65532:65532 --chmod=755 release/ares /ares\n", true},
		{"the JSON form", "FROM x\nCOPY [\"release/ares\", \"/ares\"]\n", true},
		{"lower case and indented", "FROM x\n  copy release/ares /ares\n", true},
		{"several binaries", "FROM x\nCOPY release/blade-runner /blade-runner\nCOPY release/blade-controller /blade-controller\n", true},
		{"its own build stage", "FROM golang AS build\nRUN go build -o /out/ares ./cmd/ares\nFROM x\nCOPY --from=build /out/ares /ares\n", false},
		{"a --from copy of a release-looking path", "FROM x\nCOPY --from=build release/ares /ares\n", false},
		{"a directory that merely starts with the word", "FROM x\nCOPY released/notes /notes\n", false},
		{"the word in a comment", "FROM x\n# COPY release/ares /ares — not yet\nCOPY --from=build /out/ares /ares\n", false},
		{"ADD is not COPY", "FROM x\nADD release/ares /ares\n", false},
		{"a COPY that is all flags names no source", "FROM x\nCOPY --chown=65532:65532\n", false},
		{"a bare COPY", "FROM x\nCOPY\n", false},
		{"a COPY with the release source alone", "FROM x\nCOPY release/ares\n", true},
		{"nothing", "", false},
	} {
		if got := CopiesRelease(c.dockerfile); got != c.want {
			t.Errorf("%s: CopiesRelease = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBaseToolchainIsTheFleetBaseRepositoryAlone(t *testing.T) {
	for ref, want := range map[string]string{
		"registry.notusmi.com/foundry/base-images/python:stable@sha256:d7ea337fb2dd": "python",
		"registry.notusmi.com/foundry/base-images/bun:stable":                        "bun",
		"registry.notusmi.com/foundry/base-images/go@sha256:ec818e859cb2":            "go",
		"registry.notusmi.com/foundry/base-images/rust":                              "rust",
		// Not a fleet base: another registry, another namespace, a base
		// image the fleet does not publish, a nested path, nothing at all.
		"docker.io/library/python:3.14-slim":                   "",
		"registry.notusmi.com/rob/stellar_core:python-runtime": "",
		"registry.notusmi.com/foundry/base-images/node:stable": "",
		"registry.notusmi.com/foundry/base-images/go/extra":    "",
		"": "",
	} {
		if got := BaseToolchain(ref); got != want {
			t.Errorf("BaseToolchain(%q) = %q, want %q", ref, got, want)
		}
	}
}
