package buildlane

import (
	"slices"
	"strings"
)

// THE IMAGE IS THE BASE PLUS THE ARTIFACT (CA master-plan F14/F17; Rob:
// "Build - Copy the binary from the previous complex run"; "FROM base +
// COPY"). The Gate compiled the star's release once (foundry-tools Release,
// the go:release atom's own exec, so the engine answers it from cache), and
// the Dockerfile's job shrinks to naming the base and copying the artifact in:
//
//	FROM registry.notusmi.com/foundry/base-images/go:stable@sha256:…
//	COPY release/<star> /<star>
//	CMD ["/<star>"]
//
// THE DOCKERFILE SAYS WHETHER IT WANTS THE ARTIFACT, by copying from
// ReleaseDir. That is the whole contract between the two: a Dockerfile that
// still carries its own build stage copies nothing from release/, gets no
// artifact staged, and builds as it always did — which is what lets the fleet
// flip one star at a time instead of all at once. A Dockerfile that copies
// from release/ is asking for the Gate's build, and the lane stages it at
// that path in the build context before the Dockerfile runs. Nothing else
// reads or writes the directory: it is never in the tree, and the template's
// .gitignore refuses it.

// ReleaseDir is the path in the build context where the lane stages the
// Gate's release artifact — the directory a Dockerfile COPYs from.
const ReleaseDir = "release"

// releaseSources walks a Dockerfile's COPY lines the way the lane stages them
// and answers, per line, the FIRST source that reads from the build context:
// the tokens after the flags (`COPY --chown=… release/ares /ares`, or the JSON
// form `COPY ["release/ares", "/ares"]`). A `--from=` copy takes its sources
// from another image or stage, never from the context, so it contributes
// nothing. One reading, two questions: CopiesRelease asks whether any of them
// is under ReleaseDir, ReleaseCopies asks which.
func releaseSources(dockerfile string) []string {
	var out []string
	for _, line := range strings.Split(dockerfile, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.EqualFold(fields[0], "COPY") {
			continue
		}
		// The flags, then the sources; a COPY that is all flags names none.
		args := fields[1:]
		fromContext := true
		for len(args) != 0 && strings.HasPrefix(args[0], "--") {
			if strings.HasPrefix(strings.ToLower(args[0]), "--from=") {
				fromContext = false
			}
			args = args[1:]
		}
		if !fromContext || len(args) == 0 {
			continue
		}
		// The JSON form's first source: strip the bracket, the quote and the
		// comma that separates it from the destination.
		out = append(out, strings.TrimRight(strings.TrimLeft(args[0], "[\""), "\","))
	}
	return out
}

// CopiesRelease answers whether a Dockerfile copies from ReleaseDir — whether
// it is asking for the Gate's artifact.
func CopiesRelease(dockerfile string) bool {
	for _, src := range releaseSources(dockerfile) {
		if strings.HasPrefix(src, ReleaseDir+"/") {
			return true
		}
	}
	return false
}

// ReleaseCopies names what a Dockerfile copies out of ReleaseDir, in file
// order, each once: `COPY release/clio-consume /clio-consume` names
// clio-consume. These ARE the image's binaries: the Dockerfile says which
// files the image carries, so the release build derives its list from it and
// nothing is declared twice. Read with exactly CopiesRelease's rules (flags
// skipped, `--from=` copies ignored, only a line's first source). A source
// that is the directory itself or reaches below a file name (release/a/b)
// names no binary and is left out — a Go or Rust star's COPY is one file.
func ReleaseCopies(dockerfile string) []string {
	var out []string
	for _, src := range releaseSources(dockerfile) {
		name, ok := strings.CutPrefix(src, ReleaseDir+"/")
		if !ok || name == "" || strings.Contains(name, "/") || slices.Contains(out, name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// FleetBases is where the fleet's runtime bases live: one repository per
// toolchain under foundry/base-images (base-images README), each the base a
// star's Dockerfile FROMs as its runtime stage.
const FleetBases = "registry.notusmi.com/foundry/base-images/"

// BaseToolchain names the fleet base a runtime reference is on — "go",
// "rust", "bun" or "python" — or "" for any image that is not one of them.
// The tag and the digest do not matter: a star pins
// foundry/base-images/python:stable@sha256:…, and it is the repository that
// says which toolchain the image's release is built with.
//
// THE IMAGE'S BASE PICKS THE RELEASE BUILD, NOT THE TREE'S MANIFESTS. A tree
// can declare two toolchains — mnemosyne carries a go.mod and the pyproject
// its Go port left behind — and only one of them made the image. The base
// the Dockerfile's runtime stage is FROM is the one fact that says which.
func BaseToolchain(ref string) string {
	name, ok := strings.CutPrefix(RepoOf(ref), FleetBases)
	if !ok {
		return ""
	}
	switch name {
	case "go", "rust", "bun", "python":
		return name
	}
	return ""
}
