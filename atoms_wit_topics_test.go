// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

var witTopicsPaths = map[string]string{
	"prime/ourea-config.yaml": "",
	"tools/wit-topics":        "",
}

// THE TREE'S CHECKER RUNS, UNEMBEDDED, WITH NO PACKAGE: it hashes files.
func TestWitTopicsRunsTheTreesOwnCheckerWithNoPackages(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(witTopicsPaths))
	wantState(t, runAtom(t, "fleet:wit-topics", ""), 0)

	c := engine.chain("tools/wit-topics", "exitCode")
	wantCalls(t, c,
		[]string{"withExec", `args:["uv","--version"]`},
		[]string{"withExec", "expect:ANY", `"uv","run","--no-project","--quiet","python3","tools/wit-topics"`},
	)
	if strings.Contains(c, "withNewFile") || strings.Contains(c, "--with") {
		t.Errorf("fleet:wit-topics embedded a script or asked uv for a package:\n%s", c)
	}
}

// 0 current, 1 stale, 2 could not run: the script's ladder is the verdict.
func TestWitTopicsPassesTheExitCodeStraightThrough(t *testing.T) {
	for code, want := range map[int]int{0: 0, 1: 1, 2: 2} {
		engine.reset()
		engine.withTree(fleetTree(witTopicsPaths))
		engine.exitCode(`"python3","tools/wit-topics"`, code)
		wantState(t, runAtom(t, "fleet:wit-topics", ""), want)
	}
}

func TestWitTopicsIsAbsentOutsideTheFluxTree(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{"tools/wit-topics": ""}, "prime/"))
	wantState(t, runAtom(t, "fleet:wit-topics", ""), 0, "ABSENT", "not the fleet's flux tree")
	fleetNoContainer(t, "absence decided from the Directory")
}

// A flux tree without its checker is not an absence: the gate would go quiet
// the day the script moved.
func TestWitTopicsCannotRunOnTheFluxTreeWithoutItsChecker(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{"prime/ourea-config.yaml": ""}, "tools/wit-topics"))
	wantState(t, runAtom(t, "fleet:wit-topics", ""), 2, "CANNOT RUN", "tools/wit-topics is absent")
}

// A tree that will not list is unknown, not absent: "found nothing" and "could
// not look" must not print the same.
func TestWitTopicsRefusesATreeItCannotEnumerate(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail("{directory{entries}}", "the tree went away")
	wantState(t, runAtom(t, "fleet:wit-topics", ""), 2,
		"CANNOT RUN", "would not enumerate", "the tree went away")
}
