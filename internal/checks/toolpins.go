package checks

// THE ATOMS BINARY'S TOOLS CONTAINER (atoms_tools.go in the module). The
// binary execs its tools from PATH, so the container it runs in is built from
// a base and these pins, in the Dagger pipeline: nothing is published, and a
// pin that moves rebuilds its layer and the ones above it.

// ImageTools is the base of that container: Debian's slim image, by digest.
// It is the INDEX digest of bookworm-slim (read from the registry 2026-10-08),
// so the engine picks the platform and the pin does not move with a rebuild
// of one architecture. Plain bookworm-slim ships neither git nor
// ca-certificates; the container's first layer installs both.
const ImageTools = "docker.io/library/debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587"

// ShellcheckVersion / ShellcheckURL fetch the shellcheck binary ops:shell runs.
// The chain ran `uvx --from shellcheck-py shellcheck` with NO version pin;
// shellcheck-py 0.11.0.1, the release that was current on 2026-10-08, wraps
// ShellCheck 0.11.0, so this pin is the same program the chain resolved that
// day, and the one the chain will keep resolving only until PyPI moves.
//
// Spelled in full, as every pin in this package is: a `+` in a const block is
// a declaration no coverage profile can mark.
const (
	// renovate: datasource=github-releases depName=koalaman/shellcheck extractVersion=^v(?<version>.*)$
	ShellcheckVersion = "0.11.0"
	ShellcheckURL     = "https://github.com/koalaman/shellcheck/releases/download/v0.11.0/shellcheck-v0.11.0.linux.x86_64.tar.xz"
	// ShellcheckMember is the binary inside that tarball.
	ShellcheckMember = "shellcheck-v0.11.0/shellcheck"
	// ShellcheckPyVersion is the shellcheck-py wheel that wraps ShellcheckVersion:
	// the CHAIN of ops:shell still provisions shellcheck through uvx, and an
	// unpinned `uvx --from shellcheck-py` is a different program the day PyPI
	// moves. Pinned here so the chain and the binary grade with one shellcheck.
	// renovate: datasource=pypi depName=shellcheck-py
	ShellcheckPyVersion = "0.11.0.1"
)

// ToolSHA256 is the checksum of every URL the tools container fetches, hex.
// A pin is a version AND a checksum: the engine's HTTP fetch is addressed by
// URL, so without this a release asset replaced upstream would be installed
// silently. Where the publisher states a checksum (kubectl, opa, compose,
// chezmoi, hadolint's recorded 2026-09-13 measurement) these are equal to it;
// the others were computed from the asset on 2026-10-08. A version bump that
// does not update its line fails TestEveryToolPinHasAChecksum, and a mismatch
// at build time leaves the tool out of the container (its atom settles 2).
var ToolSHA256 = map[string]string{
	OpaURL:        "668506eb17a2eaa1fce6cc0d1f42ef85125d4ac5bda5fc74d1152d0c77145031",
	OpengrepURL:   "a66aa3278457f02b287b985a45b6762aebcaba5000f2689245fd1ed86d1456c7",
	HadolintURL:   "c7187db94eeeeca956519a6af171adc31453941a1e777961f6e680f697c8c507",
	KubectlURL:    "65691ff77eb6fa44c908b77a1082c9f092c3b9733b5cefabec0d1104890e21a8",
	ChezmoiURL:    "4b42a0fb0dec69f37964c6701f89daa6bf6393aaeb92bbcbc8f3ee9f08a49bcd",
	ComposeURL:    "40343e21ca777173e69cff5dbafeb37c6f81f3b0d57d9e597f036e95eb63e76a",
	WasmToolsURL:  "ad62b2176037e93e1348cb65d6212d128ca9f097b63d155569f25215818ff7b1",
	JustURL:       "4a5cc2f53e6f0f8c59092a6cc38291eb729d46a7dd95d3ae582008881b84931d",
	ShellcheckURL: "8c3be12b05d5c177a04c29e3c78ce89ac86f1595681cab149b65b97c4e227198",
}
