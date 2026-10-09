package checks

import (
	"fmt"
	"strings"
	"time"
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
	// Logs are the lines this atom itself produced. NEVER omitempty and never
	// nil: an atom that printed nothing carries [], and "printed nothing" is
	// a different fact from "nobody set this field". Rob's rule is that every
	// atom carries its lines, passing ones included — we don't trust absence
	// as evidence.
	Logs []string `json:"logs"`
	// Truncated says the per-atom cap bit. A cut this module makes SAYS SO,
	// which is the whole difference between this and the silent truncation
	// that started the work (infra #10719).
	Truncated bool `json:"truncated,omitempty"`
	// OriginalBytes is the size of the output BEFORE any cut — the true
	// number, always, so a reader can tell how much they are not seeing.
	OriginalBytes int `json:"original_bytes,omitempty"`
	// Findings are what this atom FOUND, one entry per result — the tier
	// between the verdict above and the lines beside it. Empty for a passing
	// atom and for every atom whose tool's output format nothing parses yet;
	// the reader treats those the same and says which lane it was.
	Findings []Finding `json:"findings,omitempty"`
	// StartedAt and FinishedAt are when the atom's runner was entered and when
	// it answered, as RFC 3339 UTC (see Timed). EMPTY FOR AN ATOM THAT NEVER
	// STARTED — one the plan left absent, one another atom covered — because a
	// time it did not run at is a fact nobody measured.
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	// Gradings are a mutation atom's units and what each one's mutants did
	// (gradings.go) — what a later run may reuse. Empty for every other atom.
	Gradings []Grading `json:"gradings,omitempty"`
	// Audit is a mutation atom's soundness audit (audit.go): "match", or
	// "mismatch: <units>", on a sampled reuse run; empty on every other.
	Audit string `json:"audit,omitempty"`
	// CPUMs is the CPU the atom spent, in milliseconds: its share of the atoms
	// binary's process (a CPU profile, by the atom's goroutine label) plus the
	// programs it ran. NIL IS NOT KNOWN, never zero: a chain's atom, one that
	// never started, one whose profile would not start and that ran no program.
	CPUMs *int64 `json:"cpu_ms,omitempty"`
}

// VerdictOf builds one element of the vector from a raw exit code.
//
// AN ATOM THAT DECLARED ITSELF ABSENT IS NOT REPORTED AS A PASS. Half the
// atoms here find their own absence in the tree rather than off a root
// manifest — no rules/sast, no Dockerfile, no digest reference in
// a compose file — and they say so on stdout before exiting 0. Rendering
// that as "pass" flattens "there was nothing to check" into "I checked and it
// was clean", which is the same conflation StateCannotRun exists to prevent,
// one shelf up: measured on ca-sweep-manual-1788973171, 28 of the run's 86
// verdicts read `pass` and `absent=0` while nothing had been examined. The
// state stays 0 — an absence is not a failure — and AbsentVerdict's own shape
// is what it borrows.
func VerdictOf(a AtomDef, exit int, output string) Verdict {
	s := StateFor(exit)
	// THE ONE CAPTURE POINT, and it is here because this is the only place
	// every atom's raw output is still in hand. reasonFor returns just
	// "<id>: PASS" for a passing atom — the output is DISCARDED there — so a
	// downstream reader trying to recover the lines from Reason would find
	// nothing for exactly the atoms Rob's "all atoms carry logs" decision is
	// about. Capturing here also means none of the ~40 atoms_*.go call sites
	// change.
	logs, truncated, originalBytes := CaptureLogs(output)
	if s == StatePass {
		if line, ok := AnnouncedAbsence(a.ID, output); ok {
			return Verdict{
				Atom:          a.ID,
				Stage:         a.Stage,
				Lane:          string(a.Lane),
				State:         int(StatePass),
				Result:        "absent",
				Reason:        line,
				Logs:          logs,
				Truncated:     truncated,
				OriginalBytes: originalBytes,
			}
		}
	}
	return Verdict{
		Atom:          a.ID,
		Stage:         a.Stage,
		Lane:          string(a.Lane),
		State:         int(s),
		Result:        s.String(),
		Reason:        reasonFor(a.ID, s, exit, output),
		Logs:          logs,
		Truncated:     truncated,
		OriginalBytes: originalBytes,
		// THE FINDINGS ARE EXTRACTED HERE FOR CaptureLogs' REASON, one line up:
		// this is the only place every atom's raw output is still in hand, so it
		// is the only place the parsing can be done ONCE. Extracting at any call
		// site would mean forty of them, each with its own idea of the shape.
		//
		// ONLY FOR A STATE THAT FOUND SOMETHING. A passing atom has nothing to
		// report, and an atom that could not run found nothing either — it never
		// looked, which is the distinction StateCannotRun exists to keep, so
		// manufacturing findings from its error output would put "evidence of
		// nothing" in a field that means "what was found".
		Findings: findingsFor(s, a.ID, output),
	}
}

