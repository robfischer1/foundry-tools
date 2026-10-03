package checks

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// A MUTATION RUN SAYS WHAT IT GRADED, PER UNIT, SO THE NEXT RUN NEED NOT. The
// gradings ride the atom's verdict into the run record; Daedalus stores them at
// settle (erebus.mutation_gradings), and a later lane whose unit has the same
// key — unit, content hash H, change ranges R, engine E (internal/unitkey) —
// may take the stored outcomes instead of grading the unit again. Equal H and
// R mean equal file contents and equal scope, so a deterministic tool makes the
// same mutants; equal E means the same tool grades them the same way.
//
// ONLY A TRUSTED RUN'S GRADINGS ARE REUSABLE. A run whose own controls did not
// clear it (a broken canary, a classifier that did not answer, a timeout-heavy
// run, a tool that exited wrong) stores its gradings with reusable=false: they
// still say what was seen, and nobody may skip work on their strength. A unit
// with a timed-out mutant is never reusable either — a timeout is a load-
// dependent outcome, and its unit is graded again.
//
// KILLED AND INERT MUTANTS ARE COUNTS. Everything else is listed — every
// mutant that produced a finding — because a reused grading must rebuild the
// same findings a cold run would have made.

// The outcomes a listed mutant can carry.
const (
	OutcomeKilled       = "killed"
	OutcomeInert        = "inert"
	OutcomeLived        = "lived"
	OutcomeNotCovered   = "not-covered"
	OutcomeCoveredUnrun = "covered-unrun"
	OutcomeTimedOut     = "timed-out"
	OutcomeForgiven     = "forgiven"
	OutcomeUngraded     = "ungraded"
	OutcomeCaught       = "caught"
	OutcomeMissed       = "missed"
	OutcomeUnviable     = "unviable"
)

// Why a grading is not reusable.
const (
	WhyNotTimeout   = "timeout"
	WhyNotUntrusted = "run_untrusted"
	WhyNotUnkeyable = "unkeyable"
)

// ScoredMutant is one mutant as a scorer decided it, with its file relative to
// the repository root.
type ScoredMutant struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Op      string `json:"op"`
	Outcome string `json:"outcome"`
	// Status is the tool's word for the mutant (after the covered-unrun
	// correction), Detail what the scorer added: the forgiveness class.
	Status string `json:"status,omitempty"`
	Detail string `json:"detail,omitempty"`
	// Disputed is a NOT COVERED mutant on a line the lane's own profile says
	// ran (GoMutationScore.ProfileDisagrees).
	Disputed bool `json:"disputed,omitempty"`
}

// GradingCounts are a unit's mutants by outcome.
type GradingCounts struct {
	Generated    int `json:"generated"`
	Killed       int `json:"killed"`
	Lived        int `json:"lived"`
	NotCovered   int `json:"not_covered"`
	TimedOut     int `json:"timed_out"`
	CoveredUnrun int `json:"covered_unrun"`
	Forgiven     int `json:"forgiven"`
	Ungraded     int `json:"ungraded"`
	Inert        int `json:"inert"`
	Caught       int `json:"caught"`
	Missed       int `json:"missed"`
	Unviable     int `json:"unviable"`
	Disputed     int `json:"disputed"`
}

// add counts one mutant under its outcome. An outcome nobody names is counted
// as generated and nothing else, which is what the scorers do with a status
// they do not know.
func (c *GradingCounts) add(m ScoredMutant) {
	c.Generated++
	if m.Disputed {
		c.Disputed++
	}
	field := map[string]*int{
		OutcomeKilled: &c.Killed, OutcomeInert: &c.Inert, OutcomeLived: &c.Lived,
		OutcomeNotCovered: &c.NotCovered, OutcomeCoveredUnrun: &c.CoveredUnrun,
		OutcomeTimedOut: &c.TimedOut, OutcomeForgiven: &c.Forgiven, OutcomeUngraded: &c.Ungraded,
		OutcomeCaught: &c.Caught, OutcomeMissed: &c.Missed, OutcomeUnviable: &c.Unviable,
	}[m.Outcome]
	if field != nil {
		*field++
	}
}

// listed reports an outcome whose mutants a grading names one by one.
func listed(outcome string) bool {
	return !slices.Contains([]string{OutcomeKilled, OutcomeInert, OutcomeCaught, OutcomeUnviable}, outcome)
}

