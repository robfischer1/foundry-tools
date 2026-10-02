package main

import (
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// toolOnFleetBase runs a distroless tool's binary on the fleet's bookworm-slim
// base (checks.ImageFleet), with the binary as the entrypoint so a caller's
// WithExec args read exactly as they did against the tool's own image.
//
// WHY NOT THE TOOL'S IMAGE (2026-10-02). The engine installs its cache CA into
// every container, and Dagger's installer needs a distro it can work: syft's
// image is FROM scratch and cosign's is a ko image, so every run logged
// "failed to create cacerts installer ... invalid argument" plus the matching
// "uninstall CA certs" cleanup failure, and ran WITHOUT the CA — 351 failing
// spans in 24h on the live engines. On the fleet base (the constellation's
// bookworm-slim, carrying ca-certificates and update-ca-certificates) both
// tools ran on a CA-mounted engine with cache-ca.crt in the trust store and no
// installer or uninstall error. Plain debian:bookworm-slim is not enough: it
// ships neither update-ca-certificates nor /etc/ssl/certs.
func toolOnFleetBase(image, path, name string) *dagger.Container {
	bin := "/usr/local/bin/" + name
	return dag.Container().From(checks.ImageFleet).
		WithFile(bin, dag.Container().From(image).File(path), dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithEntrypoint([]string{bin})
}

// cosignIn is cosign on the fleet base as the user its own image runs as
// (65532), so the secrets the lanes mount for that owner read as they did.
func cosignIn() *dagger.Container {
	return toolOnFleetBase(checks.ImageCosign, "/ko-app/cosign", "cosign").WithUser("65532:65532")
}
