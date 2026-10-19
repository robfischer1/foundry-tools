package releasekit

import (
	"context"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

const narcToml = `[[binary]]
package = "./cmd/narc"
out = "release/narc"
cgo = true
tags = ["netgo", "osusergo"]
ldflags = '-s -w -extldflags "-static"'
`

func TestUT_ReadManifestDeclaresEachBinary(t *testing.T) {
	got, err := ReadManifest(narcToml + "\n[[binary]]\npackage = \"./cmd/aux\"\nout = \"release/aux\"\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []checks.ReleaseBinary{
		{Name: "aux", Package: "./cmd/aux"},
		{Name: "narc", Package: "./cmd/narc", Cgo: true, Tags: []string{"netgo", "osusergo"}, Ldflags: `-s -w -extldflags "-static"`},
	}
	if len(got) != 2 || got[0].Name != want[0].Name || got[0].Cgo || got[0].Ldflags != "" || got[0].Tags != nil ||
		got[1].Name != "narc" || !got[1].Cgo || !slices.Equal(got[1].Tags, want[1].Tags) || got[1].Ldflags != want[1].Ldflags || got[1].Package != "./cmd/narc" {
		t.Errorf("%+v", got)
	}
}

func TestUT_ReadManifestRefusesWhatItCannotBuild(t *testing.T) {
	for name, c := range map[string]struct{ body, why string }{
		"not toml":       {"[[binary", "release.toml"},
		"empty":          {"", "declares no [[binary]]"},
		"unknown key":    {"[[binary]]\npackage = \"./cmd/a\"\nout = \"release/a\"\nflags = \"x\"\n", "unknown key"},
		"no package":     {"[[binary]]\nout = \"release/a\"\n", "names no package"},
		"package path":   {"[[binary]]\npackage = \"cmd/a\"\nout = \"release/a\"\n", "not a ./ package"},
		"package escape": {"[[binary]]\npackage = \"./../a\"\nout = \"release/a\"\n", "not a ./ package"},
		"no out":         {"[[binary]]\npackage = \"./cmd/a\"\n", "release/<name>"},
		"out elsewhere":  {"[[binary]]\npackage = \"./cmd/a\"\nout = \"bin/a\"\n", "release/<name>"},
		"out nested":     {"[[binary]]\npackage = \"./cmd/a\"\nout = \"release/x/a\"\n", "release/<name>"},
		"out dot":        {"[[binary]]\npackage = \"./cmd/a\"\nout = \"release/.a\"\n", "release/<name>"},
		"duplicate":      {"[[binary]]\npackage = \"./cmd/a\"\nout = \"release/a\"\n[[binary]]\npackage = \"./cmd/b\"\nout = \"release/a\"\n", "twice"},
		"bad tag":        {"[[binary]]\npackage = \"./cmd/a\"\nout = \"release/a\"\ntags = [\"a b\"]\n", "not a build tag"},
		"empty tag":      {"[[binary]]\npackage = \"./cmd/a\"\nout = \"release/a\"\ntags = [\"\"]\n", "not a build tag"},
		"newline ld":     {"[[binary]]\npackage = \"./cmd/a\"\nout = \"release/a\"\nldflags = \"a\\nb\"\n", "ldflags"},
	} {
		if _, err := ReadManifest(c.body); err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: %v, want %q", name, err, c.why)
		}
	}
}

func TestUT_GoPlanReadsReleaseTomlWhenTheTreeCarriesOne(t *testing.T) {
	ctx := context.Background()
	sub := Subject{Star: "narcissus", Body: "FROM x\nCOPY release/narc /narc\n"}
	tree := mem{files: map[string]string{"release.toml": narcToml}, dirs: []string{"cmd/narc/"}}
	plan, why := GoPlan(ctx, tree, sub)
	if why != "" || !plan.Manifest || len(plan.Binaries) != 1 || !plan.Binaries[0].Cgo || plan.Binaries[0].Package != "./cmd/narc" || plan.Star != "narcissus" || plan.Lane != checks.LaneGo {
		t.Fatalf("%+v %q", plan, why)
	}
	tree.dirs = append(tree.dirs, "vendor/")
	if plan, _ = GoPlan(ctx, tree, sub); !plan.Vendored {
		t.Error("vendor is still read off the tree")
	}
	// WITHOUT release.toml the plan is the Dockerfile's, unchanged.
	plan, why = GoPlan(ctx, mem{dirs: []string{"cmd/narc/"}}, sub)
	if why != "" || plan.Manifest || plan.Binaries[0].Cgo || plan.Binaries[0].Tags != nil || plan.Binaries[0].Ldflags != "" {
		t.Errorf("%+v %q", plan, why)
	}
}

func TestUT_GoPlanRefusesAManifestThatDisagreesWithTheTree(t *testing.T) {
	ctx := context.Background()
	sub := Subject{Star: "narcissus", Body: "FROM x\nCOPY release/narc /narc\n"}
	files := map[string]string{"release.toml": narcToml}
	for name, c := range map[string]struct {
		tree mem
		sub  Subject
		why  string
	}{
		"bad manifest": {mem{files: map[string]string{"release.toml": "[[binary"}}, sub, "release.toml"},
		"no package":   {mem{files: files}, sub, "the tree carries no cmd/narc"},
		"io":           {mem{files: files, dirs: []string{"cmd/narc/"}, fail: map[string]error{"IsDir": errBoom}}, sub, "could not be read for cmd/narc: boom"},
		"copy unbuilt": {mem{files: files, dirs: []string{"cmd/narc/"}}, Subject{Star: "n", Body: "FROM x\nCOPY release/narc /narc\nCOPY release/other /o\n"}, "copies release/other and release.toml declares no such binary"},
	} {
		if _, why := GoPlan(ctx, c.tree, c.sub); !strings.Contains(why, c.why) {
			t.Errorf("%s: %q, want %q", name, why, c.why)
		}
	}
}
