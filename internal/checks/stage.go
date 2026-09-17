package checks

import (
	"fmt"
	"sort"
	"strings"
)

// THE COMMIT STAGE'S SHAPE, as data (CA master-plan F12). Rob, 2026-09-17: the
// commit runs a basic fanout — secrets, SAST, large files and the rest every
// tree gets — beside a language fanout over what the tree actually contains,
// and "What's in the commit? (Go? Python? Ansible?)" decides which of those
// apply. Each fanout runs to completion; the stage settles on its worst state
// and answers the log of everything that ran, in order.
const (
	GroupBasic    = "basic"
	GroupLanguage = "language"
)

// GroupOf is an atom's fanout: the fleet namespace is the basic checks every
// repository gets; every other namespace is a language or a surface the tree
// may or may not contain (go, python, rust, ts, ops, compose, dies).
func GroupOf(id string) string {
	if strings.HasPrefix(id, "fleet:") {
		return GroupBasic
	}
	return GroupLanguage
}

// StageAtom is one atom's line in a stage's answer.
type StageAtom struct {
	Atom   string
	Group  string
	State  int
	Result string
	Reason string
}

// Stage is a stage's settled answer.
type Stage struct {
	Name string
	// State is the worst state of the atoms that ran: 0 clean, 1 findings,
	// 2 could not run.
	State int
	// Lanes are the language and surface namespaces the tree turned out to
	// contain — the answer to "what's in the commit".
	Lanes []string
	// Ran are the atoms that looked, basic before language.
	Ran []StageAtom
	// Omitted are the atoms that found no surface here, each with the reason,
	// so an atom the planner skipped is never silent.
	Omitted []StageAtom
	// Unreached are the atoms a failing sequence never got to, in the order
	// they would have run. A stage that stops early must say what it did not
	// look at: "everything else passed" and "everything else was skipped" are
	// not the same answer, and only one of them is true here.
	Unreached []string
	// Log is every atom that ran, in Ran's order, then the omitted atoms, one
	// line per distinct reason.
	Log string
}

// Stops reports whether a sequence stops here: an atom that found something or
// could not run. An absence stops nothing — a lane with no surface is not a
// failure, and the atoms behind it still have surfaces of their own.
func Stops(v Verdict) bool { return v.State != 0 && v.Result != "absent" }

// TailAfter is the ids of the atoms after the one at i — what a sequence that
// stopped at i never reached. A pure function because the arithmetic is the
// whole of it: off by one here and a stage claims it ran the atom that stopped
// it, or hides the one after.
func TailAfter(selected []AtomDef, i int) []string {
	var out []string
	for _, rest := range selected[i+1:] {
		out = append(out, rest.ID)
	}
	return out
}

// SettleStage folds a stage's verdicts. Basic before language; within a group,
// the order given. An absent verdict is omitted and never settles the stage.
// The unreached ids are the atoms a fail-fast sequence never ran.
func SettleStage(name string, vs []Verdict, unreached ...string) Stage {
	st := Stage{Name: name, Unreached: unreached}
	lanes := map[string]bool{}
	for _, group := range []string{GroupBasic, GroupLanguage} {
		for _, v := range vs {
			if GroupOf(v.Atom) != group {
				continue
			}
			a := StageAtom{Atom: v.Atom, Group: group, State: v.State, Result: v.Result, Reason: v.Reason}
			if v.Result == "absent" {
				st.Omitted = append(st.Omitted, a)
				continue
			}
			st.Ran = append(st.Ran, a)
			st.State = max(st.State, v.State)
			if group == GroupLanguage {
				ns, _, _ := strings.Cut(v.Atom, ":")
				lanes[ns] = true
			}
		}
	}
	for ns := range lanes {
		st.Lanes = append(st.Lanes, ns)
	}
	sort.Strings(st.Lanes)
	var log strings.Builder
	for _, a := range st.Ran {
		fmt.Fprintf(&log, "── %s · %s ──\n%s\n", a.Atom, a.Result, strings.TrimRight(unprefixed(a.Atom, a.Reason), "\n"))
	}
	if len(st.Omitted) > 0 {
		log.WriteString("── omitted: no surface here ──\n")
		var reasons []string
		atoms := map[string][]string{}
		for _, a := range st.Omitted {
			first, _, _ := strings.Cut(unprefixed(a.Atom, a.Reason), "\n")
			if atoms[first] == nil {
				reasons = append(reasons, first)
			}
			atoms[first] = append(atoms[first], a.Atom)
		}
		for _, r := range reasons {
			fmt.Fprintf(&log, "%s — %s\n", strings.Join(atoms[r], ", "), r)
		}
	}
	if len(st.Unreached) > 0 {
		fmt.Fprintf(&log, "── not reached: the stage stopped before them ──\n%s\n", strings.Join(st.Unreached, ", "))
	}
	st.Log = log.String()
	return st
}

// unprefixed drops the "atom: " an atom's reason opens its lines with. The
// log's header already names the atom. Measured live on 2026-09-17, a Go
// tree's commit stage answered a 51-line log whose last 28 lines were the
// omitted atoms, each naming itself twice, over 8 distinct reasons.
func unprefixed(atom, reason string) string {
	lines := strings.Split(reason, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(l, atom+": ")
	}
	return strings.Join(lines, "\n")
}

// LogTail keeps the last max bytes of a log, saying how much was dropped, so a
// stage's log fits an exec argument.
func LogTail(log string, max int) string {
	if len(log) <= max {
		return log
	}
	return fmt.Sprintf("… %d earlier byte(s) of the log dropped …\n", len(log)-max) + log[len(log)-max:]
}
