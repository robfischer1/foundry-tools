package checks

// HOW A TIMED-OUT MUTANT VOTES — one decision for every mutation lane.
//
// A mutant whose tests hang until the runner's timeout kills them was NOTICED
// by the suite: the mutation changed behaviour the tests depend on, and the
// tests did not finish. Stryker and PIT count a timeout as killed for that
// reason, and this fleet's TypeScript (ScoreStryker) and Python (cosmic-ray)
// lanes already did. The Rust lane did not: cargo-mutants exits 3 when any
// mutant times out, the atom read every exit but 0 and 2 as a broken run, and
// one hanging mutant discarded a complete survivor report (anvil@0c5355f,
// 2026-10-02: 51 caught, 1 missed, 1 timeout — settled could-not-run, the
// survivor never reported, the retry ladder re-running a deterministic hang).
//
// THE DEFAULT IS DETECTED, AND IT IS A DEFAULT, NOT A RULING: it was taken by a
// session and is waiting on Rob's confirmation. Flipping it is this one
// constant. With it true a timed-out mutant is reported one per mutant under
// MutantTimeoutCause, as an `excluded` finding, and does not red the lane by
// itself (each lane keeps its own guard against a run that is mostly hangs:
// GoMutationTimeoutBudget, RustTimeoutGuard). With it false a timed-out
// mutant is `unanalyzable` — evidence of nothing — and the Rust lane settles
// any run with one as could-not-run, the counts and the survivors still in
// its reason.
//
// WHY `excluded` AND NOT THE SCHEMA'S MUTATION NOTE. foundry-dies'
// findings.schema.json annotates its `unanalyzable` count "mutation: TimedOut
// + CoveredUnrun + Ungraded"; that note records the go:mutation scorer's
// decision of its day, that a timeout is unmeasured. Under this decision it is
// measured — the suite answered, by hanging — so by the schema's own
// definitions it is not "could not run": it RAN, and it "deliberately does not
// count, with a named cause", which is `excluded` word for word. Not `holds`:
// the schema forbids conflating an exclusion with clean, and every one of
// these costs a whole test timeout per run, which a reader has to see to fix.
// The atom's own state, not its findings, is what the door folds
// (ourea tap.VerdictOfAtom), so this word never reddens or greens a lane.
const TimedOutMutantIsDetected = true

// MutantTimeoutCause is the slug a timed-out mutant's finding groups under, in
// every language that reports one.
const MutantTimeoutCause = "mutant-timeout"

// MutantTimeoutAdvice is what a session should do about one.
const MutantTimeoutAdvice = "a test blocks on a value this mutant changes; make the test fail fast instead of hang — every timed-out mutant costs the whole test timeout on every run"

// timedOutMutantVerdict is a timed-out mutant's finding word under the
// decision: `excluded` when it counts as detected, `unanalyzable` when not.
func timedOutMutantVerdict(detected bool) string {
	if detected {
		return VerdictExcluded
	}
	return VerdictUnanalyzable
}
