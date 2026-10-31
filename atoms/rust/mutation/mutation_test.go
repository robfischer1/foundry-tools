package rustmutation

import (
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/rustmut"
)

func TestTheDeclarationValidatesAndNamesItsRow(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != rustmut.ID || Def.Stage != checks.StageMutation || Def.Lane != checks.LaneRust || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "rust-trixie" || Def.Surface != "root:Cargo.toml" || Def.NotOnForks != checks.ForkChangeBar ||
		Def.History != "full" || Def.WritesTree || strings.Join(Def.Needs, ",") != "tool:cargo-mutants,tool:cargo-nextest,tool:python-standalone,tool:uv,tool:jq" {
		t.Errorf("%+v", Def)
	}
}

// A SUITE THAT EXECS jq MUST FIND IT: under nextest as under cargo test, a test
// that skips for want of jq leaves its mutants judged by a test that never ran.
func TestTheSuiteHasJq(t *testing.T) {
	for _, n := range checks.SuiteJqNeeds {
		if !slices.Contains(Def.Needs, n) {
			t.Errorf("Needs %v lacks %s", Def.Needs, n)
		}
	}
}
