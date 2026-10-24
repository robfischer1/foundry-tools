package castlane

import (
	"testing"
)

// THE LANE IS A FACT ABOUT THE TREE: a Cargo.toml alone is rust, a root go.mod
// alone is go, and a tree that declares both or neither is refused by name.
func TestDEF_B6_cast_ToolchainIsWhatTheTreeDeclares(t *testing.T) {
	for _, c := range []struct {
		rust, goRoot bool
		want, why    string
	}{
		{true, false, RustToolchain, ""},
		{false, true, GoToolchain, ""},
		{true, true, "", RefusedBoth},
		{false, false, "", RefusedNeither},
	} {
		got, why := Toolchain(c.rust, c.goRoot)
		if got != c.want || why != c.why {
			t.Errorf("rust=%v go=%v: got (%q, %q), want (%q, %q)", c.rust, c.goRoot, got, why, c.want, c.why)
		}
	}
}
