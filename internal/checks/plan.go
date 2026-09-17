package checks

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

// PlanAtoms answers the plan for a selection: what the tree declares, which
// atoms that admits, and which stand down before they start.
//
// goModules is how many Go modules the tree carries, because the go lane is
// declared by a go.mod ANYWHERE the go command would find one (GoModuleDirs
// says why), not by a root entry — so it cannot be read off `entries` like
// every other lane's manifest.
func PlanAtoms(selected []AtomDef, entries []string, goModules int) Plan {
	lanes := LanesOf(entries)
	if goModules > 0 {
		if !hasLane(lanes, LaneGo) {
			lanes = append(lanes, LaneGo)
		}
	} else {
		lanes = withoutLane(lanes, LaneGo)
	}
	p := Plan{Lanes: lanes}
	for _, a := range selected {
		if a.Lane == LaneAny || hasLane(lanes, a.Lane) {
			p.Run = append(p.Run, a)
			continue
		}
		p.Absent = append(p.Absent, a)
	}
	return p
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
