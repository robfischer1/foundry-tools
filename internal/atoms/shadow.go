package atoms

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"dagger/foundry-tools/internal/checks"
)

// THE SHADOW COMPARISON. The binary's vector and the chains' vector for the
// same tree are held against each other here, and the result is TEXT: nothing
// in this file returns a state, a lane or an error that a gate could read, so
// the comparison cannot turn a pass red or a red pass.

// Diff is one atom whose two verdicts are not the same value.
type Diff struct {
	Atom string
	// Fields names what differs, from the verdict's JSON names.
	Fields []string
	// StateDiffers is the part that matters: the two answered differently
	// about the tree. A text-only difference is the tools' own wording (a
	// pre-commit hook's lines against this package's) and is not a verdict
	// that moved.
	StateDiffers  bool
	Today, Shadow checks.Verdict
}

// Report is the comparison: who agreed, who differed, who one side lacks.
type Report struct {
	Agree         []string
	Differ        []Diff
	MissingShadow []string
	MissingToday  []string
	// Dry is the DRY fleet:witness comparison, kept out of every count above: the
	// binary asked nothing, so it has no verdict to agree or differ with. Nil when
	// the binary's witness was not dry or the chain did not run it.
	Dry *DryWitness
	// Voter says which side's verdicts the lane graded on: "binary" or "chains".
	// The other side is the non-voting comparator. Empty is not printed.
	Voter string
	// Elapsed is the shadow's own wall time, both sides included; zero is
	// unmeasured and is not printed.
	Elapsed time.Duration
}

// DryWitness is the dry witness held against the chain's real one: the files the
// binary would have asked about, the files the chain did ask about, and the
// counts of what neither was shown.
type DryWitness struct {
	Would, Asked []string
	// Missing are asked by the chain and not listed by the binary; Extra the
	// other way round.
	Missing, Extra []string
	// SkipsComparable is false when the chain's reason carries no skipped
	// clauses (a finding or a could-not-consult reason does not), so equal
	// counts cannot be claimed.
	SkipsComparable bool
	SkipsAgree      bool
	// Truncated says the chain's table did not fit its log, so its paths are a
	// prefix and no agreement is claimed.
	Truncated bool
}

// witnessText is what a verdict said. A passing atom's words ride in the logs
// and its reason is "<id>: PASS"; any other state carries them in both, so the
// logs are read when there are any and never added to the reason.
func witnessText(v checks.Verdict) string {
	if len(v.Logs) > 0 {
		return strings.Join(v.Logs, "\n")
	}
	return v.Reason
}

// isDryWitness reports whether v is the binary's dry fleet:witness.
func isDryWitness(v checks.Verdict) bool {
	return v.Atom == "fleet:witness" && strings.Contains(witnessText(v), checks.WitnessDryMark)
}

// dryWitness holds a dry verdict against the chain's.
func dryWitness(chain, dry checks.Verdict) *DryWitness {
	d := &DryWitness{
		Would:     checks.WitnessDryPaths(witnessText(dry)),
		Asked:     checks.WitnessedPaths(witnessText(chain)),
		Truncated: chain.Truncated,
	}
	d.Missing = minus(d.Asked, d.Would)
	d.Extra = minus(d.Would, d.Asked)
	cs, cv, ct := checks.WitnessSkipCounts(witnessText(chain))
	ds, dv, dt := checks.WitnessSkipCounts(witnessText(dry))
	d.SkipsComparable = chain.State == int(checks.StatePass)
	d.SkipsAgree = cs == ds && cv == dv && ct == dt
	return d
}

// minus is the strings of a that b lacks, in a's order.
func minus(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}

// line is the one line the report carries.
func (d DryWitness) line() string {
	paths := "paths agree"
	switch {
	case d.Truncated:
		paths = "paths not comparable (the chain's table was truncated)"
	case len(d.Missing)+len(d.Extra) > 0:
		paths = fmt.Sprintf("paths differ: +%d -%d", len(d.Extra), len(d.Missing))
		if len(d.Extra) > 0 {
			paths += " (+ " + strings.Join(d.Extra, ", ") + ")"
		}
		if len(d.Missing) > 0 {
			paths += " (- " + strings.Join(d.Missing, ", ") + ")"
		}
	}
	skips := "skipped/vendored/tests agree"
	switch {
	case !d.SkipsComparable:
		skips = "skipped/vendored/tests not comparable (the chain's reason carries none)"
	case !d.SkipsAgree:
		skips = "skipped/vendored/tests differ"
	}
	return fmt.Sprintf("fleet:witness (dry): would ask %d, chain asked %d; %s | %s", len(d.Would), len(d.Asked), paths, skips)
}

