package main

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE ENVIRONMENT OF THE RUST SUITE ATOMS, against the paper engine: the lane
// clippy and test run in, how they hold the target volume and which of them is
// handed the base. What each atom asks of the tree is tested beside the atom
// (atoms/rust/*) and in internal/atoms.

const (
	// rustClippyNeedle and rustTestNeedle are the files the adapter reads each
	// atom's verdict from, and atomExec the exec that runs it.
	rustClippyNeedle = "rust__cargo-clippy.json"
	rustTestNeedle   = "rust__cargo-test.json"
	atomExec         = `"/atom","/src"`
)

// rustVerdict is the verdict an atom of the rust lane wrote.
func rustVerdict(id, stage string) string {
	return `{"atom":"` + id + `","stage":"` + stage + `","lane":"rust","state":0,"result":"pass","logs":[]}`
}

// THE FETCH IS THE ENVIRONMENT'S: the dependency graph is fetched once, on its
// own layer, before the atom runs, and under the default Expect — a registry
// outage is a could-not-run the door re-asks. The atom runs after it and sets
// its own cargo bounds, so nothing about the fetch layer depends on them.
func TestTheRustSuiteAtomsRunOnTheFetchedLayer(t *testing.T) {
	for _, tc := range []struct{ id, needle, stage string }{
		{"rust:cargo-clippy", rustClippyNeedle, "precommit"},
		{"rust:cargo-test", rustTestNeedle, "precommit"},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.contents(tc.needle, rustVerdict(tc.id, tc.stage))
		wantState(t, runAtom(t, tc.id, ""), 0)
		c := engine.chain(tc.needle, "contents")
		wantCalls(t, c,
			[]string{"withExec", `args:["cargo","fetch","--locked"]`},
			[]string{"withExec", "expect:ANY", `args:["/atom","/src"]`},
			[]string{"withEnvVariable", `name:"ATOM_OUT"`},
		)
		if hasCall(c, "withExec", `args:["cargo","fetch","--locked"]`, "expect:ANY") {
			t.Errorf("%s: the fetch is provisioning and must run under the default Expect:\n%s", tc.id, c)
		}
		// the atom sets its own bounds on its cargo execs; the container carries none
		if hasCall(c, "withEnvVariable", `name:"CARGO_BUILD_JOBS"`) || hasCall(c, "withEnvVariable", `name:"CARGO_INCREMENTAL"`) {
			t.Errorf("%s: the bound is the atom's, set on its execs:\n%s", tc.id, c)
		}
	}
}

// A repo that does not scope its crates pays nothing for the base: the atom's
// cache key is the tree's alone, as the chains' was (Rule 8). A repo that
// carries the scope script is handed the base, the origin and the fork record,
// because the atom reads git against it.
func TestTheBaseReachesTheRustSuiteAtomsOnlyForARepoThatScopes(t *testing.T) {
	for _, tc := range []struct{ id, needle, stage string }{
		{"rust:cargo-clippy", rustClippyNeedle, "precommit"},
		{"rust:cargo-test", rustTestNeedle, "precommit"},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.contents(tc.needle, rustVerdict(tc.id, tc.stage))
		runAtom(t, tc.id, "abc123")
		if c := engine.chain(tc.needle, "contents"); hasCall(c, "withEnvVariable", `name:"GATE_BASE"`) || hasCall(c, "withEnvVariable", `name:"ATOM_BASE"`) {
			t.Errorf("%s: a repo that does not scope reads no base:\n%s", tc.id, c)
		}

		engine.reset()
		engine.withTree(everyLaneTree)
		engine.withTree(map[string]string{checks.RustScopeScript: "#!/usr/bin/env python3\n"})
		engine.contents(tc.needle, rustVerdict(tc.id, tc.stage))
		runAtom(t, tc.id, "abc123")
		wantCalls(t, engine.chain(tc.needle, "contents"),
			[]string{"withEnvVariable", `name:"GATE_BASE"`, `value:"abc123"`},
			[]string{"withEnvVariable", `name:"ATOM_BASE"`, `value:"abc123"`},
			[]string{"withEnvVariable", `name:"ATOM_ORIGIN"`},
			[]string{"withEnvVariable", `name:"ATOM_FORK"`},
		)
	}
}

