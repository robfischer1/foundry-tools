package checks

import "testing"

// Every lane image mounts at least one toolchain cache, and every cache key
// is unique across the fleet — two toolchains sharing a volume would write
// over each other, and a lane with no cache re-downloads its world per run,
// which is the waste this file exists to end.
func TestEveryLaneImageMountsACacheWithADistinctKey(t *testing.T) {
	keys := map[string]string{}
	for _, img := range []string{ImageGo, ImagePython, ImageRust, ImageTS} {
		mounts := CachesFor(img)
		if len(mounts) == 0 {
			t.Errorf("%s mounts no toolchain cache", img)
		}
		for _, m := range mounts {
			if m.Path == "" || m.Key == "" {
				t.Errorf("%s: a cache mount is missing its path or key: %+v", img, m)
			}
			if prev, dup := keys[m.Key]; dup && prev != m.Path {
				t.Errorf("cache key %q names two paths: %s and %s", m.Key, prev, m.Path)
			}
			keys[m.Key] = m.Path
		}
	}
	if CachesFor(ImageKubeconform) != nil {
		t.Errorf("the kubeconform image has no toolchain and must mount no cache")
	}
}

// The rust lane's config.toml (the Nexus registry route) lives at
// CARGO_HOME/config.toml, beside registry/ and git/. A mount over CARGO_HOME
// itself would hide it and send every crate fetch to crates.io.
func TestTheCargoHomeItselfIsNeverMounted(t *testing.T) {
	for _, m := range CachesFor(ImageRust) {
		if m.Path == "/usr/local/cargo" || m.Path == "/usr/local/cargo/" {
			t.Errorf("a cache volume over CARGO_HOME hides the image's config.toml: %+v", m)
		}
	}
}

func TestGatePopulationDropsTheFleetExcludeAndDirectories(t *testing.T) {
	in := []string{
		"main.go", "./cmd/x.go", "vendor/a/b.go", "web/node_modules/x.js",
		".claude/settings.json", ".specify/x", ".furnace/y", "a.melt",
		"internal/", "docs/readme.md",
	}
	want := []string{"main.go", "cmd/x.go", "docs/readme.md"}
	got := GatePopulation(in)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
		}
	}
}

func TestGoHasTestFilesReadsTheCounts(t *testing.T) {
	if GoHasTestFiles("00\n00\n0\n") {
		t.Errorf("all-zero counts read as having tests")
	}
	if GoHasTestFiles("") {
		t.Errorf("no packages read as having tests")
	}
	if !GoHasTestFiles("00\n01\n") {
		t.Errorf("a package with an external test file read as having none")
	}
	if !GoHasTestFiles("30\n") {
		t.Errorf("a package with test files read as having none")
	}
}
