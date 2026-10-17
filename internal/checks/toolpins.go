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

// PythonStandaloneURL is the CPython the tools container's python layer unpacks:
// a python-build-standalone install_only_stripped release, the same build
// `uv python install 3.14` resolves, fetched by the engine like every tool here
// and checked against ToolSHA256 (the publisher's own SHA256SUMS line, equal to
// the asset's sum read 2026-10-08). The tools container's uv is told never to
// download an interpreter of its own.
const PythonStandaloneURL = "https://github.com/astral-sh/python-build-standalone/releases/download/20261013/cpython-3.14.8%2B20261013-x86_64-unknown-linux-gnu-install_only_stripped.tar.gz"

// ToolSHA256 is the checksum of every pinned release asset the module fetches
// (the tools container's, the lanes', the cargo tool tarballs', oras), hex. Every
// one is served mirror-first (ToolSources), so every one is seeded
// (`just mirror-seed <url> <sum>`).
// A pin is a version AND a checksum: the engine's HTTP fetch is addressed by
// URL, so without this a release asset replaced upstream would be installed
// silently. Where the publisher states a checksum (kubectl, opa, compose,
// chezmoi, hadolint's recorded 2026-09-13 measurement) these are equal to it;
// the others were computed from the asset on 2026-10-08 (WacURL's, the bare
// binary rust:wit-compose places, on 2026-10-12, when the wit lane began
// verifying what it fetches). A version bump that
// does not update its line fails TestEveryToolPinHasAChecksum, and a mismatch
// at build time leaves the tool out of the container (its atom settles 2).
var ToolSHA256 = map[string]string{
	OpaURL:           "830ab11f61ffec760b5e19db0d7ad02bf037e13f5ef4d665faeaf17632177527",
	OpengrepURL:      "dbbe3c2058dbe600c449a8ba22ea79a1643a6660658400090d0323a8d0d31147",
	HadolintURL:      "c7187db94eeeeca956519a6af171adc31453941a1e777961f6e680f697c8c507",
	KubectlURL:       "65691ff77eb6fa44c908b77a1082c9f092c3b9733b5cefabec0d1104890e21a8",
	ChezmoiURL:       "fbf312e45c5c1f629e1d994c201cdd666198bd556af38512f4432ae6d3952521",
	ComposeURL:       "40343e21ca777173e69cff5dbafeb37c6f81f3b0d57d9e597f036e95eb63e76a",
	WasmToolsURL:     "655b8df67d54c3ef9b52fc237c4e94bbfbb5f5f43825245cb0940a7002a9ed65",
	WasmTools1250URL: "b746c34e7c4162b8812eb29397ebe076834e496a8c46fe68d793379a2741eb50",
	NodeRegenURL:     "6e1db87ef58b8819e5d5402eff1536491b18edd8eb7bee5ef7897876e88dc5ff",
	WacURL:           "7cd490edbd452221ef89e6750fc43eacc9b71b1242095eb577cb9a3bf0d3beda",
	JustURL:          "409044617b6f06517c1035e55ca38c8c856ff8b650e41bc2aa1ef82d5903a23e",
	ShellcheckURL:    "8c3be12b05d5c177a04c29e3c78ce89ac86f1595681cab149b65b97c4e227198",

	PythonStandaloneURL: "d8faa1b588506b9e3e5a3f57807bb056d534a1641a6b10d15b6ea3f0b413b960",

	// The rust lane's release binaries (images.go has why these two and not
	// cargo-mutants), computed from the assets 2026-10-09.
	CargoAuditURL:   "7fb9497f8594b389e5fce5ef9b92db08432996895b2e0c5a0167a69ed445c428",
	CargoNextestURL: "32aa82416099eb12fffae9cf1a279ad201fecbd3f74826c613e32e9006b29867",
	CargoMutantsURL: "dfe6dc37d0342c891d2829b5a695aa57c2d0edecef7e7d0399a30cc6e206411e",

	// The rust containers' compile cache wrapper (images.go SccacheURL).
	SccacheURL: "45f1447fbe231e3037bde351ef70677dd212216c8d62ae7ca409fecc4d6acc89",

	// The rust atoms' linker (images.go MoldURL): upstream's tarball, equal to the
	// asset downloaded 2026-10-17.
	MoldURL: "7524fe67f917d5287b2f7a7d6401e2625fa3f5f84bc6b8d9b487d6cb08876488",

	// The rust containers' clippy and rustfmt (images.go RustClippyURL).
	RustClippyURL:      "b3fe2a650456f055f4163e7de8c230609a5daf72c204a4a64cc6b51b5eb72e3e",
	RustRustfmtURL:     "7b5d4b02c8d0f7c3be64d3bc2e0738ccb06dc0492f81a93c4fcb223ff93f8b38",
	RustWasmClippyURL:  "0e1db6adef940dd21896dbd7a051e25056df2fede90591c583552f94e913af22",
	RustWasmRustfmtURL: "61407015edaeafa9b61b3ffefb5077624cd4017ac50be05e246bcadafbb56d99",

	// The oras client the build, bundle, cast and govern lanes push with: the
	// publisher's own oras_1.3.4_checksums.txt line, equal to the asset's sum
	// read 2026-10-12, when every fetch began going through the mirror (which
	// serves a blob only by the digest of its bytes, so it needs a pin).
	OrasURL: "f27adb935022d94df8dc77719c322dda592c78a0d57a6f7dcdd8d900b248c454",
}

