package checks

import (
	"fmt"
	"strings"
)

// State is an atom's verdict. THREE STATES, NEVER TWO.
//
// The two-state habit is what this module exists to end: a check that could not
// run — the tool absent, the index unreachable, the ruleset matching no file —
// used to exit 0 and render as Passed, indistinguishable from a clean scan.
// foundry-stocks#4415 is the worked example: every Go and Rust star ran a
// security gate that had never examined a single file and reported success
// every time. `staticcheck` and `govulncheck` already carried the discipline;
// this makes it the type.
type State int

const (
	// StatePass — the atom ran and found nothing.
	StatePass State = 0
	// StateFindings — the atom ran and has something to say.
	StateFindings State = 1
	// StateCannotRun — the atom did not run. NEVER a pass.
	StateCannotRun State = 2
)

// String names the state for a verdict vector.
func (s State) String() string {
	switch s {
	case StatePass:
		return "pass"
	case StateFindings:
		return "findings"
	case StateCannotRun:
		return "cannot-run"
	default:
		return "cannot-run"
	}
}

// StateFor maps a process exit code to the atom's verdict.
//
// EVERY NON-ZERO CODE IS A NON-PASS, and every code that is not 0 or 1 is
// CANNOT RUN rather than findings: a 127 is a missing binary, a 137 is an OOM
// kill, a 143 is a cancelled run. Reading any of those as "findings" would be
// wrong in the safe direction; reading them as a pass is the failure this type
// exists to make unrepresentable.
func StateFor(exit int) State {
	switch exit {
	case 0:
		return StatePass
	case 1:
		return StateFindings
	default:
		return StateCannotRun
	}
}

// Verdict is one atom's answer — the element of the vector the door reads.
type Verdict struct {
	Atom   string `json:"atom"`
	Stage  string `json:"stage"`
	Lane   string `json:"lane"`
	State  int    `json:"state"`
	Result string `json:"result"`
	Reason string `json:"reason,omitempty"`
}

// VerdictOf builds one element of the vector from a raw exit code.
//
// AN ATOM THAT DECLARED ITSELF ABSENT IS NOT REPORTED AS A PASS. Half the
// atoms here find their own absence in the tree rather than off a root
// manifest — no rules/sast, no Dockerfile, no digest reference under
// .forgejo/workflows — and they say so on stdout before exiting 0. Rendering
// that as "pass" flattens "there was nothing to check" into "I checked and it
// was clean", which is the same conflation StateCannotRun exists to prevent,
// one shelf up: measured on ca-sweep-manual-1788973171, 28 of the run's 86
// verdicts read `pass` and `absent=0` while nothing had been examined. The
// state stays 0 — an absence is not a failure — and AbsentVerdict's own shape
// is what it borrows.
func VerdictOf(a AtomDef, exit int, output string) Verdict {
	s := StateFor(exit)
	if s == StatePass {
		if line, ok := AnnouncedAbsence(a.ID, output); ok {
			return Verdict{
				Atom:   a.ID,
				Stage:  a.Stage,
				Lane:   string(a.Lane),
				State:  int(StatePass),
				Result: "absent",
				Reason: line,
			}
		}
	}
	return Verdict{
		Atom:   a.ID,
		Stage:  a.Stage,
		Lane:   string(a.Lane),
		State:  int(s),
		Result: s.String(),
		Reason: reasonFor(a.ID, s, exit, output),
	}
}

// AnnouncedAbsence reports whether the atom's own output declared ABSENT, and
// returns the line that did.
//
// The prefix is the atom's id, deliberately: a script that merely mentions the
// word — a message about some OTHER check's absence, a grep hit in a scanned
// file — must not be read as this atom standing down. Every absence in the
// table is written `<id>: ABSENT - <why>`, which is the shape this reads.
func AnnouncedAbsence(id, output string) (string, bool) {
	prefix := id + ": ABSENT"
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return line, true
		}
	}
	return "", false
}

// AbsentVerdict is the fourth shape and it is still a three-state answer: a lane
// with no surface in the tree reports ABSENT, exits 0, and SAYS SO. Silence
// would be indistinguishable from a scan that found nothing.
func AbsentVerdict(a AtomDef) Verdict {
	return Verdict{
		Atom:   a.ID,
		Stage:  a.Stage,
		Lane:   string(a.Lane),
		State:  int(StatePass),
		Result: "absent",
		Reason: absentReason(a),
	}
}

func absentReason(a AtomDef) string {
	return fmt.Sprintf("%s: ABSENT — no %s at the repository root, so this repo does not build the %s lane. Nothing was checked and nothing needed to be.",
		a.ID, ManifestFor(a.Lane), a.Lane)
}

func reasonFor(id string, s State, exit int, output string) string {
	switch s {
	case StatePass:
		return fmt.Sprintf("%s: PASS", id)
	case StateFindings:
		return fmt.Sprintf("%s: FINDINGS (exit 1)\n%s", id, output)
	default:
		return fmt.Sprintf("%s: CANNOT RUN (exit %d) — a check that could not run is not a check that passed.\n%s", id, exit, output)
	}
}

// Answer turns a verdict into what `dagger check` reads: nil for a pass, an
// error carrying the state and the output for anything else.
func (v Verdict) Answer() (string, error) {
	if State(v.State) == StatePass {
		return v.Reason, nil
	}
	return "", fmt.Errorf("%s", v.Reason)
}
