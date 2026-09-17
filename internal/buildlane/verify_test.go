package buildlane

import (
	"strings"
	"testing"
)

func TestSplitFindingsKeepsTheStarsOwnAndNamesTheBases(t *testing.T) {
	image := []string{
		"HIGH CVE-2026-2 perl-base 5.40.1-6 -> 5.40.1-6+deb13u1 (registry.notusmi.com/rob/calliope:stable (debian 13.1))",
		"HIGH GO-2026-1 google.golang.org/grpc v1.83.1 -> 1.83.2 (calliope)",
		"CRITICAL CVE-2026-3 libsqlite3-0 3.46.1 -> 3.46.2 (registry.notusmi.com/rob/calliope:stable (debian 13.1))",
	}
	base := []string{
		// The same findings under the base's own target name: the target is
		// not part of the match.
		"HIGH CVE-2026-2 perl-base 5.40.1-6 -> 5.40.1-6+deb13u1 (registry.notusmi.com/rob/stellar_core@sha256:9a21 (debian 13.1))",
		"CRITICAL CVE-2026-3 libsqlite3-0 3.46.1 -> 3.46.2 (registry.notusmi.com/rob/stellar_core@sha256:9a21 (debian 13.1))",
		// A finding the base has and the image does not is nobody's here.
		"HIGH CVE-2026-9 gzip 1.13-1 -> 1.13-1+deb13u1 (registry.notusmi.com/rob/stellar_core@sha256:9a21 (debian 13.1))",
	}
	own, inherited := SplitFindings(image, base)
	if len(own) != 1 || !strings.Contains(own[0], "google.golang.org/grpc") {
		t.Errorf("own = %v, want grpc alone", own)
	}
	if len(inherited) != 2 || !strings.Contains(inherited[0], "perl-base") || !strings.Contains(inherited[1], "libsqlite3-0") {
		t.Errorf("inherited = %v, want perl and sqlite, in the image's order", inherited)
	}

	// A different fixed version is a different finding: the base was rebuilt
	// past it, and the image was not.
	own, inherited = SplitFindings(
		[]string{"HIGH CVE-2026-2 perl-base 5.40.1-6 -> 5.40.1-6+deb13u1 (img)"},
		[]string{"HIGH CVE-2026-2 perl-base 5.40.1-6 -> 5.40.1-6+deb13u2 (base)"},
	)
	if len(own) != 1 || len(inherited) != 0 {
		t.Errorf("a finding differing in its fix was treated as the base's: own=%v inherited=%v", own, inherited)
	}

	// No base: everything is the star's.
	own, inherited = SplitFindings(image, nil)
	if len(own) != 3 || len(inherited) != 0 {
		t.Errorf("with no base: own=%d inherited=%d, want 3 and 0", len(own), len(inherited))
	}
	// A line with no target still matches one with.
	own, inherited = SplitFindings([]string{"HIGH CVE-1 x 1 -> 2"}, []string{"HIGH CVE-1 x 1 -> 2 (base)"})
	if len(own) != 0 || len(inherited) != 1 {
		t.Errorf("untargeted line: own=%v inherited=%v", own, inherited)
	}
}

func TestScanVerdict(t *testing.T) {
	own := []string{"HIGH GO-2026-1 google.golang.org/grpc v1.83.1 -> 1.83.2 (calliope)"}
	inherited := []string{"HIGH CVE-2026-2 perl-base 5.40.1-6 -> 5.40.1-6+deb13u1 (calliope)"}
	base := "registry.notusmi.com/rob/stellar_core:python-runtime@sha256:9a21"

	code, why := ScanVerdict(own, inherited, base)
	if code != Findings {
		t.Errorf("code = %d, want findings", code)
	}
	for _, want := range []string{"findings in the scan: 1 fixable HIGH or CRITICAL vulnerabilities in this image's own layer", own[0], "1 inherited from the base " + base, "its lane's to fix, not counted here", inherited[0]} {
		if !strings.Contains(why, want) {
			t.Errorf("verdict lacks %q:\n%s", want, why)
		}
	}

	code, why = ScanVerdict(nil, inherited, base)
	if code != Clean || !strings.Contains(why, "scan clean: no fixable HIGH or CRITICAL vulnerabilities in this image's own layer") || !strings.Contains(why, "1 inherited") {
		t.Errorf("inherited only: code=%d why=%q", code, why)
	}

	code, why = ScanVerdict(nil, nil, base)
	if code != Clean || strings.Contains(why, "inherited") || !strings.HasPrefix(why, "scan clean") {
		t.Errorf("clean: code=%d why=%q", code, why)
	}

	code, why = ScanVerdict(own, nil, "")
	if code != Findings || strings.Contains(why, "inherited") {
		t.Errorf("own only: code=%d why=%q", code, why)
	}
}
