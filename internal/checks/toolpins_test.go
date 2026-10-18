package checks

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Every URL the tools container fetches has a checksum, and it is a SHA-256.
func TestEveryToolPinHasAChecksum(t *testing.T) {
	hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, url := range []string{OpaURL, OpengrepURL, HadolintURL, KubectlURL, ChezmoiURL, ComposeURL, WasmToolsURL, WasmTools1250URL, NodeRegenURL, RustStdWasmURL, RustStdURL, WacURL, JustURL, ShellcheckURL, PythonStandaloneURL, PythonStandalone315URL, CargoAuditURL, CargoNextestURL, CargoMutantsURL, OrasURL, SccacheURL, RustClippyURL, RustRustfmtURL, RustWasmClippyURL, RustWasmRustfmtURL, MoldURL, GofmtURL, RustDist1971RustcURL, RustDist1971CargoURL, RustDist1971StdURL, RustDist1971StdWasmURL, RustDist1990RustcURL, RustDist1990CargoURL, RustDist1990StdURL, RustDist1990StdWasmURL} {
		sum, ok := ToolSHA256[url]
		if !ok || !hex.MatchString(sum) {
			t.Errorf("%s has no SHA-256 in ToolSHA256 (got %q)", url, sum)
		}
	}
	if len(ToolSHA256) != 35 {
		t.Errorf("ToolSHA256 holds %d entries; a stale line for a URL nothing fetches is a pin nobody checks", len(ToolSHA256))
	}
}

// The rust lane's cargo tool tarballs name the version their crate is pinned
// at, and the member is the subcommand cargo will exec: a renovate bump of the
// version that leaves the URL (and so the checksum) behind fails here.
func TestCargoToolPinsAgree(t *testing.T) {
	for _, tc := range []struct{ url, member, version, prefix, bin string }{
		{CargoAuditURL, CargoAuditMember, CargoAuditVersion, "https://github.com/rustsec/rustsec/releases/download/cargo-audit/v", "cargo-audit"},
		{CargoNextestURL, CargoNextestMember, CargoNextestVersion, "https://github.com/nextest-rs/nextest/releases/download/cargo-nextest-", "cargo-nextest"},
	} {
		// The tag AND the asset name carry the version.
		if !strings.HasPrefix(tc.url, tc.prefix+tc.version+"/") || strings.Count(tc.url, tc.version) != 2 {
			t.Errorf("%s does not carry version %s", tc.url, tc.version)
		}
		if !strings.HasSuffix(tc.member, "/"+tc.bin) && tc.member != tc.bin {
			t.Errorf("member %q is not %s", tc.member, tc.bin)
		}
	}
	// The libc each asset was measured to run on (images.go): bookworm's glibc
	// refuses the gnu cargo-audit, so it is the static musl build.
	if !strings.Contains(CargoAuditURL, "x86_64-unknown-linux-musl") || !strings.Contains(CargoNextestURL, "x86_64-unknown-linux-gnu") {
		t.Errorf("the cargo tool assets moved off the builds measured to run in ImageRust: %s, %s", CargoAuditURL, CargoNextestURL)
	}
}

// The sccache tarball names the pinned version in its tag and its asset, and
// the member is the binary inside it. The asset is the static musl build.
func TestSccachePinAgrees(t *testing.T) {
	want := "https://github.com/mozilla/sccache/releases/download/v" + SccacheVersion + "/sccache-v" + SccacheVersion + "-x86_64-unknown-linux-musl.tar.gz"
	if SccacheURL != want {
		t.Errorf("SccacheURL %q, want %q", SccacheURL, want)
	}
	if SccacheMember != "sccache-v"+SccacheVersion+"-x86_64-unknown-linux-musl/sccache" {
		t.Errorf("member %q does not match v%s", SccacheMember, SccacheVersion)
	}
}

// The wasm32 standard library of each toolchain is that image's own compiler
// version: serve lays it so no atom runs `rustup target add`.
func TestRustStdPinsFollowTheImages(t *testing.T) {
	base := "https://static.rust-lang.org/dist/rust-std-"
	if RustStdURL != base+RustComponentsTag+"-wasm32-unknown-unknown.tar.gz" || RustStdWasmURL != base+RustWasmComponentsTag+"-wasm32-unknown-unknown.tar.gz" {
		t.Errorf("%s %s", RustStdURL, RustStdWasmURL)
	}
}

