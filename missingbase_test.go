package main

import (
	"context"
	"strings"
	"testing"
)

// A BASE THE DOOR NAMED AND THE HISTORY LACKS IS LOUD on the door's own tree.
// Every diff-scoped mutation atom, handed a tree the engine fetched (repo set)
// and a base that rev-parse cannot find even after withBase fetched it,
// settles could-not-run naming the base — never the clean "no usable PR base"
// stand-down, which would pass a pull having mutated nothing. The same miss on
// a local snapshot (no repo) still stands down 0, as the atoms' own
// stand-down tables hold.
func TestMutationAtomsFailLoudOnAFetchedTreeMissingItsBase(t *testing.T) {
	const repo = "http://door:8215/star.git"
	cases := []struct {
		atom   string
		script func(map[string]string)
		needle string
	}{
		{"go:mutation", scriptGoMutation, `"git","rev-parse","--verify","--quiet","abc123^{commit}"`},
		{"python:mutation", scriptPythonMutation, pyBaseNeedle},
		{"rust:mutation", scriptRustMutation, rustBaseNeedle},
		{"ts:mutation", scriptTSMutation, tsBaseNeedle},
	}
	for _, c := range cases {
		t.Run(c.atom, func(t *testing.T) {
			c.script(nil)
			engine.exitCode(c.needle, 1)
			v := registry[c.atom](context.Background(), newRun(dag.Directory(), repo, "abc123"))
			wantState(t, v, 2, "CANNOT RUN - the base abc123 is not in this history, though the door named it and it was fetched")
			if engine.chain(mergeBaseNeedle) != "" {
				t.Errorf("went on to the merge base after the base was not found")
			}
			// The base WAS asked for from the door, in the same chain that
			// then could not find it: the loud answer is about a real fetch.
			fetch := `"fetch","--quiet","--no-tags","` + repo + `","abc123"`
			if !strings.Contains(engine.chain(c.needle), fetch) {
				t.Errorf("the base was not fetched from the door before it was looked for: %s", engine.chain(c.needle))
			}

			// The same miss on a local snapshot stands down clean.
			c.script(nil)
			engine.exitCode(c.needle, 1)
			wantState(t, registry[c.atom](context.Background(), newRun(dag.Directory(), "", "abc123")), 0)
		})
	}
}

// missingBase itself: the door's tree is loud and names the base; a local
// snapshot gets the atom's own stand-down words back, untouched.
func TestMissingBaseIsLoudOnlyOnTheDoorsTree(t *testing.T) {
	state, why := newRun(dag.Directory(), "http://door:8215/star.git", "b4se").missingBase("stand down")
	if state != 2 || !strings.HasPrefix(why, "CANNOT RUN - the base b4se is not in this history") {
		t.Errorf("fetched tree: got %d %q", state, why)
	}
	state, why = newRun(dag.Directory(), "", "b4se").missingBase("stand down")
	if state != 0 || why != "stand down" {
		t.Errorf("local snapshot: got %d %q", state, why)
	}
}
