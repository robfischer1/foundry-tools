package main

import (
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
	rustClippyNeedle   = "rust__cargo-clippy.json"
	rustTestNeedle     = "rust__cargo-test.json"
	rustMutationNeedle = "rust__mutation.json"
	atomExec           = `"/atom","/src"`
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
