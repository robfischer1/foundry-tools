package buildlane

import (
	"fmt"
	"strings"
)

// THE VERIFY STAGE'S SCAN SCOPE (CA master-plan F14). A star's image is its
// base plus its own layer, and a vulnerability in the base is the base lane's
// to fix: the star cannot rebuild perl. So the scan's verdict is over what the
// star OWNS — the findings its image carries that its base does not — and the
// base's findings are reported beside it, never counted against it.
//
// MEASURED BEFORE DECIDING, 2026-09-17, trivy over all 44 rob/*:stable images
// with the base lane's own flags (HIGH,CRITICAL, fixable only): 34 carried at
// least one finding, 421 in all. Of those, 25 Go stars carried exactly one,
// the same one — google.golang.org/grpc v1.83.1, fixed in 1.83.2 — which IS
// the star's own; the rest were Debian and Alpine packages inherited from the
// base (calliope and demeter: 14 each, all perl-base, libpcre2, libsqlite3;
// ergo-recorder: 309, linux-libc-dev and imagemagick). A verdict over the
// whole image would have failed 34 of 44 landings for findings 30 of them
// could not act on.

// SplitFindings partitions an image's findings into the star's own and the
// ones its base already carries. Lines are TrivyFindings' — sorted, one per
// finding — and a line matches when the base reports the same one; the target
// in parentheses is dropped first, because the base's target is its own image
// name and the star's is the star's.
func SplitFindings(image, base []string) (own, inherited []string) {
	seen := map[string]bool{}
	for _, b := range base {
		seen[untargeted(b)] = true
	}
	for _, f := range image {
		if seen[untargeted(f)] {
			inherited = append(inherited, f)
			continue
		}
		own = append(own, f)
	}
	return own, inherited
}

// untargeted is a finding line without its "(target)" suffix. The cut is at
// the FIRST " (": a target nests its own parentheses ("calliope:stable (debian
// 13.1)"), and the fields before it — severity, id, package, versions — never
// carry a space-paren.
func untargeted(line string) string {
	before, _, _ := strings.Cut(line, " (")
	return before
}

// ScanVerdict is the trivy atom's answer: findings when the star owns any,
// clean otherwise, the inherited ones named either way so a reader sees the
// whole image and knows which lane each finding belongs to.
func ScanVerdict(own, inherited []string, base string) (int, string) {
	var b strings.Builder
	if len(own) > 0 {
		fmt.Fprintf(&b, "findings in the scan: %d fixable HIGH or CRITICAL vulnerabilities in this image's own layer:\n%s", len(own), strings.Join(own, "\n"))
	} else {
		b.WriteString("scan clean: no fixable HIGH or CRITICAL vulnerabilities in this image's own layer")
	}
	if len(inherited) > 0 {
		fmt.Fprintf(&b, "\n%d inherited from the base %s (its lane's to fix, not counted here):\n%s", len(inherited), base, strings.Join(inherited, "\n"))
	}
	if len(own) > 0 {
		return Findings, b.String()
	}
	return Clean, b.String()
}