// Compare holds two vectors against each other, atom by atom, on every field
// the record carries EXCEPT the clock and the lane's own bookkeeping
// (StartedAt, FinishedAt, Gradings, Audit): two runs never start at the same
// instant and the others belong to atoms this package does not port.
func Compare(today, shadow []checks.Verdict) Report {
	var rep Report
	byID := map[string]checks.Verdict{}
	for _, v := range shadow {
		byID[v.Atom] = v
	}
	seen := map[string]bool{}
	for _, t := range today {
		seen[t.Atom] = true
		s, ok := byID[t.Atom]
		if !ok {
			rep.MissingShadow = append(rep.MissingShadow, t.Atom)
			continue
		}
		if isDryWitness(s) {
			rep.Dry = dryWitness(t, s)
			continue
		}
		if fields := differing(t, s); len(fields) > 0 {
			rep.Differ = append(rep.Differ, Diff{Atom: t.Atom, Fields: fields, StateDiffers: t.State != s.State, Today: t, Shadow: s})
			continue
		}
		rep.Agree = append(rep.Agree, t.Atom)
	}
	for _, s := range shadow {
		if !seen[s.Atom] {
			rep.MissingToday = append(rep.MissingToday, s.Atom)
		}
	}
	return rep
}

// differing names the compared fields on which two verdicts differ.
func differing(a, b checks.Verdict) []string {
	var out []string
	check := func(name string, x, y any) {
		if !reflect.DeepEqual(x, y) {
			out = append(out, name)
		}
	}
	check("stage", a.Stage, b.Stage)
	check("lane", a.Lane, b.Lane)
	check("state", a.State, b.State)
	check("result", a.Result, b.Result)
	check("reason", a.Reason, b.Reason)
	check("logs", a.Logs, b.Logs)
	check("truncated", a.Truncated, b.Truncated)
	check("original_bytes", a.OriginalBytes, b.OriginalBytes)
	check("findings", a.Findings, b.Findings)
	return out
}

// StatesAgree counts the atoms both sides answered with the same state.
func (r Report) StatesAgree() int {
	n := len(r.Agree)
	for _, d := range r.Differ {
		if !d.StateDiffers {
			n++
		}
	}
	return n
}

// Render is the report as the lines a CI log carries: a headline, then one
// block per atom that is not an exact agreement.
func (r Report) Render() string {
	compared := len(r.Agree) + len(r.Differ)
	var b strings.Builder
	took := ""
	if r.Elapsed > 0 {
		took = ", took " + r.Elapsed.Round(time.Millisecond).String()
	}
	who := ""
	if r.Voter != "" {
		who = " (" + r.Voter + " voted)"
	}
	fmt.Fprintf(&b, "shadow atoms%s: %d compared, %d identical, %d same state, %d state differs, %d missing from the binary, %d missing from the chains%s\n",
		who, compared, len(r.Agree), r.StatesAgree()-len(r.Agree), compared-r.StatesAgree(), len(r.MissingShadow), len(r.MissingToday), took)
	if r.Dry != nil {
		b.WriteString(r.Dry.line() + "\n")
	}
	for _, d := range r.Differ {
		kind := "text differs"
		if d.StateDiffers {
			kind = "STATE DIFFERS"
		}
		fmt.Fprintf(&b, "\n%s: %s (%s)\n  chain  state=%d %s\n  binary state=%d %s\n",
			d.Atom, kind, strings.Join(d.Fields, ", "),
			d.Today.State, clip(d.Today.Reason), d.Shadow.State, clip(d.Shadow.Reason))
	}
	for _, id := range r.MissingShadow {
		fmt.Fprintf(&b, "\n%s: the chain answered and the binary did not\n", id)
	}
	for _, id := range r.MissingToday {
		fmt.Fprintf(&b, "\n%s: the binary answered and the chain did not\n", id)
	}
	return b.String()
}

// clip bounds a reason for the log: the full verdicts are in the vectors, and a
// comparison that reprints a thousand findings twice is not one anybody reads.
func clip(s string) string {
	const n = 300
	s = strings.ReplaceAll(s, "\n", " | ")
	if len(s) > n {
		// Back up to a rune boundary: a cut through a multi-byte rune leaves
		// invalid UTF-8 in a log line.
		cut := n
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		return s[:cut] + "..."
	}
	return s
}