// A linked worktree's `.git` is a FILE whose gitdir does not exist in here. A
// test that probes the repository it runs in (cerberus's porosity probe) read
// "not a repository" from the pre-push while the door's whole clone passed. The
// atom runs in gitReady's snapshot: the /src swap, then the throwaway
// repository, then the atom. The stamp the atom lays follows all of it.
func TestRustCargoTestRunsInATreeGitCanRead(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{".git": "gitdir: /x/.git/worktrees/y\n"}, ".git/HEAD"))
	engine.contents(rustTestNeedle, rustVerdict("rust:cargo-test", "precommit"))
	wantState(t, runAtom(t, "rust:cargo-test", ""), 0)

	run := engine.chain(rustTestNeedle, "contents")
	wantCalls(t, run,
		[]string{"withExec", `args:["git","init","-q","."]`},
		[]string{"withExec", `args:["git","add","-A"]`},
		[]string{"withExec", "expect:ANY", `args:["/atom","/src"]`},
	)
	swap := lastCall(run, "withMountedDirectory", `path:"/src"`)
	gitInit := strings.Index(run, `"git","init"`)
	suite := strings.Index(run, atomExec)
	if swap < 0 || gitInit < 0 || suite < 0 || swap > gitInit || gitInit > suite {
		t.Errorf("the /src swap, the snapshot and the atom must run in that order (swap %d, init %d, atom %d):\n%s",
			swap, gitInit, suite, run)
	}

	// A primary checkout's `.git` is a directory: nothing is rebuilt.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents(rustTestNeedle, rustVerdict("rust:cargo-test", "precommit"))
	wantState(t, runAtom(t, "rust:cargo-test", ""), 0)
	if hasCall(engine.chain(rustTestNeedle, "contents"), "withExec", `"git","init"`) {
		t.Errorf("a primary checkout needs no throwaway repository")
	}
}

// Fetch layer first, then the target held LOCKED for the atom (the remount after
// the fetch so the fetch itself takes no lock): the order in the chain.
func TestRustCargoTestHoldsTheTargetLockedAfterTheFetch(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents(rustTestNeedle, rustVerdict("rust:cargo-test", "precommit"))
	runAtom(t, "rust:cargo-test", "")
	c := engine.chain(rustTestNeedle, "contents")
	fetch := strings.Index(c, `"cargo","fetch","--locked"`)
	lock := lastCall(c, "withMountedCache", `path:"/cache/cargo-target"`, "sharing:LOCKED")
	if fetch < 0 || lock < 0 || lock < fetch {
		t.Errorf("the target is remounted LOCKED after the fetch (fetch %d, lock %d):\n%s", fetch, lock, c)
	}
}

// THE WIT ATOMS' ENVIRONMENT (DEF-B4-wit-1): each in the fetched rust lane, with
// the pinned tools it execs and a release volume of its own. What each asks of
// the tree is tested beside the atom (atoms/rust/wit-*).

const (
	witGuestNeedle   = "rust__wit-guest.json"
	witComposeNeedle = "rust__wit-compose.json"
)

// witVerdict is the verdict a wit atom wrote.
func witVerdict(id string) string { return rustVerdict(id, "prepush") }

// releaseKeys are the release-cache volume keys the last run asked the engine
// for, in order (a cache volume is a query of its own).
func releaseKeys() []string {
	var keys []string
	all := strings.Join(engine.chains(), "\n")
	for _, m := range regexp.MustCompile(`key:"(foundry-cargo-release[^"]*)"`).FindAllStringSubmatch(all, -1) {
		if !slices.Contains(keys, m[1]) {
			keys = append(keys, m[1])
		}
	}
	return keys
}

