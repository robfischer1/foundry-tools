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

const rootToml = "[[binary]]\npackage = \".\"\nout = \"release/skms\"\nldflags = '-s -w'\n"

func TestUT_ReadManifestBuildsTheRootPackage(t *testing.T) {
	got, err := ReadManifest(rootToml)
	if err != nil || len(got) != 1 || got[0].Name != "skms" || got[0].Package != "." || got[0].Ldflags != "-s -w" {
		t.Fatalf("%+v %v", got, err)
	}
	// Only "." spells the root: "./" is still not a package path.
	for _, pkg := range []string{"./"} {
		if _, err := ReadManifest("[[binary]]\npackage = \"" + pkg + "\"\nout = \"release/a\"\n"); err == nil || !strings.Contains(err.Error(), "not a ./ package") {
			t.Errorf("%q: %v", pkg, err)
		}
	}
}

func TestUT_GoPlanBuildsTheRootPackageWithoutACmdDirectory(t *testing.T) {
	ctx := context.Background()
	sub := Subject{Star: "sigstore-kms-infisical", Body: "FROM scratch\nCOPY release/skms /usr/local/bin/skms\n"}
	// The root is the module, which GoRoot already requires: no directory to look for.
	tree := mem{files: map[string]string{"release.toml": rootToml}, fail: map[string]error{"IsDir": errBoom}}
	plan, why := GoPlan(ctx, tree, sub)
	if why != "" || len(plan.Binaries) != 1 || plan.Binaries[0].Package != "." || !plan.Manifest {
		t.Fatalf("%+v %q", plan, why)
	}
}

// A base's release.toml may also name the Go module inside the base's directory.
func TestReadBaseManifestNamesTheModuleAndTheBinaries(t *testing.T) {
	t.Parallel()
	const bin = "[[binary]]\npackage = \".\"\nout = \"release/chairman-svid\"\n"
	for body, want := range map[string]string{
		bin:                         ".",
		"module = \"svid\"\n" + bin: "svid",
		"module = \"a/b\"\n" + bin:  "a/b",
		"module = \".\"\n" + bin:    ".",
	} {
		module, bins, err := ReadBaseManifest(body)
		if err != nil || module != want || len(bins) != 1 || bins[0].Name != "chairman-svid" || bins[0].Package != RootPackage {
			t.Errorf("%q: %q %+v %v", body, module, bins, err)
		}
	}
	for name, c := range map[string]struct{ body, why string }{
		"absolute":  {"module = \"/svid\"\n" + bin, "not a directory inside the base"},
		"escape":    {"module = \"../x\"\n" + bin, "not a directory inside the base"},
		"dotdot":    {"module = \"..\"\n" + bin, "not a directory inside the base"},
		"unclean":   {"module = \"a//b\"\n" + bin, "not a directory inside the base"},
		"trailing":  {"module = \"a/\"\n" + bin, "not a directory inside the base"},
		"no binary": {"module = \"svid\"\n", "declares no [[binary]]"},
		"not toml":  {"[[binary", "release.toml"},
		"unknown":   {"modul = \"svid\"\n" + bin, "unknown key"},
	} {
		if _, _, err := ReadBaseManifest(c.body); err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: %v, want %q", name, err, c.why)
		}
	}
	// a star's release.toml is its root module's: it names no module
	if _, err := ReadManifest("module = \"svid\"\n" + bin); err == nil || !strings.Contains(err.Error(), "unknown key module") {
		t.Errorf("a star's manifest took a module: %v", err)
	}
}

// A base's tools are built from the module's requirements: its [[binary]] may
// name a package by import path. A star's manifest still takes only ./ packages.
func TestReadBaseManifestTakesAnImportPath(t *testing.T) {
	t.Parallel()
	out := func(n string) string { return "out = \"release/" + n + "\"\n" }
	body := "[[binary]]\npackage = \"golang.org/x/vuln/cmd/govulncheck\"\n" + out("govulncheck") +
		"[[binary]]\npackage = \"honnef.co/go/tools/cmd/staticcheck\"\n" + out("staticcheck") + "[[binary]]\npackage = \"./cmd/local\"\n" + out("local")
	_, bins, err := ReadBaseManifest(body)
	if err != nil || len(bins) != 3 || bins[0].Package != "golang.org/x/vuln/cmd/govulncheck" || bins[1].Package != "./cmd/local" || bins[2].Package != "honnef.co/go/tools/cmd/staticcheck" {
		t.Errorf("%+v %v", bins, err)
	}
	if _, err := ReadManifest(body); err == nil || !strings.Contains(err.Error(), "not a ./ package inside the module") {
		t.Errorf("a star's manifest took an import path: %v", err)
	}
	for name, pkg := range map[string]string{
		"no dot in the first element": "tools/cmd/x",
		"a flag":                      "-x.y/z",
		"absolute":                    "/a.b/c",
		"unclean":                     "a.b//c",
		"parent":                      "a.b/../c",
		"trailing":                    "a.b/c/",
		"blank":                       "a.b/ c",
		"dotdot first":                "../a.b",
	} {
		if _, _, err := ReadBaseManifest("[[binary]]\npackage = \"" + pkg + "\"\n" + out("x")); err == nil || !strings.Contains(err.Error(), "is not a ./ package or an import path") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
