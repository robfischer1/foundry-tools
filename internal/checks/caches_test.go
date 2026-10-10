package checks

import (
	"strings"
	"testing"
)

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

func TestNeedsArgFileTurnsAtTheBudgetExactly(t *testing.T) {
	// One file of exactly the budget minus its NUL fits; one byte more does not.
	fits := []string{strings.Repeat("a", argvBudget-1)}
	if NeedsArgFile(fits) {
		t.Errorf("a list exactly at the budget must fit")
	}
	over := []string{strings.Repeat("a", argvBudget)}
	if !NeedsArgFile(over) {
		t.Errorf("a list one byte over the budget must go in a file")
	}
	if NeedsArgFile(nil) {
		t.Errorf("no files need no file")
	}
}

// The cargo target dir is a build OF A TREE: two trees of one repo mounting it
// at once write over each other (foundry-tools#15765). It is keyed per repo and
// locked, and no other cache is.
func TestCargoTargetIsKeyedPerRepo(t *testing.T) {
	a := CachesForRepo(ImageRust, "https://git.example/org/a.git")
	b := CachesForRepo(ImageRust, "https://git.example/org/b.git")
	shared := CachesForRepo(ImageRust, "")
	find := func(ms []CacheMount) CacheMount {
		for _, m := range ms {
			if m.EnvVar == "CARGO_TARGET_DIR" {
				return m
			}
		}
		t.Fatalf("no cargo target mount in %+v", ms)
		return CacheMount{}
	}
	ta, tb, ts := find(a), find(b), find(shared)
	if ta.Key == tb.Key {
		t.Errorf("two repos share one target volume %q", ta.Key)
	}
	if ta.Key != find(CachesForRepo(ImageRust, "https://git.example/org/a.git")).Key {
		t.Errorf("the key is not stable for one repo")
	}
	if !strings.HasPrefix(ta.Key, "foundry-cargo-target-") || len(ta.Key) <= len("foundry-cargo-target-") {
		t.Errorf("a per-repo key keeps the volume's name and appends the repo digest, got %q", ta.Key)
	}
	base := map[string]string{}
	for _, m := range CachesFor(ImageRust) {
		base[m.Path] = m.Key
	}
	for _, m := range a {
		if !m.PerRepo && m.Key != base[m.Path] {
			t.Errorf("%s is shared across repos but its key changed to %q", m.Path, m.Key)
		}
	}
	if ts.Key != "foundry-cargo-target" {
		t.Errorf("a run naming no repo keeps the shared key, got %q", ts.Key)
	}
	for _, m := range a {
		if m.EnvVar != "CARGO_TARGET_DIR" && m.PerRepo {
			t.Errorf("only the target dir is per-repo and private: %+v", m)
		}
	}
}

// The release volume is per repository, and never the gate's debug target.
func TestReleaseCacheForKeysByRepoApartFromTheGatesTarget(t *testing.T) {
	a, b, bare := ReleaseCacheFor("http://x/a.git"), ReleaseCacheFor("http://x/b.git"), ReleaseCacheFor("")
	if a.Key == b.Key || a.Key != "foundry-cargo-release-"+repoDigest("http://x/a.git") || bare.Key != "foundry-cargo-release" {
		t.Errorf("keys %q %q %q", a.Key, b.Key, bare.Key)
	}
	if a.Path != ReleaseCachePath || !a.PerRepo || a.EnvVar != "" {
		t.Errorf("%+v", a)
	}
	for _, m := range CachesForRepo(ImageRust, "http://x/a.git") {
		if m.Path == a.Path || strings.HasPrefix(a.Key, m.Key+"-") || m.Key == a.Key {
			t.Errorf("the release volume collides with the lane's %+v", m)
		}
	}
	if RustReleaseTarget != ReleaseCachePath || WitGuestTargetDir != ReleaseCachePath || !strings.HasPrefix(WitReplayTargetDir, ReleaseCachePath+"/") {
		t.Errorf("a release build writes outside the release volume: %s %s %s", RustReleaseTarget, WitGuestTargetDir, WitReplayTargetDir)
	}
}

// The Dev surface's cargo target is keyed on repo and tree and LOCKED; nothing
// else about the lane's volumes moves, and the gate's own are never locked.
func TestCachesForDevKeysTheTargetOnRepoAndTreeAndLocksIt(t *testing.T) {
	dev := CachesForDev(ImageRust, "r", "/w/one")
	gate := CachesForRepo(ImageRust, "r")
	if len(dev) != len(gate) {
		t.Fatalf("same volumes, %d vs %d", len(dev), len(gate))
	}
	for i, c := range dev {
		if !gate[i].PerRepo {
			if c != gate[i] || c.Locked {
				t.Errorf("%s is shared and unchanged, got %+v", c.Path, c)
			}
			continue
		}
		if !c.Locked || gate[i].Locked {
			t.Errorf("%s: dev LOCKED, gate not: %+v / %+v", c.Path, c, gate[i])
		}
		if c.Key != gate[i].Key+"-"+repoDigest("/w/one") {
			t.Errorf("key %s is repo then tree", c.Key)
		}
	}
	if k := CachesForDev(ImageRust, "r", "")[2].Key; k != "foundry-cargo-target-"+repoDigest("r") {
		t.Errorf("no tree: the repo's alone, got %s", k)
	}
	if k := CachesForDev(ImageGo, "r", "/w")[0]; k.Locked || k.Key != "foundry-go-mod" {
		t.Errorf("go caches are the fleet's, shared: %+v", k)
	}
}
