package main

import (
	"strings"

	"dagger/foundry-tools/internal/buildlane"
)

// A PROCEDURAL LANE'S RECORD, AS PHASES — the shape the build lane proved and
// the cast lane now shares.
//
// WHY THERE IS A TYPE HERE RATHER THAN A COPY. Both lanes are a sequence of
// named methods answering the same (code, reason) pair the lane settles on, and
// both want the same four things from a record: which phase ran, what it said,
// what it printed, and which phases were never reached. Two copies of that is
// two places for a recorder bug to live, and the second copy is always the one
// nobody updates. Bundle and publish are the same shape when their turn comes.
//
// WHAT IT IS NOT. This does not decide a verdict. run() already answers the
// code the lane settles on; the record explains that answer rather than
// computing a rival one.
type phases struct {
	// group is every atom's group, as the gate's "basic"/"language" fanouts
	// are. One group per lane: these are a SEQUENCE, not a fanout — each phase
	// runs because the one before it held.
	group string
	// order is the phases in the order the lane takes them, and it is what
	// makes Unreached answerable: a lane that stopped at the scan can say the
	// publish and the signature never ran, which is a different fact from
	// their passing.
	order []string
	// fanout marks a run whose atoms are not that sequence — the build lane's
	// bases path, one atom per base. Unreached is then EMPTY rather than
	// wrong: there is no "after" for a base that did not run, because the
	// ones that ran are the ones there were.
	fanout bool
	// atoms are the phases that have sealed, in the order they ran; lines are
	// what the phase now running has said.
	atoms []AtomResult
	lines []string
}

// say records what a phase reported AND prints it through the lane's own
// printer, because both readers matter: the record is what the door folds into
// a verdict, and the pod log is what a person tails while the lane runs.
func (p *phases) say(print func(string), line string) {
	print(line)
	p.lines = append(p.lines, line)
}

// seal ends a phase and files what it answered.
//
// IT IS CALLED ON EVERY OUTCOME, not only on failures. An atom that held is
// evidence — it is how the record says the scan ran and passed rather than
// staying silent about it, and it is what lets Unreached mean "never ran"
// instead of "said nothing".
func (p *phases) seal(atom string, code int, reason string) {
	p.atoms = append(p.atoms, AtomResult{
		Atom:   atom,
		Group:  p.group,
		State:  code,
		Result: phaseResult(code),
		Reason: strings.TrimSpace(reason),
		// NEVER NIL. The door's reader tolerates a null, but an empty list and
		// an absent one read differently to anyone looking at the JSON, and
		// "this phase printed nothing" is a fact worth being able to see.
		Logs: append([]string{}, p.lines...),
	})
	p.lines = nil
}

// stop seals the phase that ended the run and hands its verdict straight back,
// so a call site reads as one statement instead of three.
//
// AN EMPTY atom SEALS NOTHING, which is how a lane says "I held, I simply had
// no more phases to run" — the build lane's pull-time exit, the cast lane's
// dry run. Those phases are then Unreached, which is the honest word: a pull
// did not prove the signature, it never asked for one.
func (p *phases) stop(atom string, code int, reason string) (int, string) {
	if atom != "" {
		p.seal(atom, code, reason)
	}
	return code, reason
}

// phaseResult maps the three states onto the vocabulary the gate's atoms
// already use, so one reader folds every lane.
func phaseResult(code int) string {
	switch code {
	case buildlane.Clean:
		return "pass"
	case buildlane.Findings:
		return "findings"
	default:
		return "cannot-run"
	}
}

// record is the lane's whole answer in the shape the door reads.
func (p *phases) record(stage string, code int) *StageResult {
	unreached := p.unreached()
	return &StageResult{
		Stage:     stage,
		State:     code,
		Lanes:     []string{p.group},
		Atoms:     p.atoms,
		Unreached: unreached,
		Log:       p.renderLog(unreached),
	}
}

// unreached answers the phases that never sealed, in the order they would have
// run. A fanout answers none — see the field's own comment.
func (p *phases) unreached() []string {
	if p.fanout {
		return nil
	}
	sealed := map[string]bool{}
	for _, a := range p.atoms {
		sealed[a.Atom] = true
	}
	var out []string
	for _, name := range p.order {
		if !sealed[name] {
			out = append(out, name)
		}
	}
	return out
}

// renderLog is the record's human half: every phase in order with its verdict,
// then what the run never reached.
func (p *phases) renderLog(unreached []string) string {
	var b strings.Builder
	for _, a := range p.atoms {
		b.WriteString(a.Atom + ": " + a.Result)
		if a.Reason != "" {
			b.WriteString(" — " + a.Reason)
		}
		b.WriteString("\n")
		for _, line := range a.Logs {
			b.WriteString("  " + line + "\n")
		}
	}
	if len(unreached) > 0 {
		b.WriteString("never reached: " + strings.Join(unreached, ", ") + "\n")
	}
	return b.String()
}

// shortSha is the twelve characters every other coordinate in the fleet is
// written with.
//
// NO CONDITIONAL, DELIBERATELY. `if len(sha) > 12` carried a boundary mutant no
// test can kill, because at EXACTLY twelve the two arms agree: sha[:12] of a
// twelve-character string is that string. Slicing to a min has no boundary to
// mutate.
func shortSha(sha string) string { return sha[:min(len(sha), 12)] }