func witChain(t *testing.T, id, needle string) string {
	t.Helper()
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents(needle, witVerdict(id))
	wantState(t, runAtom(t, id, ""), 0)
	return engine.chain(needle, "contents")
}

// The fetch is provisioning under the default Expect, then the pinned tools, then
// the atom; CARGO_TARGET_DIR is the atom's own volume. wasm-tools is fetched,
// extracted and proved for both; wac for the composer alone.
func TestTheWitAtomsRunOnTheFetchedLayerWithTheirPinnedTools(t *testing.T) {
	member := checks.WasmToolsMember
	for _, tc := range []struct {
		id, needle string
		wac        bool
	}{
		{"rust:wit-guest", witGuestNeedle, false},
		{"rust:wit-compose", witComposeNeedle, true},
	} {
		c := witChain(t, tc.id, tc.needle)
		wantCalls(t, c,
			[]string{"withExec", `args:["cargo","fetch","--locked"]`},
			[]string{"withEnvVariable", `name:"CARGO_TARGET_DIR"`, `value:"` + checks.WitGuestTargetDir + `"`},
			[]string{"withExec", `args:["tar","xzf","/tmp/wasm-tools.tar.gz","-C","/usr/local/bin","--strip-components=1","` + member + `"]`},
			[]string{"withExec", `args:["wasm-tools","--version"]`},
			[]string{"withExec", "expect:ANY", `args:["/atom","/src"]`},
			[]string{"withEnvVariable", `name:"ATOM_OUT"`},
		)
		if hasCall(c, "withExec", `args:["cargo","fetch","--locked"]`, "expect:ANY") || hasCall(c, "withExec", `args:["wasm-tools","--version"]`, "expect:ANY") {
			t.Errorf("%s: provisioning runs under the default Expect:\n%s", tc.id, c)
		}
		if got := hasCall(c, "withFile", `path:"/usr/local/bin/wac"`); got != tc.wac {
			t.Errorf("%s: wac on PATH is %v, want %v:\n%s", tc.id, got, tc.wac, c)
		}
		if pinnedFetch(checks.WasmToolsURL, "id") == "" {
			t.Errorf("%s: the pinned wasm-tools was not fetched:\n%v", tc.id, engine.chains())
		}
		if tc.wac && pinnedFetch(checks.WacURL, "id") == "" {
			t.Errorf("%s: the pinned wac was not fetched:\n%v", tc.id, engine.chains())
		}
		// the atom bounds its own cargo runs; the container carries no bound
		if hasCall(c, "withEnvVariable", `name:"CARGO_BUILD_JOBS"`) {
			t.Errorf("%s: the bound is the atom's, set on its execs:\n%s", tc.id, c)
		}
		// rule 8: neither reads the base
		if hasCall(c, "withEnvVariable", `name:"GATE_BASE"`) || hasCall(c, "withEnvVariable", `name:"ATOM_BASE"`) {
			t.Errorf("%s reads no base:\n%s", tc.id, c)
		}
	}
}

