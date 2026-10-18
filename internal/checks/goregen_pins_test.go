package checks

import (
	"strings"
	"testing"
)

func TestRegenRustLineReadsTheRecordedCompiler(t *testing.T) {
	for _, tc := range []struct {
		in, line, version string
		ok                bool
	}{
		{"rustc 1.97.1 (8bab26f4f 2026-07-14)", "rustc 1.97.1 (8bab26f4f 2026-07-14)", "1.97.1", true},
		{"rustc 1.99.0 (b940084d7 2026-09-28), wasm-tools 1.261.0, wac 0.12.0", "rustc 1.99.0 (b940084d7 2026-09-28)", "1.99.0", true},
		{"  rustc 1.97.0 (2d8144b78 2026-07-07), was recorded 1.97.1  ", "rustc 1.97.0 (2d8144b78 2026-07-07)", "1.97.0", true},
		{"", "", "", false},
		{"cargo 1.97.1 (x 2026-01-01)", "", "", false},
		{"rustc 1.97.1", "", "", false},
		{"rustc 1.97.1 8bab26f4f 2026-07-14", "", "", false},
		{"rustc  (a b)", "", "", false},
		{"rustc 1.97.1 (unclosed", "", "", false},
	} {
		line, version, ok := RegenRustLine(tc.in)
		if line != tc.line || version != tc.version || ok != tc.ok {
			t.Errorf("%q: %q %q %v, want %q %q %v", tc.in, line, version, ok, tc.line, tc.version, tc.ok)
		}
	}
}

func TestRegenRustDistNamesBothVersionsWhenThePinIsMissing(t *testing.T) {
	d, err := RegenRustDist("1.99.0", "1.97.0")
	if err != nil || d.Version != "1.99.0" || len(d.URLs) != 4 {
		t.Fatalf("%+v %v", d, err)
	}
	_, err = RegenRustDist("1.98.2", "1.97.0")
	if err == nil {
		t.Fatal("a version with no pin was served")
	}
	for _, want := range []string{"pins rustc 1.98.2", "carries rustc 1.97.0", "only 1.97.1, 1.99.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not say %q", err, want)
		}
	}
}

// Every laid toolchain is the four dist tarballs of its own version, each held
// to a sum, and the tarballs are the order the install needs: the compiler,
// cargo, the host standard library, then wasm32's.
func TestEveryRustDistIsFourPinnedTarballsOfItsVersion(t *testing.T) {
	for _, d := range RustDists {
		if len(d.URLs) != 4 {
			t.Fatalf("%s: %d tarballs", d.Version, len(d.URLs))
		}
		for i, prefix := range []string{"rustc-", "cargo-", "rust-std-", "rust-std-"} {
			u := d.URLs[i]
			want := "https://static.rust-lang.org/dist/" + prefix + d.Version + "-"
			if !strings.HasPrefix(u, want) || !strings.HasSuffix(u, ".tar.gz") {
				t.Errorf("%s: %s is not a %s%s dist tarball", d.Version, u, prefix, d.Version)
			}
			if _, _, err := ToolSources(u, ToolSHA256); err != nil {
				t.Errorf("%s: %v", u, err)
			}
		}
		if !strings.HasSuffix(d.URLs[2], "-x86_64-unknown-linux-gnu.tar.gz") || !strings.HasSuffix(d.URLs[3], "-wasm32-unknown-unknown.tar.gz") {
			t.Errorf("%s: the standard libraries are %s and %s", d.Version, d.URLs[2], d.URLs[3])
		}
	}
}

func TestGofmtIsTheGoOfTheToolchainImage(t *testing.T) {
	if !strings.Contains(ImageGo, "golang:"+GofmtGoRelease+"-") {
		t.Errorf("%s is not Go %s", ImageGo, GofmtGoRelease)
	}
	if GofmtURL != "https://go.dev/dl/go"+GofmtGoRelease+".linux-amd64.tar.gz" || !strings.HasPrefix(GofmtMember, "go/bin/") {
		t.Errorf("%s %s", GofmtURL, GofmtMember)
	}
	if _, _, err := ToolSources(GofmtURL, ToolSHA256); err != nil {
		t.Error(err)
	}
}
