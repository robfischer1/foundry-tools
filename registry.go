package main

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/checks"
)

// atomFn is one typed atom: given the run it is part of, it builds its chain
// and answers its verdict. It never returns an error — an atom that could not
// run answers state 2, and the vector keeps it visible.
type atomFn func(ctx context.Context, r *run) checks.Verdict

// registry maps a catalogue id to its runner. Each atoms_<lane>.go registers
// its own in an init(); the catalogue row (checks.Atoms) stays the ONE
// definition of what exists, and this is the ONE definition of how it runs.
var registry = map[string]atomFn{}

// register binds a runner to a catalogue id. Two runners for one id, or a
// runner for an id the catalogue does not carry, is an authoring error and
// panics at load — `dagger functions` fails, and so does every gate, loudly,
// before any verdict is answered.
//
// THE OTHER DIRECTION CANNOT BE SEEN FROM HERE. A catalogue row that no
// atoms_*.go registers is an atom `dagger check -l` lists and verdictFor
// errors on, and no amount of load-time checking finds it because nothing
// calls register() with it. internal/checks/registry_test.go reads these calls
// out of the source and holds both directions at once.
func register(id string, fn atomFn) {
	if !checks.AtomExists(id) {
		panic(fmt.Sprintf("registry: %q is not in the catalogue", id))
	}
	if _, dup := registry[id]; dup {
		panic(fmt.Sprintf("registry: %q registered twice", id))
	}
	registry[id] = fn
}