// Grading is one unit's share of one mutation run.
type Grading struct {
	Lang     string         `json:"lang"`
	Unit     string         `json:"unit"`
	Hash     string         `json:"hash"`
	Ranges   string         `json:"ranges"`
	Engine   string         `json:"engine"`
	Closure  []string       `json:"closure"`
	Reusable bool           `json:"reusable"`
	WhyNot   string         `json:"why_not,omitempty"`
	Counts   GradingCounts  `json:"counts"`
	Mutants  []ScoredMutant `json:"mutants"`
}

// UnitKey is a unit's key as the lane computed it. Err is why it could not
// be computed; such a unit is graded and stored unkeyable.
type UnitKey struct {
	Unit    string
	Hash    string
	Ranges  string
	Closure []string
	Err     string
}

// BuildGradings splits one run's mutants into its units' gradings, one per
// key in keys order, plus one unkeyable grading per unit a mutant fell in that
// no key names. owner maps a file to its unit. trusted is whether the run's
// own controls cleared it.
func BuildGradings(lang, engine string, keys []UnitKey, owner func(file string) (string, bool), mutants []ScoredMutant, trusted bool) []Grading {
	out := make([]Grading, 0, len(keys))
	at := map[string]int{}
	for _, k := range keys {
		at[k.Unit] = len(out)
		g := Grading{Lang: lang, Unit: k.Unit, Hash: k.Hash, Ranges: k.Ranges, Engine: engine, Closure: k.Closure, Mutants: []ScoredMutant{}}
		if k.Err != "" {
			g.WhyNot = WhyNotUnkeyable
		}
		out = append(out, g)
	}
	for _, m := range mutants {
		unit, ok := owner(m.File)
		i, known := at[unit]
		if !ok || !known {
			at[unit] = len(out)
			i = len(out)
			out = append(out, Grading{Lang: lang, Unit: unit, Engine: engine, WhyNot: WhyNotUnkeyable, Mutants: []ScoredMutant{}})
		}
		out[i].Counts.add(m)
		if listed(m.Outcome) {
			out[i].Mutants = append(out[i].Mutants, m)
		}
	}
	for i := range out {
		g := &out[i]
		slices.SortFunc(g.Mutants, compareMutants)
		switch {
		case g.WhyNot != "":
		case !trusted:
			g.WhyNot = WhyNotUntrusted
		case g.Counts.TimedOut > 0:
			g.WhyNot = WhyNotTimeout
		}
		g.Reusable = g.WhyNot == ""
	}
	return out
}

// compareMutants orders mutants by where they are, then by what they change.
func compareMutants(a, b ScoredMutant) int {
	return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Col, b.Col),
		cmp.Compare(a.Op, b.Op), cmp.Compare(a.Outcome, b.Outcome))
}

// GoGradingTrusted is whether a Go mutation run's gradings may be reused: its
// verdict measured something (not could-not-run) and its harness control
// answered OK. A canary that could not be read has not cleared the runner.
func GoGradingTrusted(state int, canary string) bool {
	return state < 2 && canary == CanaryOK
}

// rustListLine is one line of a mutants.out outcome list, with its column:
// `crates/a/src/x.rs:148:5: replace poll_ms -> i32 with -1`.
var rustListLine = regexp.MustCompile(`^(\S+?):(\d+)(?::(\d+))?: (.+)$`)

// RustScored reads cargo-mutants' four outcome lists into scored mutants. A
// line that does not parse keeps its text as the operator and no file, so it
// lands in an unkeyable grading rather than vanishing.
func RustScored(missed, caught, unviable, timeout string) []ScoredMutant {
	var out []ScoredMutant
	for _, list := range []struct{ text, outcome string }{
		{missed, OutcomeMissed}, {caught, OutcomeCaught}, {unviable, OutcomeUnviable}, {timeout, OutcomeTimedOut},
	} {
		for _, ln := range strings.Split(list.text, "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" {
				continue
			}
			m := ScoredMutant{Op: ln, Outcome: list.outcome}
			if p := rustListLine.FindStringSubmatch(ln); p != nil {
				m.File, m.Op = p[1], p[4]
				m.Line, _ = strconv.Atoi(p[2])
				m.Col, _ = strconv.Atoi(p[3])
			}
			out = append(out, m)
		}
	}
	return out
}
