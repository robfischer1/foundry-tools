package checks

import (
	"regexp"
	"strings"
	"testing"
)

// Every URL the tools container fetches has a checksum, and it is a SHA-256.
func TestEveryToolPinHasAChecksum(t *testing.T) {
	hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, url := range []string{OpaURL, OpengrepURL, HadolintURL, KubectlURL, ChezmoiURL, ComposeURL, WasmToolsURL, JustURL, ShellcheckURL, PythonStandaloneURL} {
		sum, ok := ToolSHA256[url]
		if !ok || !hex.MatchString(sum) {
			t.Errorf("%s has no SHA-256 in ToolSHA256 (got %q)", url, sum)
		}
	}
	if len(ToolSHA256) != 10 {
		t.Errorf("ToolSHA256 holds %d entries; a stale line for a URL nothing fetches is a pin nobody checks", len(ToolSHA256))
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