// The rust component tarballs are the toolchain images' own version: a bump of
// an image tag that leaves its clippy and rustfmt behind would lay components
// of another compiler over the toolchain.
func TestRustComponentPinsFollowTheImages(t *testing.T) {
	for _, tc := range []struct{ image, version, clippy, rustfmt string }{
		{ImageRust, RustComponentsTag, RustClippyURL, RustRustfmtURL},
		{ImageRustWasm, RustWasmComponentsTag, RustWasmClippyURL, RustWasmRustfmtURL},
	} {
		if !strings.Contains(tc.image, "library/rust:"+tc.version+"-") {
			t.Errorf("image %s is not rust %s", tc.image, tc.version)
		}
		base := "https://static.rust-lang.org/dist/"
		if tc.clippy != base+"clippy-"+tc.version+"-x86_64-unknown-linux-gnu.tar.gz" || tc.rustfmt != base+"rustfmt-"+tc.version+"-x86_64-unknown-linux-gnu.tar.gz" {
			t.Errorf("components %s %s are not %s's", tc.clippy, tc.rustfmt, tc.version)
		}
	}
}

// mold is the version bookworm's package carries, which the Dagger lane has run
// the rust atoms with: a pin that moved would re-measure the link thread cap.
func TestMoldPinIsBookworms(t *testing.T) {
	if MoldRelease != "1.10.1" || MoldURL != "https://github.com/rui314/mold/releases/download/v"+MoldRelease+"/mold-"+MoldRelease+"-x86_64-linux.tar.gz" {
		t.Errorf("mold pin %s %s", MoldRelease, MoldURL)
	}
}

func TestShellcheckPinAgrees(t *testing.T) {
	if !strings.Contains(ShellcheckURL, "/v"+ShellcheckVersion+"/") || !strings.Contains(ShellcheckURL, "shellcheck-v"+ShellcheckVersion+".linux.x86_64.tar.xz") {
		t.Errorf("shellcheck pin %q does not carry v%s", ShellcheckURL, ShellcheckVersion)
	}
	if ShellcheckMember != "shellcheck-v"+ShellcheckVersion+"/shellcheck" {
		t.Errorf("member %q does not match v%s", ShellcheckMember, ShellcheckVersion)
	}
}

// The chain's shellcheck-py wheel wraps the shellcheck the binary runs: the wheel's
// version is the program's version with a packaging revision after it.
func TestShellcheckPyWrapsThePinnedShellcheck(t *testing.T) {
	if !strings.HasPrefix(ShellcheckPyVersion, ShellcheckVersion+".") {
		t.Errorf("shellcheck-py %s does not wrap shellcheck %s", ShellcheckPyVersion, ShellcheckVersion)
	}
}

// The tools container's base is a Debian slim image pinned by digest.
func TestToolsImageIsDebianSlimByDigest(t *testing.T) {
	if !regexp.MustCompile(`^docker\.io/library/debian:bookworm-slim@sha256:[0-9a-f]{64}$`).MatchString(ImageTools) {
		t.Errorf("ImageTools %q is not debian:bookworm-slim by digest", ImageTools)
	}
}

// THE INTERPRETER IS PINNED: a release URL naming the fleet's python, from the
// python-build-standalone project, with a checksum. `uv python install` in the
// container is what this replaced (it failed in the engine on every lane), so a
// pin that drifts back to "whatever uv resolves" is the regression.
func TestPythonInterpreterIsPinnedAndVerified(t *testing.T) {
	const host = "https://github.com/astral-sh/python-build-standalone/releases/download/"
	if !strings.HasPrefix(PythonStandaloneURL, host) {
		t.Errorf("PythonStandaloneURL %q is not a python-build-standalone release", PythonStandaloneURL)
	}
	if !strings.Contains(PythonStandaloneURL, "/cpython-"+FleetPython+".") {
		t.Errorf("PythonStandaloneURL %q does not carry FleetPython %s", PythonStandaloneURL, FleetPython)
	}
	if !strings.HasSuffix(PythonStandaloneURL, "-x86_64-unknown-linux-gnu-install_only_stripped.tar.gz") {
		t.Errorf("PythonStandaloneURL %q is not the x86_64 glibc install_only_stripped build", PythonStandaloneURL)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(ToolSHA256[PythonStandaloneURL]) {
		t.Errorf("PythonStandaloneURL has no SHA-256 in ToolSHA256")
	}
}

// A lane-installed python tool is an EXACT version, never a floor or a range:
// `==` is what the install is handed, and the probe is the tool's own name.
func TestPythonLaneToolsArePinnedExactly(t *testing.T) {
	exact := regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)
	if len(PythonLaneTools) == 0 {
		t.Fatal("the python lane installs no tools")
	}
	for _, tool := range PythonLaneTools {
		if !exact.MatchString(tool.Version) {
			t.Errorf("%s version %q is not exact", tool.Name, tool.Version)
		}
		if tool.Spec() != tool.Name+"=="+tool.Version {
			t.Errorf("%s spec %q is not name==version", tool.Name, tool.Spec())
		}
		if len(tool.Probe) == 0 || tool.Probe[0] != tool.Name {
			t.Errorf("%s probe %v does not exec the tool by name", tool.Name, tool.Probe)
		}
	}
}

