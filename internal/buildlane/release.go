package buildlane

import "strings"

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

// CopiesRelease answers whether a Dockerfile copies from ReleaseDir — whether
// it is asking for the Gate's artifact. A COPY's sources are the tokens after
// its flags (`COPY --chown=… release/ares /ares`, or the JSON form
// `COPY ["release/ares", "/ares"]`); a `--from=` copy takes its sources from
// another image or stage, never from the context, so it never asks.
func CopiesRelease(dockerfile string) bool {
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
		// The JSON form's first source: strip the bracket and the quote.
		src := strings.TrimLeft(args[0], "[\"")
		if strings.HasPrefix(src, ReleaseDir+"/") {
			return true
		}
	}
	return false
}
