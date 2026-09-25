package checks

import (
	"slices"
	"strings"
)

// THE PLANNER (CA master-plan F12). Rob's shape for the commit stage names it
// as its own step: "NEW: What's in the commit? (Go? Python? Ansible? Identify
// atoms that apply)". Until now every atom answered that question for itself —
// each one read the repository root, each one decided its lane was absent, and
// the tree was read once per atom to reach the same answer. The question is
// about the TREE, not about the atom, so it is asked once, before anything
// runs, and the answer is a value the stage can show.
//
// WHAT THE PLANNER DOES NOT DECIDE. The surface namespaces (ops, compose,
// dies — SurfaceNamespaces) find their surface INSIDE the tree, not at its
// root: an ops tree is flux/ or ansible/playbooks or a chezmoi source, and the
// atom that knows how to look is the atom itself. Those are planned to run and
// answer their own ABSENT, which is why a stage's omitted list is longer than
// the planner's.

// Plan is what a stage decided before it ran anything.
type Plan struct {
	// Lanes are the language lanes the tree's root declares, sorted.
	Lanes []Lane
	// Run are the atoms whose lane has a surface here, in selection order.
	Run []AtomDef
	// Absent are the atoms whose lane the tree does not declare. They never
	// reach a container; each answers AbsentVerdict, with the reason that
	// names the manifest it looked for.
	Absent []AtomDef
}

// Tree is what the planner knows about the repository before anything runs.
//
// IT IS A VALUE BECAUSE THE QUESTION KEPT GROWING. The planner took
// `(entries, goModules int)` when only the go lane was declared by its files;
// python became the second on 2026-09-25 and a third positional int would have
// been the point where nobody could read the call site. The struct also says
// the thing the comment at the top of this file says: the question is about
// the TREE, so the argument is the tree.
type Tree struct {
	// Entries is the repository ROOT, which is where a lane's manifest — and
	// so its build — is declared.
	Entries []string
	// GoModules is how many Go modules the tree carries: the go lane is
	// declared by a go.mod ANYWHERE the go command would find one
	// (GoModuleDirs says why), not by a root entry.
	GoModules int
	// PythonFiles is how many .py files the tree carries that count as the
	// repository's own (PythonFiles says which do not).
	PythonFiles int
}

// PlanAtoms answers the plan for a selection: what the tree declares, which
// atoms that admits, and which stand down before they start.
//
// A LANE IS DECLARED BY ITS FILES; A BUILD IS DECLARED BY ITS MANIFEST. Both
// halves are needed and they are not the same question. go.mod and .py
// anywhere declare the go and python lanes, so lint and test reach a tree that
// carries the language in any shape. An atom whose subject is the PROJECT says
// NeedsManifest and stands down without the root manifest even inside a lane
// that is running — see AtomDef.NeedsManifest for why the two must not be one
// test.
func PlanAtoms(selected []AtomDef, tree Tree) Plan {
	p := Plan{Lanes: LanesOfTree(tree)}
	lanes := p.Lanes
	for _, a := range selected {
		switch {
		case a.Lane == LaneAny:
		case !hasLane(lanes, a.Lane):
			p.Absent = append(p.Absent, a)
			continue
		case a.NeedsManifest && !DeclaresManifest(tree.Entries, ManifestFor(a.Lane)):
			p.Absent = append(p.Absent, a)
			continue
		}
		p.Run = append(p.Run, a)
	}
	return p
}

// LanesOfTree is every lane this tree declares, sorted — the planner's own
// answer, exported so nothing else has to reconstruct it.
//
// `Lanes` (the verb a session types to ask what the gate would run here) used
// to build this itself off the root entries, which meant it could disagree
// with the gate about the repository in front of it — and did, the moment a
// lane stopped being read from a root manifest. One function, two callers.
func LanesOfTree(tree Tree) []Lane {
	lanes := LanesOf(tree.Entries)
	if tree.GoModules > 0 {
		if !hasLane(lanes, LaneGo) {
			lanes = append(lanes, LaneGo)
		}
	} else {
		lanes = withoutLane(lanes, LaneGo)
	}
	// Additive, never subtractive, and that is the asymmetry with go above: a
	// root pyproject.toml declares the lane on its own (that is what a python
	// star is) and a .py file declares it too. Removing the lane from a repo
	// that has a manifest and no .py yet would take python:release off a star
	// mid-scaffold.
	if tree.PythonFiles > 0 && !hasLane(lanes, LanePython) {
		lanes = append(lanes, LanePython)
	}
	// slices.Sort, not a comparator of our own: a `<` written here is a `<=`
	// away from an equivalent mutant, because lanes are unique and the sort
	// never sees a tie. The line with nothing to say is the line not written.
	slices.Sort(lanes)
	return lanes
}

// DeclaresManifest reports whether this root manifest is one of the entries.
//
// Dagger names a root entry with a leading "./" in some contexts and without
// in others — PythonSourceDirs carries the same note — so both spellings
// answer yes and the result is a fact about the repository rather than about
// which call produced the list.
func DeclaresManifest(entries []string, manifest string) bool {
	if manifest == "" {
		return false
	}
	for _, e := range entries {
		if strings.TrimPrefix(e, "./") == manifest {
			return true
		}
	}
	return false
}

func hasLane(lanes []Lane, l Lane) bool {
	for _, x := range lanes {
		if x == l {
			return true
		}
	}
	return false
}

func withoutLane(lanes []Lane, l Lane) []Lane {
	out := lanes[:0]
	for _, x := range lanes {
		if x != l {
			out = append(out, x)
		}
	}
	return out
}