// findingsFor extracts an atom's findings, and only where findings are what the
// state means.
//
// StateFindings ONLY: a pass found nothing, and a cannot-run never looked. The
// three states are kept apart everywhere else in this module for exactly this
// reason, and a findings list on a cannot-run would read downstream as a defect
// the run proved — when what it actually proves is that nobody measured.
func findingsFor(s State, atomID, output string) []Finding {
	if s != StateFindings {
		return nil
	}
	return FindingsOf(atomID, output)
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
		// An atom that never ran printed nothing. EMPTY, not nil — the
		// distinction is the one this whole feature is built on.
		Logs: []string{},
	}
}

// absentReason says which absence this is, and there are three of them.
//
// THE READER HAS TO BE ABLE TO TELL THEM APART. An atom that stood down
// because the repository carries none of its language is a different fact from
// one that stood down because the repository carries the language but does not
// package it — and a reader who cannot tell which cannot tell whether the gate
// is right. So the manifest sentence is reserved for the atoms that actually
// asked for a manifest, and the lanes declared by their files say so.
func absentReason(a AtomDef) string {
	if a.NeedsManifest {
		// The lane may well be running; this atom's subject is the PROJECT,
		// and the project is what the root manifest declares. It does not say
		// "does not build the lane" — that sentence is true of python:release
		// and false of python:mypy, which is not a build and still cannot work
		// without the dependency set the manifest declares.
		return fmt.Sprintf("%s: ABSENT — no %s at the repository root, so this repo declares no %s project for it to read. The lane's lint and tests still run. Nothing was checked here and nothing needed to be.",
			a.ID, ManifestFor(a.Lane), a.Lane)
	}
	if source, ok := laneDeclaredByFiles[a.Lane]; ok {
		// Declared by its FILES, so its absence is not a root-only statement.
		return fmt.Sprintf("%s: ABSENT — no %s anywhere in the tree (vendor/, testdata/ and _ or . directories do not count), so this repo carries no %s to check. Nothing was checked and nothing needed to be.",
			a.ID, source, a.Lane)
	}
	return fmt.Sprintf("%s: ABSENT — no %s at the repository root, so this repo does not build the %s lane. Nothing was checked and nothing needed to be.",
		a.ID, ManifestFor(a.Lane), a.Lane)
}

// laneDeclaredByFiles names what a lane declared BY ITS FILES looks for, for the atoms
// that did not ask for a manifest. A lane absent from this map is declared by
// its root manifest alone and gets the manifest sentence.
var laneDeclaredByFiles = map[Lane]string{
	LaneGo:     "go.mod",
	LanePython: ".py file",
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

// TimeLayout is how an atom's start and finish are written: RFC 3339 in UTC,
// to the millisecond. Most atoms answer in well under a second from a warm
// cache, so whole seconds would make half the fleet's timings read zero.
const TimeLayout = "2006-01-02T15:04:05.000Z07:00"

// Timed stamps a verdict with when its atom started and finished. The times
// are converted to UTC here, so a runner whose clock carries a zone still
// writes the one the record's readers expect.
func Timed(v Verdict, started, finished time.Time) Verdict {
	v.StartedAt = started.UTC().Format(TimeLayout)
	v.FinishedAt = finished.UTC().Format(TimeLayout)
	return v
}
