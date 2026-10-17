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

	// rust:cargo-test: the listing build and the suite are one exec now, and it
	// holds the target LOCKED throughout.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents(rustTestNeedle, rustVerdict("rust:cargo-test", "precommit"))
	runAtom(t, "rust:cargo-test", "")
	c := engine.chain(rustTestNeedle, "contents")
	if got := mountSharingAt(c, target, atomExec); got != "LOCKED" {
		t.Errorf("rust:cargo-test: cargo target is %q, want LOCKED:\n%s", got, c)
	}
	// The registry volumes stay shared.
	if got := mountSharingAt(c, "/cache/cargo-home", atomExec); got == "LOCKED" {
		t.Errorf("registry must stay shared")
	}

	// rust:cargo-clippy builds no binary it runs: still the default sharing.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents(rustClippyNeedle, rustVerdict("rust:cargo-clippy", "precommit"))
	runAtom(t, "rust:cargo-clippy", "")
	if c := engine.chain(rustClippyNeedle, "contents"); mountSharingAt(c, target, atomExec) == "LOCKED" {
		t.Errorf("rust:cargo-clippy runs no binary and must stay shared:\n%s", c)
	}

	// rust:wit-guest and rust:wit-compose keep volumes of their own, and the
	// guest's native test locks its (atom_env_rust_test.go).
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
}
