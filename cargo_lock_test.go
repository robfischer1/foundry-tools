package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// mountSharingAt is the sharing mode the volume at path carries when the exec
// that opens at needle runs: the LAST withMountedCache of that path before the
// needle, since a later mount of a path replaces an earlier one. "" when none.
func mountSharingAt(chain, path, needle string) string {
	end := strings.Index(chain, needle)
	if end < 0 {
		return ""
	}
	got := ""
	for i := 0; i < end; {
		k := strings.Index(chain[i:end], "withMountedCache(")
		if k < 0 {
			break
		}
		start := i + k + len("withMountedCache")
		stop := start + matching(chain[start:], '(', ')')
		if args := chain[start : stop+1]; strings.Contains(args, `path:"`+path+`"`) {
			switch {
			case strings.Contains(args, "sharing:LOCKED"):
				got = "LOCKED"
			case strings.Contains(args, "sharing:SHARED"):
				got = "SHARED"
			default:
				got = "DEFAULT"
			}
		}
		i = stop + 1
	}
	return got
}

// foundry-tools#16916: two gates of one repo shared the per-repo cargo volumes
// and one replaced the binaries the other had built and not yet run. Every exec
// that builds and then runs from such a volume mounts it LOCKED; the rest keep
// their sharing.
func TestBuildThenRunExecsMountTheirCargoVolumeLocked(t *testing.T) {
	const target, release = "/cache/cargo-target", "/cache/cargo-release"

	// rust:cargo-test: the listing build and the suite both.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(`"--list"`, "tests::one: test\n")
	runAtom(t, "rust:cargo-test", "")
	for _, needle := range []string{`"cargo","test","--workspace","--","--list"`, `"cargo","test","--workspace"]`} {
		c := engine.chain(needle, "exitCode")
		if got := mountSharingAt(c, target, needle); got != "LOCKED" {
			t.Errorf("rust:cargo-test %s: cargo target is %q, want LOCKED:\n%s", needle, got, c)
		}
		// The registry volumes stay shared.
		if got := mountSharingAt(c, "/cache/cargo-home", needle); got == "LOCKED" {
			t.Errorf("registry must stay shared")
		}
	}

	// rust:cargo-clippy builds no binary it runs: still the default sharing.
	engine.reset()
	engine.withTree(everyLaneTree)
	runAtom(t, "rust:cargo-clippy", "")
	if c := engine.chain(`"cargo","clippy"`, "exitCode"); mountSharingAt(c, target, `"cargo","clippy"`) == "LOCKED" {
		t.Errorf("rust:cargo-clippy runs no binary and must stay shared:\n%s", c)
	}

	// rust:wit-guest: the native test locks the release volume; the guest build
	// (copied out in its own exec) keeps SHARED.
	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": witTwoWorlds})
	engine.stdout(`"component","wit"`, "world w")
	wantState(t, runAtom(t, "rust:wit-guest", ""), 0)
	for _, w := range []string{"wit-guest-identity", "wit-guest-reader"} {
		c := engine.chain(`"cargo","test"`, `"--no-default-features"`, feat(w), "exitCode")
		if got := mountSharingAt(c, release, `"cargo","test"`); got != "LOCKED" {
			t.Errorf("rust:wit-guest %s test: release volume is %q, want LOCKED:\n%s", w, got, c)
		}
		if got := mountSharingAt(c, release, `"cargo","build"`); got == "LOCKED" {
			t.Errorf("rust:wit-guest %s build copies out in-exec and stays SHARED, got %q", w, got)
		}
	}

	// rust:wit-compose stays SHARED throughout: its compose exec would wait
	// silently behind the same gate's LOCKED wit-guest tests.
	engine.reset()
	engine.withTree(witComposeTree(nil))
	engine.stdout(composeReplay, "TOTAL 1371 pass, 0 fail")
	wantState(t, runAtom(t, "rust:wit-compose", ""), 0)
	c := engine.chain(composeReplay, "exitCode")
	for _, needle := range []string{composeBuildHost, composeRun, composeReplay} {
		if got := mountSharingAt(c, release, needle); got != "SHARED" {
			t.Errorf("rust:wit-compose %s: release volume is %q, want SHARED:\n%s", needle, got, c)
		}
	}
}

// The locked remount names the same volume the lane mounted — per repo, never
// a per-gate key (a cold volume per gate is the cost this avoids) — and the Dev
// surface keeps its tree-keyed key.
func TestLockedCargoVolumesKeepTheirPerRepoKey(t *testing.T) {
	r := newRun(dag.Directory(), "http://door/x.git", "")
	engine.reset()
	if _, err := r.withLockedCargoTarget(r.cargoDeps()).WithExec([]string{"cargo", "test"}).Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	key := checks.CachesForRepo(checks.ImageRust, r.repo)[1].Key
	c := engine.chain(`"cargo","test"`)
	if c == "" || engine.chain(`cacheVolume(`, `key:"`+key+`"`) == "" || mountSharingAt(c, "/cache/cargo-target", `"cargo","test"`) != "LOCKED" {
		t.Errorf("want %s mounted LOCKED:\n%s", key, c)
	}

	engine.reset()
	d := newRun(dag.Directory(), "cerberus", "")
	d.dev, d.tree = true, "/w/one"
	if _, err := d.withLockedCargoTarget(d.cargoDeps()).WithExec([]string{"cargo", "test"}).Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	dkey := checks.CachesForDev(checks.ImageRust, "cerberus", "/w/one")[1].Key
	if c := engine.chain(`"cargo","test"`); engine.chain(`cacheVolume(`, `key:"`+dkey+`"`) == "" || mountSharingAt(c, "/cache/cargo-target", `"cargo","test"`) != "LOCKED" {
		t.Errorf("dev keeps its tree key %s, LOCKED:\n%s", dkey, c)
	}

	engine.reset()
	rel := newRun(dag.Directory(), "http://door/x.git", "")
	if _, err := rel.withLockedReleaseVolume(rel.withReleaseCache(rel.cargoDeps())).WithExec([]string{"cargo", "test"}).Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	rk := checks.ReleaseCacheFor(rel.repo).Key
	if c := engine.chain(`"cargo","test"`); engine.chain(`cacheVolume(`, `key:"`+rk+`"`) == "" || mountSharingAt(c, "/cache/cargo-release", `"cargo","test"`) != "LOCKED" {
		t.Errorf("want %s mounted LOCKED:\n%s", rk, c)
	}
}
