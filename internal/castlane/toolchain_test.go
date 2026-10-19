package castlane

import (
	"os"
	"strings"
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

// R20: the executor says what Dagger's cast lane says. cast.go is the other
// runner and is not imported (it is the module's main package), so its two
// sentences are read out of its source and held equal to these.
func TestDEF_B6_cast_RefusalsAreTheDaggerLanesSentences(t *testing.T) {
	src, err := os.ReadFile("../../cast.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{RefusedBoth, RefusedNeither} {
		if !strings.Contains(string(src), "findings: "+s) {
			t.Errorf("cast.go does not say %q", s)
		}
	}
}