// THE SPLIT: no volume, key or path is shared between the two atoms, and neither
// needs the copy-out helper (the atom carries its own output). The guest holds
// its build volume SHARED and its native test's directory LOCKED, a volume of its
// own nested in it; the composer holds its volume SHARED.
func TestTheWitAtomsShareNoVolume(t *testing.T) {
	guest := witChain(t, "rust:wit-guest", witGuestNeedle)
	gk := releaseKeys()
	compose := witChain(t, "rust:wit-compose", witComposeNeedle)
	ck := releaseKeys()
	// the guest holds a build volume and a test volume, the composer one, and no
	// key is in both chains or is the bare release cache the cast and rust:release hold
	if len(gk) != 2 || len(ck) != 1 {
		t.Fatalf("guest volumes %v, compose volumes %v", gk, ck)
	}
	for _, g := range gk {
		if g == ck[0] || strings.HasSuffix(g, "-wit-compose") {
			t.Errorf("volume %s is shared between the atoms", g)
		}
	}
	slices.Sort(gk) // the engine asks for the volumes in the order it evaluates them
	if !strings.HasSuffix(ck[0], "-"+witComposeVolume) || !strings.HasSuffix(gk[0], "-"+witGuestVolume) || !strings.HasSuffix(gk[1], "-"+witGuestTestVolume) {
		t.Errorf("volumes are keyed by atom: %v %v", gk, ck)
	}
	for _, k := range append(gk, ck...) {
		if strings.HasSuffix(k, "-"+witGuestVolume) || strings.HasSuffix(k, "-"+witGuestTestVolume) || strings.HasSuffix(k, "-"+witComposeVolume) {
			continue
		}
		t.Errorf("%s is the release cache the cast and rust:release hold", k)
	}

	const release, test = "/cache/cargo-release", "/cache/cargo-release/wit-guest-test"
	if test != checks.WitGuestTestTargetDir {
		t.Fatalf("the test dir moved: %s", test)
	}
	if got := mountSharingAt(guest, release, atomExec); got != "SHARED" {
		t.Errorf("guest build volume is %q, want SHARED:\n%s", got, guest)
	}
	if got := mountSharingAt(guest, test, atomExec); got != "LOCKED" {
		t.Errorf("guest native test volume is %q, want LOCKED:\n%s", got, guest)
	}
	if got := mountSharingAt(compose, release, atomExec); got != "SHARED" {
		t.Errorf("compose volume is %q, want SHARED:\n%s", got, compose)
	}
	if hasCall(compose, "withMountedCache", `path:"`+test+`"`) {
		t.Errorf("the composer has no guest test directory:\n%s", compose)
	}
	// the nested directory mounts after its parent, or the parent hides it
	if p, c := strings.Index(guest, `path:"`+release+`"`), strings.Index(guest, `path:"`+test+`"`); p < 0 || c < p {
		t.Errorf("the test volume must be mounted inside the build volume (parent %d, child %d)", p, c)
	}
	for name, c := range map[string]string{"guest": guest, "compose": compose} {
		if strings.Contains(c, "copyout") {
			t.Errorf("%s: the atom carries its own output; no copy-out helper:\n%s", name, c)
		}
	}
}

// Each atom has an environment entry of its own and none names the other: the
// pair has no ordering against each other, so a lane may run them in any order or
// at once.
func TestTheWitAtomsDeclareNoOrderAgainstEachOther(t *testing.T) {
	for _, id := range []string{"rust:wit-guest", "rust:wit-compose"} {
		e, ok := atomEnvironments[id]
		if !ok || e.container == nil || e.bind == nil || e.facts || e.factsWhen != nil {
			t.Errorf("%s: %+v", id, e)
		}
	}
}

// A tool that will not fetch is left off the container, and the atom's own
// version probe settles 2 on it: the lane still builds.
func TestAWitToolThatWillNotFetchIsLeftOff(t *testing.T) {
	for _, tc := range []struct {
		id, needle, url string
		onPath          func(c string) bool
	}{
		{"rust:wit-guest", witGuestNeedle, checks.WasmToolsURL,
			func(c string) bool { return hasCall(c, "withFile", `path:"/tmp/wasm-tools.tar.gz"`) }},
		{"rust:wit-compose", witComposeNeedle, checks.WacURL,
			func(c string) bool { return hasCall(c, "withFile", `path:"/usr/local/bin/wac"`) }},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.contents(tc.needle, witVerdict(tc.id))
		engine.fail(checks.ToolMirrorURL(checks.ToolSHA256[tc.url]), "mirror-cause")
		engine.fail(tc.url, "github-cause")
		wantState(t, runAtom(t, tc.id, ""), 0)
		if c := engine.chain(tc.needle, "contents"); tc.onPath(c) {
			t.Errorf("%s: %s was not fetched and is on PATH:\n%s", tc.id, tc.url, c)
		}
	}
}
