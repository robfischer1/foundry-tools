package atoms

import (
	"fmt"
	"reflect"
	"strings"
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
	fmt.Fprintf(&b, "shadow atoms: %d compared, %d identical, %d same state, %d state differs, %d missing from the binary, %d missing from the chains\n",
		compared, len(r.Agree), r.StatesAgree()-len(r.Agree), compared-r.StatesAgree(), len(r.MissingShadow), len(r.MissingToday))
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
