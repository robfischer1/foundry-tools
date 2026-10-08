package checks

import (
	"regexp"
	"strings"
	"testing"
)

// Every URL the tools container fetches has a checksum, and it is a SHA-256.
func TestEveryToolPinHasAChecksum(t *testing.T) {
	hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, url := range []string{OpaURL, OpengrepURL, HadolintURL, KubectlURL, ChezmoiURL, ComposeURL, WasmToolsURL, JustURL, ShellcheckURL} {
		sum, ok := ToolSHA256[url]
		if !ok || !hex.MatchString(sum) {
			t.Errorf("%s has no SHA-256 in ToolSHA256 (got %q)", url, sum)
		}
	}
	if len(ToolSHA256) != 9 {
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

// The tools container's base is a Debian slim image pinned by digest.
func TestToolsImageIsDebianSlimByDigest(t *testing.T) {
	if !regexp.MustCompile(`^docker\.io/library/debian:bookworm-slim@sha256:[0-9a-f]{64}$`).MatchString(ImageTools) {
		t.Errorf("ImageTools %q is not debian:bookworm-slim by digest", ImageTools)
	}
}
