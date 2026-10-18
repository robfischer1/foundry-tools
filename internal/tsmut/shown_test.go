package tsmut

import (
	"testing"

	"dagger/foundry-tools/internal/atomkit"
)

// R20: the verdict names the tree's own paths, whichever directory the working
// copy sits in (the executor installs in a copy of its locked tree).
func TestShownSaysTheTreesRootNotTheCopys(t *testing.T) {
	g := &grading{in: atomkit.In{Root: "/src"}, tree: "/work/ts__mutation/ts/src"}
	for in, want := range map[string]string{
		"/work/ts__mutation/ts/src":              "/src",
		"/work/ts__mutation/ts/src/node_modules": "/src/node_modules",
		"/elsewhere/x":                           "/elsewhere/x",
		"/work/ts__mutation/ts":                  "/work/ts__mutation/ts",
	} {
		if got := g.shown(in); got != want {
			t.Errorf("shown(%q) = %q, want %q", in, got, want)
		}
	}
	g.tree = "/src"
	if got := g.shown("/src/a"); got != "/src/a" {
		t.Errorf("same tree: %q", got)
	}
}
