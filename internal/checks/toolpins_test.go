package checks

import (
	"regexp"
	"strings"
	"testing"
)

// Every URL the tools container fetches has a checksum, and it is a SHA-256.
func TestEveryToolPinHasAChecksum(t *testing.T) {
	hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, url := range []string{OpaURL, OpengrepURL, HadolintURL, KubectlURL, ChezmoiURL, ComposeURL, WasmToolsURL, JustURL, ShellcheckURL, PythonStandaloneURL, CargoAuditURL, CargoNextestURL} {
		sum, ok := ToolSHA256[url]
		if !ok || !hex.MatchString(sum) {
			t.Errorf("%s has no SHA-256 in ToolSHA256 (got %q)", url, sum)
		}
	}
	if len(ToolSHA256) != 12 {
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