// THE PYTHON AND RUST LANES' RUN-TIME INSTALLS, PINNED. These resolved at run
// time (`uv run --with pip-audit`, `uvx ruff@...`, `uv python install 3.14`):
// whatever PyPI or uv's list held that minute, fetched again every run. Each is
// now an exact version with a Renovate marker, so the bump is a reviewed pull
// and the verdict is a function of the pin.
//
// Spelled as bare `<Name>Version` strings in a file renovate-config's
// foundry-tools manager reads (internal/checks/[a-z]+.go), and held by
// TestEveryPinnedVersionNamesItsRenovateSource.
const (
	// RuffVersion stays at 0.16.3, the pin the ruff atoms already held: a newer
	// ruff re-judges the same tree, which is a decision, not a pin.
	// renovate: datasource=pypi depName=ruff
	RuffVersion = "0.16.3"
	// renovate: datasource=pypi depName=cosmic-ray
	CosmicRayVersion = "8.7.0"
	// renovate: datasource=pypi depName=pip-audit
	PipAuditVersion = "2.10.1"
	// renovate: datasource=pypi depName=coverage
	CoverageVersion = "7.16.2"
	// MypyVersion and PytestVersion pin the tree with no pyproject only: a
	// project's own mypy and pytest are the lock's.
	// renovate: datasource=pypi depName=mypy
	MypyVersion = "2.4.0"
	// renovate: datasource=pypi depName=pytest
	PytestVersion = "9.1.1"
	// FleetPythonVersion is the exact interpreter the rust lane installs with uv
	// (FleetPython is its minor line); `uv python install 3.14` took whatever
	// patch uv's list held when the layer was built.
	// renovate: datasource=python-version depName=python versioning=pep440
	FleetPythonVersion = "3.14.8"
)

// UVTool is a python program the lane installs ONCE, into its own venv, in a
// provision layer the engine caches by content: a `uv tool install` at an exact
// version, then a probe that proves it runs.
type UVTool struct {
	Name, Version string
	Probe         []string
}

// Spec is the requirement uv is handed: always `name==version`.
func (t UVTool) Spec() string { return t.Name + "==" + t.Version }

// PythonLaneTools are the programs the python lane installs as tools, in layer
// order. ONLY programs that do not need the repository's environment belong
// here: pip-audit audits the environment it runs in and coverage is imported by
// the suite's own interpreter, so those stay `uv run --with <name>==<pin>`
// (PythonWith), where the project is the environment.
var PythonLaneTools = []UVTool{
	{Name: "ruff", Version: RuffVersion, Probe: []string{"ruff", "--version"}},
	{Name: "cosmic-ray", Version: CosmicRayVersion, Probe: []string{"cosmic-ray", "--version"}},
}

// PythonWith is the exact requirement for a `uv run --with` that has to resolve
// into the repository's environment, for the tools named here.
func PythonWith(name string) string {
	switch name {
	case "pip-audit":
		return "pip-audit==" + PipAuditVersion
	case "coverage":
		return "coverage==" + CoverageVersion
	case "mypy":
		return "mypy==" + MypyVersion
	case "pytest":
		return "pytest==" + PytestVersion
	}
	return name
}
