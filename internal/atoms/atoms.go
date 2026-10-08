// Package atoms runs the cheap fleet atoms IN PROCESS, concurrently, inside one
// compiled binary and one container per push.
//
// WHY. Every atom in package main builds its own Dagger container chain, and
// for the atoms that only read the tree that is the whole cost: the question
// ("is any file over 2 MB?") takes microseconds and the container around it
// takes seconds. An atom here is a function over an Input, the runner is a
// worker pool, and the answer is the SAME checks.Verdict value the module
// emits today, so a vector from this package compares field by field with the
// vector from the chains (Compare).
//
// THE THREE STATES ARE UNCHANGED: 0 pass, 1 findings, 2 could not run, plus
// absent (a pass that SAYS nothing was checked). A panic, a deadline and a
// tree that would not enumerate are all 2 and never a pass.
package atoms

import (
	"context"
	"fmt"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// Scope says what part of the tree an atom's question is about.
type Scope int

const (
	// ScopeTree atoms read the whole population. fleet:stop-justifications is
	// one even though it "only" reads tracked files: a justification expires
	// by today's date, so a file nobody touched can turn red.
	ScopeTree Scope = iota
	// ScopeChanged atoms are about the change, not the tree. With an empty
	// change set the runner settles them absent without calling them, and with
	// a change set that would not compute it settles them 2 — an atom that
	// graded "nothing changed" because git would not answer has passed a pull
	// it never looked at.
	ScopeChanged
)

// Input is what every atom is handed: one tree, read once. The git answers are
// computed ONCE by Collect and shared, so thirty atoms do not each fork git.
//
// A QUESTION THAT WOULD NOT ANSWER RIDES AS AN ERROR FIELD, not a failed Run:
// an atom that does not need the change set must still run when git cannot
// compute it, and one that does need it files its own 2.
type Input struct {
	// Root is the repository root on disk.
	Root string
	// Files is the gate's population: what the repository would commit, less
	// the fleet exclude (checks.GatePopulation), sorted, slash-separated and
	// relative to Root.
	Files    []string
	FilesErr error
	// Tracked is `git ls-files`, in git's order — a narrower set than Files
	// (it omits untracked, unignored files) and the one stop-justifications
	// is defined over.
	Tracked    []string
	TrackedErr error
	// Changed is the change set (ChangeSet).
	Changed    []string
	ChangedErr error
	// Origin is the URL the repository was fetched from; "" names no
	// exemption (checks.RepoFromOrigin).
	Origin string
	// Now is the clock the atoms read, so a test says what day it is.
	Now time.Time
}

// RunFunc answers one atom's verdict. The AtomDef is resolved by the registry
// from the catalogue and handed in, so no atom looks its own row up — and none
// can name a row that is not there.
type RunFunc func(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict

// Atom is one registration: a catalogued id, its scope and its function.
type Atom struct {
	ID    string
	Scope Scope
	Run   RunFunc
}

type entry struct {
	def  checks.AtomDef
	atom Atom
}

// Registry is an ordered, validated set of atoms. Its order is the vector's.
type Registry struct {
	entries []entry
}

// NewRegistry validates the atoms and keeps their order. An id missing from
// the catalogue (internal/checks.Atoms), a duplicate id and a nil function are
// errors here, at construction, rather than a panic or a hole in a vector on
// some later gate.
func NewRegistry(atoms ...Atom) (*Registry, error) {
	seen := map[string]bool{}
	reg := &Registry{}
	for _, a := range atoms {
		def, ok := lookup(a.ID)
		switch {
		case !ok:
			return nil, fmt.Errorf("atom %q is not in the catalogue (internal/checks.Atoms)", a.ID)
		case seen[a.ID]:
			return nil, fmt.Errorf("atom %q is registered twice", a.ID)
		case a.Run == nil:
			return nil, fmt.Errorf("atom %q has no run function", a.ID)
		}
		seen[a.ID] = true
		reg.entries = append(reg.entries, entry{def: def, atom: a})
	}
	return reg, nil
}

// IDs answers the registered ids in registry order.
func (r *Registry) IDs() []string {
	ids := make([]string, len(r.entries))
	for i, e := range r.entries {
		ids[i] = e.atom.ID
	}
	return ids
}

// lookup is checks.AtomByID without the panic: this package validates ids and
// reports, it does not crash a gate on an authoring error.
func lookup(id string) (checks.AtomDef, bool) {
	for _, a := range checks.Atoms {
		if a.ID == id {
			return a, true
		}
	}
	return checks.AtomDef{}, false
}

// Builtin is the atoms this package carries, in the order the vector lists
// them. Registration is explicit rather than init()-time so the set a binary
// runs is visible in one place and a test can build a registry of its own.
func Builtin() []Atom {
	return []Atom{
		{ID: "fleet:check-yaml", Scope: ScopeTree, Run: checkYAML},
		{ID: "fleet:check-added-large-files", Scope: ScopeTree, Run: checkAddedLargeFiles},
		{ID: "fleet:check-merge-conflict", Scope: ScopeTree, Run: checkMergeConflict},
		{ID: "fleet:stop-justifications", Scope: ScopeTree, Run: stopJustifications},
	}
}
