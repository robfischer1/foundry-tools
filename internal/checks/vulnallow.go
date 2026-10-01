package checks

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// THE ONE PLACE THE GATE TOLERATES A KNOWN VULNERABILITY, and it is deliberately
// uncomfortable to use: one advisory id at a time, each with a date it stops
// working and a reason naming what would clear it.
//
// WHY THIS EXISTS AT ALL. govulncheck asks a LIVE advisory database, so a repo
// that was green an hour ago is red now with nobody having touched it — the
// finding arrives from outside the tree. That is usually correct and the answer
// is to upgrade. It is not always possible: on 2026-09-28 GO-2026-6508 landed
// against otlploggrpc <= v0.20.x while github.com/dagger/otel-go@v1.43.0 — the
// LATEST, `go list -m -versions` answers only v1.41.0 and v1.43.0 — is built
// against the otel/log API that v0.21.0 removes. Bumping it produced thirteen
// undefined-symbol errors. Every foundry-tools pull in the fleet was red on a
// dependency nobody had changed and nobody could change.
//
// AN ALLOWANCE IS NOT A DISMISSAL. Three properties make this safe to have:
//
//   - IT FAILS CLOSED. One advisory in the run that is not allowed, or an
//     output this cannot parse, and the atom reds exactly as before. A finding
//     is suppressed only when EVERY id found is covered.
//   - IT EXPIRES. Past Until the entry stops applying and the gate reds again,
//     which is what stops "temporary" from meaning "forever". An allowance that
//     has to be renewed is an allowance somebody looks at.
//   - IT IS LOUD. The atom passes with a reason that names each suppressed id,
//     its expiry and its reason, so a green gate never quietly hides a known
//     vulnerability. The full govulncheck output is kept as the atom's lines.
//
// THIS CHANGES THE CANONICAL GATE FOR EVERY GO REPO IN THE FLEET. Rob's call,
// 2026-09-28, over freezing foundry-tools until dagger ships a compatible
// otel-go.

// VulnAllowance is one advisory the gate will not red on, until it will.
type VulnAllowance struct {
	// ID is the advisory exactly as govulncheck prints it (GO-YYYY-NNNN).
	ID string
	// Until is the day this stops applying. NOT optional and NOT far away: it
	// is the mechanism, not a formality.
	Until time.Time
	// Why names what would clear the allowance, so the next reader can tell
	// whether it still holds without re-deriving the whole situation.
	Why string
}

// vulnAllowed is the whole list. It should be short, and empty is the goal.
var vulnAllowed = []VulnAllowance{
	{
		ID:    "GO-2026-6508",
		Until: time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC),
		Why: "otlploggrpc's fix is v0.21.0, whose otel/log API github.com/dagger/otel-go@v1.43.0 " +
			"does not compile against (13 undefined symbols: log.KeyValue, log.Value, log.Kind*), and " +
			"v1.43.0 is the latest release. Nothing in this fleet imports otlplog — the only reachability " +
			"is dagger's own generated dispatch — and OTLP log shipping was reverted at Rob's instruction. " +
			"CLEARED BY: a dagger/otel-go release built against otel/log v0.21.0 or later, then drop this entry.",
	},
	{
		// THE SAME WALL, ONE MODULE OVER (2026-10-01). GO-2026-6615 is otel/sdk/log
		// <= v0.20.x, fixed in v0.21.0 — and sdk/log v0.21.0 pulls otel/log v0.21.0,
		// which otlploghttp v0.19.0 and dagger/otel-go@v1.43.0 do not compile
		// against (measured: `undefined: api.KeyValue`). Its sibling GO-2026-6505
		// (otlptrace) was not this: v1.46.0 builds, and was bumped instead.
		ID:    "GO-2026-6615",
		Until: time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC),
		Why: "otel/sdk/log's fix is v0.21.0, which drags otel/log to v0.21.0, whose API " +
			"github.com/dagger/otel-go@v1.43.0 (the latest release) and otlploghttp v0.19.0 do not compile " +
			"against. The only reachability is dagger's own generated dispatch (otel.InitEmbedded / otel.Close). " +
			"CLEARED BY: a dagger/otel-go release built against otel/log v0.21.0 or later, then drop this entry " +
			"with GO-2026-6508's.",
	},
}

