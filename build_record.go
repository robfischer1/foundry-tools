package main

import (
	"strings"

	"dagger/foundry-tools/internal/buildlane"
)

// THE BUILD LANE'S RECORD, AS PHASES. Until now the lane answered the door with
// an exit code and nothing else: `build holds — no atom record — settle only`
// was the whole of what ci_logs could say about a build, at every depth. A red
// build named no step, so the only way to learn which one broke was to read 400
// lines of pod log.
//
// THE PHASES ARE NOT INVENTED, and that is the reason this shape and not
// another. run() was already a sequence of named methods — detect, stageRelease,
// verify, publish, sign, permit — each answering the same (code, reason) pair the
// lane settles on. This records what they already answer instead of asking them
// to answer differently, so the atom names are the code's own vocabulary rather
// than a second one laid over it.
//
// A ONE-ATOM RECORD WAS THE ALTERNATIVE and was rejected for being barely more
// than the exit code: it would carry a state and a reason the settle's log tail
// already carries, and depth 3 would still have nothing to say.

// buildGroup is every build atom's group, as the gate's "basic"/"language"
// fanouts are. One group, because the phases are a SEQUENCE and not a fanout:
// each runs because the one before it held.
const buildGroup = "build"

// buildPhases is the star path in the order run() takes it, and it is what makes
// Unreached answerable: a lane that stopped at the scan can say the publish,
// signature and permit never ran, which is a different fact from their passing.
//
// THE BASES PATH IS NOT HERE, deliberately — it is a fanout over whatever sits
// under bases/, one atom per base (baseAtom), and it replaces the star path
// entirely rather than joining it. A repo has bases or it has a star image; no
// run takes both.
var buildPhases = []string{
	"build:preflight",
	"build:dependencies",
	"build:detect",
	"build:release",
	"build:image",
	"build:verify",
	"build:publish",
	"build:sign",
	"build:permit",
}

// baseAtom names one base's whole build, scan, publish and promote.
func baseAtom(base string) string { return "build:base:" + base }

// say records what a phase reported AND prints it, because both readers matter:
// the record is what the door folds into a verdict, and the pod log is what a
// person tails while the lane runs. A phase that printed nothing carries [].
func (l *buildLane) say(format string, args ...any) {
	line := sayf(format, args...)
	l.lines = append(l.lines, line)
}

// seal ends a phase and files what it answered.
//
// IT IS CALLED ON EVERY OUTCOME, not only on failures. An atom that held is
// evidence — it is how the record says the scan ran and passed rather than
// staying silent about it, and it is what lets Unreached mean "never ran"
// instead of "said nothing".
func (l *buildLane) seal(atom string, code int, reason string) {
	l.atoms = append(l.atoms, AtomResult{
		Atom:   atom,
		Group:  buildGroup,
		State:  code,
		Result: buildResult(code),
		Reason: strings.TrimSpace(reason),
		// NEVER NIL. The door's reader tolerates a null, but an empty list and
		// an absent one read differently to anyone looking at the JSON, and
		// "this phase printed nothing" is a fact worth being able to see.
		Logs: append([]string{}, l.lines...),
	})
	l.lines = nil
}

// buildResult maps the lane's three states onto the vocabulary the gate's atoms
// already use, so one reader folds both.
func buildResult(code int) string {
	switch code {
	case buildlane.Clean:
		return "pass"
	case buildlane.Findings:
		return "findings"
	default:
		return "cannot-run"
	}
}

// record is the lane's whole answer in the shape the door reads, built from the
// phases that sealed.
//
// THE STATE IS THE WORST PHASE'S, never a fourth thing. run() already answers
// the code the lane settles on and this does not second-guess it — the record
// explains that verdict, it does not compute a rival one.
func (l *buildLane) record(stage string, code int) *StageResult {
	unreached := l.unreached()
	return &StageResult{
		Stage:     stage,
		State:     code,
		Lanes:     []string{buildGroup},
		Atoms:     l.atoms,
		Unreached: unreached,
		Log:       l.renderLog(unreached),
	}
}

// unreached answers the star phases that never sealed, in the order they would
// have run. A bases run answers none: its atoms are a fanout, so there is no
// "after" for a base that did not run — the ones that ran are the ones there
// were.
func (l *buildLane) unreached() []string {
	for _, a := range l.atoms {
		if strings.HasPrefix(a.Atom, "build:base:") {
			return nil
		}
	}
	sealed := map[string]bool{}
	for _, a := range l.atoms {
		sealed[a.Atom] = true
	}
	var out []string
	for _, p := range buildPhases {
		if !sealed[p] {
			out = append(out, p)
		}
	}
	return out
}

// renderLog is the record's human half: every phase in order with its verdict,
// then what the run never reached.
func (l *buildLane) renderLog(unreached []string) string {
	var b strings.Builder
	for _, a := range l.atoms {
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

// stop seals the phase that ended the run and hands its verdict straight back,
// so a call site reads as one statement instead of three.
//
// AN EMPTY atom SEALS NOTHING, which is the pull-time exit: the run held, it
// simply had no more phases to run because publishing is the landing's. Those
// phases are then Unreached, and that is the honest word for them — a pull did
// not prove the signature, it never asked for one.
func (l *buildLane) stop(atom string, code int, reason string) (int, string) {
	if atom != "" {
		l.seal(atom, code, reason)
	}
	return code, reason
}

// shortSha is the twelve characters every other coordinate in the fleet is
// written with.
func shortSha(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
