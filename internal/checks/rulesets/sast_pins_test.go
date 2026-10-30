package rulesets

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// THE EMBEDDED FORK SAST RULESETS ARE COPIES, AND NOTHING ELSE PINS THEM.
// A gate test cannot read the template repos, so each copy is held to the
// sha256 of the template file it was taken from. Changing a template does not
// fail this test (it cannot see the template); it is the tripwire for the
// other direction, an embedded file edited by hand or re-copied without
// updating the pin below, and it names the source so a reviewer knows what to
// diff.
//
// To re-copy after a template change, from the foundry-tools root:
//
//	git -C ~/Forge/Outputs/<lang>-repo-template show origin/main:template/rules/sast/dataflow.yml \
//	  > internal/checks/rulesets/sast-<lang>.yml
//	sha256sum internal/checks/rulesets/sast-<lang>.yml   # paste into the pin
//
// This test only guards this repo's embedded bytes. It reads nothing outside
// the package and cannot change any other repo's verdict.
var sastPins = []struct {
	name, source, sha string
	body              *string
}{
	{"go", "go-repo-template template/rules/sast/dataflow.yml", "d59dafe7aa1819894f60f28a862f5d6af57dc408c3db96a4b29ec2a341d4f58c", &SastGo},
	{"rust", "rust-repo-template template/rules/sast/dataflow.yml", "b1c2c5f035ecb57046041638b43fe1a8bc6cdf471b56a11beea2a78f8ef90fac", &SastRust},
	{"python", "python-repo-template template/rules/sast/dataflow.yml", "79ea5b8e0b133ffbbef143949012f1950b79b8f8d7830f2fdf860b1209ea9b6f", &SastPython},
	{"frontend", "frontend-repo-template template/rules/sast/dataflow.yml", "31325c47a1a140da28f12880e3ee7a40b506a1d225b13e5126e8a8a15e206f43", &SastFrontend},
}

func TestEmbeddedSastRulesetsMatchTheirTemplatePins(t *testing.T) {
	for _, p := range sastPins {
		t.Run(p.name, func(t *testing.T) {
			sum := sha256.Sum256([]byte(*p.body))
			if got := hex.EncodeToString(sum[:]); got != p.sha {
				t.Errorf("sast-%s.yml sha256 %s, pinned %s (source: %s). Re-copy from the template and update the pin, or revert the edit", p.name, got, p.sha, p.source)
			}
		})
	}
}