// vulnID matches the advisory on govulncheck's own "Vulnerability #N:" line,
// which is the stable, documented shape of its text report.
var vulnID = regexp.MustCompile(`(?m)^Vulnerability #\d+: (GO-\d{4}-\d+)`)

// FoundVulnIDs answers every advisory govulncheck named, in the order it named
// them, without repeats.
func FoundVulnIDs(out string) []string {
	var ids []string
	seen := map[string]bool{}
	for _, m := range vulnID.FindAllStringSubmatch(out, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			ids = append(ids, m[1])
		}
	}
	return ids
}

// AllowedVuln answers the allowance covering an advisory at t, if one does.
func AllowedVuln(id string, t time.Time) (VulnAllowance, bool) {
	for _, a := range vulnAllowed {
		// NOT After, so the expiry day itself is the LAST day it applies and
		// the boundary is a date rather than an instant somebody has to reason
		// about across time zones.
		if a.ID == id && !t.After(a.Until) {
			return a, true
		}
	}
	return VulnAllowance{}, false
}

// SuppressedVulnReason answers the pass reason when every advisory in a run is
// allowed, and false when the atom must red.
//
// THE EMPTY SET IS NOT A PASS. A findings exit whose output named no advisory
// this could parse is a report shaped differently from the one this understands,
// and the honest answer to "I did not understand the finding" is the finding.
func SuppressedVulnReason(out string, t time.Time) (string, bool) {
	ids := FoundVulnIDs(out)
	if len(ids) == 0 {
		return "", false
	}
	// SORTED BY ID, so the reason reads the same however govulncheck ordered
	// its report — a reason that reshuffles between runs is a diff nobody can
	// read. The ids are sorted rather than the allowances: a comparator of my
	// own carried a boundary mutant no test can kill (`<` and `<=` are the same
	// function over distinct keys, and these are distinct by FoundVulnIDs'
	// dedupe), so this uses the stdlib's sort over strings and has no
	// comparator to get wrong.
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	var allowed []VulnAllowance
	for _, id := range sorted {
		a, ok := AllowedVuln(id, t)
		if !ok {
			return "", false
		}
		allowed = append(allowed, a)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d known vulnerability(ies) found and ALLOWED by the gate's own list — this is not a clean scan:", len(allowed))
	for _, a := range allowed {
		fmt.Fprintf(&b, "\n  %s — allowed until %s\n    %s", a.ID, a.Until.Format("2006-01-02"), a.Why)
	}
	return b.String(), true
}

// AllowedVulnVerdict is a PASS that says what it allowed.
//
// IT DOES NOT GO THROUGH VerdictOf, and that is the whole point. VerdictOf's
// reasonFor DISCARDS the output for a passing atom and answers "<id>: PASS" —
// correct for every other atom, and exactly wrong here, because it would make a
// suppressed vulnerability indistinguishable from a clean scan in the one field
// a reader looks at. AbsentVerdict is built by hand for the same reason its own
// comment gives: a lane with no surface "reports ABSENT, exits 0, and SAYS SO.
// Silence would be indistinguishable from a scan that found nothing."
//
// THE OUTPUT IS STILL THE LINES. govulncheck's whole report is captured exactly
// as a red run's would be, so the evidence for the allowance travels with it.
func AllowedVulnVerdict(a AtomDef, why, output string) Verdict {
	logs, truncated, originalBytes := CaptureLogs(output)
	return Verdict{
		Atom:          a.ID,
		Stage:         a.Stage,
		Lane:          string(a.Lane),
		State:         int(StatePass),
		Result:        StatePass.String(),
		Reason:        a.ID + ": " + why,
		Logs:          logs,
		Truncated:     truncated,
		OriginalBytes: originalBytes,
	}
}