// The `uv run --with` tools that must resolve into the repository's own
// environment carry an exact pin, and an unknown name passes through unchanged.
func TestPythonWithPinsTheEnvironmentTools(t *testing.T) {
	for name, want := range map[string]string{
		"pip-audit": "pip-audit==" + PipAuditVersion,
		"coverage":  "coverage==" + CoverageVersion,
		"mypy":      "mypy==" + MypyVersion,
		"pytest":    "pytest==" + PytestVersion,
		"other":     "other",
	} {
		if got := PythonWith(name); got != want {
			t.Errorf("PythonWith(%q) = %q, want %q", name, got, want)
		}
	}
}

// The rust lane's uv install names the fleet's python to the PATCH.
func TestFleetPythonVersionIsAPatchOfTheFleetsPython(t *testing.T) {
	if !strings.HasPrefix(FleetPythonVersion, FleetPython+".") {
		t.Errorf("FleetPythonVersion %q is not a patch of FleetPython %s", FleetPythonVersion, FleetPython)
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(FleetPythonVersion) {
		t.Errorf("FleetPythonVersion %q is not major.minor.patch", FleetPythonVersion)
	}
}

// The regen lane's wasm-tools release asset names its own version, its member is
// the binary, and an unpinned version has no release (the lane compiles it).
func TestWasmToolsReleaseAgrees(t *testing.T) {
	for _, v := range []string{WasmTools1250Version, WasmToolsVersion} {
		url, member, ok := WasmToolsRelease(v)
		if !ok || !strings.Contains(url, "/v"+v+"/wasm-tools-"+v+"-x86_64-linux.tar.gz") || member != "wasm-tools-"+v+"-x86_64-linux/wasm-tools" {
			t.Errorf("WasmToolsRelease(%q) = %q, %q, %v", v, url, member, ok)
		}
		if _, ok := ToolSHA256[url]; !ok {
			t.Errorf("%s is released but has no checksum", url)
		}
	}
	if _, _, ok := WasmToolsRelease("0.0.1"); ok {
		t.Error("a version with no pinned asset must fall back to the compile")
	}
}

// A prebuilt gravity is keyed by a full commit and pinned by a sha256.
func TestGravityPrebuiltIsPinned(t *testing.T) {
	for rev, sum := range GravityPrebuilt {
		if !gravityRevRe.MatchString(rev) || !sha256Hex.MatchString(sum) {
			t.Errorf("GravityPrebuilt[%q] = %q is not a commit and a sha256", rev, sum)
		}
	}
}

// Each prebuilt is one provisioned tool, named by its rev's first seven hex
// digits, and the atoms that run a tree's regen tests need exactly those tools.
func TestGravityToolsAreNamedByRevAndNeededTogether(t *testing.T) {
	if got := GravityTool("68d8146b88e53e619bfbc0a36b9d70ef9c3bcf06"); got != "gravity-68d8146" {
		t.Errorf("GravityTool = %q", got)
	}
	needs := GravityNeeds()
	if len(needs) != len(GravityPrebuilt) {
		t.Fatalf("GravityNeeds = %v for %d prebuilts", needs, len(GravityPrebuilt))
	}
	for rev := range GravityPrebuilt {
		if !slices.Contains(needs, "tool:"+GravityTool(rev)) {
			t.Errorf("GravityNeeds = %v lacks %s", needs, GravityTool(rev))
		}
	}
	if !slices.IsSorted(needs) {
		t.Errorf("GravityNeeds = %v is not in a stable order", needs)
	}
}

// THE 3.15 INTERPRETER IS THE BUILD UV PICKS: a python-build-standalone
// install_only_stripped x86_64 glibc asset whose file name carries the release
// version, with a checksum, so an executor pod lays exactly what the Dagger lanes'
// uv downloads for a tree that asks for "3.15".
func TestPython315InterpreterIsPinnedAndVerified(t *testing.T) {
	const host = "https://github.com/astral-sh/python-build-standalone/releases/download/"
	if !strings.HasPrefix(PythonStandalone315URL, host) {
		t.Errorf("PythonStandalone315URL %q is not a python-build-standalone release", PythonStandalone315URL)
	}
	if !strings.Contains(PythonStandalone315URL, "/cpython-"+PythonStandalone315Release+"%2B") || !strings.HasPrefix(PythonStandalone315Release, "3.15.") {
		t.Errorf("PythonStandalone315URL %q does not carry release %s", PythonStandalone315URL, PythonStandalone315Release)
	}
	if !strings.HasSuffix(PythonStandalone315URL, "-x86_64-unknown-linux-gnu-install_only_stripped.tar.gz") {
		t.Errorf("PythonStandalone315URL %q is not the x86_64 glibc install_only_stripped build", PythonStandalone315URL)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(ToolSHA256[PythonStandalone315URL]) {
		t.Errorf("PythonStandalone315URL has no SHA-256 in ToolSHA256")
	}
}
