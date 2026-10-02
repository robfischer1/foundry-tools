package main

import (
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// toolOnAlpine runs a distroless tool's binary on checks.ImageAlpine, with the
// binary as the entrypoint so a caller's WithExec args read exactly as they
// did against the tool's own image.
//
// WHY NOT THE TOOL'S IMAGE. The engine installs its cache CA into every
// container, and Dagger's installer needs a distro it recognises; on syft's
// scratch image and cosign's ko image it failed with "invalid argument" on
// every run and the tool ran without the CA (checks.ImageAlpine says what was
// measured).
func toolOnAlpine(image, path, name string) *dagger.Container {
	bin := "/usr/local/bin/" + name
	return dag.Container().From(checks.ImageAlpine).
		WithFile(bin, dag.Container().From(image).File(path), dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithEntrypoint([]string{bin})
}

// cosignIn is cosign on Alpine as the user its own image runs as (65532), so
// the secrets the lanes mount for that owner read as they did.
func cosignIn() *dagger.Container {
	return toolOnAlpine(checks.ImageCosign, "/ko-app/cosign", "cosign").WithUser("65532:65532")
}
