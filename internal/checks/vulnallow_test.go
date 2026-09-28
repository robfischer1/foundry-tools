package checks

import (
	"strings"
	"testing"
	"time"
)

// A REAL govulncheck REPORT, trimmed. The parse keys on the "Vulnerability #N:"
// line, so the fixture has to be the tool's own shape rather than a convenient
// one — if that line ever changes, this fixture is what notices.
const govulnReport = `=== Symbol Results ===

Vulnerability #1: GO-2026-6508
    OpenTelemetry-Go: Log gRPC exporter ignores env TLS certs, bypassing
    mTLS/pinning in go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc
  More info: https://pkg.go.dev/vuln/GO-2026-6508
  Module: go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc
    Found in: go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc@v0.19.0
    Fixed in: go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc@v0.21.0

Your code is affected by 1 vulnerability from 1 module.
`

// beforeExpiry is a day the shipped allowance still covers; afterExpiry is one
// it does not. Both are derived from the entry itself, so renewing the date
// does not silently turn these into tests of nothing.
func beforeExpiry(t *testing.T) time.Time {
	t.Helper()
	a, ok := AllowedVuln("GO-2026-6508", time.Date(2026, time.September, 28, 0, 0, 0, 0, time.UTC))
	if !ok {
		t.Fatal("the shipped allowance for GO-2026-6508 is gone — if dagger shipped the fix, delete these tests with it")
	}
	return a.Until.AddDate(0, 0, -1)
}

func afterExpiry(t *testing.T) time.Time {
	t.Helper()
	a, _ := AllowedVuln("GO-2026-6508", time.Date(2026, time.September, 28, 0, 0, 0, 0, time.UTC))
	return a.Until.AddDate(0, 0, 1)
}

// THE ADVISORY IS READ OFF THE TOOL'S OWN LINE, not guessed at.
func TestTheAdvisoryIdsAreReadFromTheReport(t *testing.T) {
	got := FoundVulnIDs(govulnReport)
	if len(got) != 1 || got[0] != "GO-2026-6508" {
		t.Fatalf("FoundVulnIDs = %v, want [GO-2026-6508]", got)
	}
}

// A REPEATED ADVISORY IS ONE ADVISORY. govulncheck prints a module's traces
// under one heading, but a second heading for the same id must not make the
// allowance look like it covered two things.
func TestARepeatedAdvisoryIsCountedOnce(t *testing.T) {
	doubled := govulnReport + "\nVulnerability #2: GO-2026-6508\n"
	if got := FoundVulnIDs(doubled); len(got) != 1 {
		t.Fatalf("a repeated id is one id, got %v", got)
	}
}

// AN ALLOWED ADVISORY PASSES, AND SAYS SO LOUDLY. A green gate that hid a known
// vulnerability without naming it would be worse than the red.
func TestAnAllowedAdvisoryPassesAndNamesItself(t *testing.T) {
	why, ok := SuppressedVulnReason(govulnReport, beforeExpiry(t))
	if !ok {
		t.Fatal("the shipped allowance must cover its own advisory before the expiry")
	}
	for _, want := range []string{"GO-2026-6508", "ALLOWED", "not a clean scan", "allowed until", "dagger/otel-go"} {
		if !strings.Contains(why, want) {
			t.Errorf("the reason must carry %q:\n%s", want, why)
		}
	}
}

// AND IT STOPS PASSING. The expiry is the whole mechanism: past it the gate
// reds again and somebody has to look. An allowance that never lapsed would be
// a permanent exemption wearing a temporary label.
func TestTheAllowanceStopsAtItsExpiry(t *testing.T) {
	if _, ok := SuppressedVulnReason(govulnReport, afterExpiry(t)); ok {
		t.Fatal("an expired allowance must not suppress anything")
	}
	if _, ok := AllowedVuln("GO-2026-6508", afterExpiry(t)); ok {
		t.Fatal("AllowedVuln must refuse past the expiry")
	}
}

// THE EXPIRY DAY ITSELF STILL COUNTS, so the boundary is a date rather than an
// instant somebody has to reason about across time zones.
func TestTheExpiryDayIsTheLastDayItApplies(t *testing.T) {
	a, _ := AllowedVuln("GO-2026-6508", beforeExpiry(t))
	if _, ok := AllowedVuln("GO-2026-6508", a.Until); !ok {
		t.Fatal("the expiry day is the last day the allowance applies")
	}
}

// ONE UNALLOWED ADVISORY REDS THE WHOLE RUN. This is the property that makes an
// allowlist safe to have: it covers what it names and nothing else, so a NEW
// vulnerability arriving beside an allowed one is still a finding.
func TestAnUnknownAdvisoryBesideAnAllowedOneStillReds(t *testing.T) {
	both := govulnReport + "\nVulnerability #2: GO-2026-9999\n"
	if _, ok := SuppressedVulnReason(both, beforeExpiry(t)); ok {
		t.Fatal("an advisory nobody allowed must red the atom, whatever it arrived beside")
	}
}

func TestAnAdvisoryNobodyAllowedIsNotSuppressed(t *testing.T) {
	only := "Vulnerability #1: GO-2026-9999\n"
	if _, ok := SuppressedVulnReason(only, beforeExpiry(t)); ok {
		t.Fatal("GO-2026-9999 is not on the list and must not be suppressed")
	}
}

// AN OUTPUT THIS CANNOT PARSE IS A FINDING, NOT A PASS. If govulncheck's report
// ever changes shape, the honest answer to "I did not understand the finding"
// is the finding — failing closed is the difference between an allowlist and a
// hole.
func TestAReportWithNoRecognisableAdvisoryFailsClosed(t *testing.T) {
	for _, out := range []string{
		"",
		"Your code is affected by 1 vulnerability from 1 module.\n",
		"Vulnerability: something entirely new\n",
		"GO-2026-6508 mentioned in passing, not on its own line\n",
	} {
		if _, ok := SuppressedVulnReason(out, beforeExpiry(t)); ok {
			t.Fatalf("an unparseable report must not suppress: %q", out)
		}
	}
}

// EVERY ENTRY CARRIES ITS OWN JUSTIFICATION. An allowance with no expiry or no
// reason is the thing this file exists to prevent, so the list is checked
// rather than trusted.
func TestEveryAllowanceIsDatedAndExplained(t *testing.T) {
	for _, a := range vulnAllowed {
		if a.ID == "" {
			t.Error("an allowance with no advisory id")
		}
		if a.Until.IsZero() {
			t.Errorf("%s has no expiry — the expiry is the mechanism", a.ID)
		}
		if !strings.Contains(a.Why, "CLEARED BY") {
			t.Errorf("%s must say what would clear it, so the next reader need not re-derive it", a.ID)
		}
	}
}

// SEVERAL ALLOWED ADVISORIES COME OUT IN A STABLE ORDER, which is why the
// reason is sorted rather than emitted in the order govulncheck happened to
// print them: a reason that reshuffles between runs is a diff nobody can read.
//
// IT ANSWERS A SURVIVING MUTANT (vulnallow.go:116 NOT COVERED, 2026-09-28):
// with one entry on the list the comparator never ran, so nothing could tell
// the sort from its absence.
func TestSeveralAllowedAdvisoriesAreNamedInAStableOrder(t *testing.T) {
	until := time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC)
	restore := vulnAllowed
	vulnAllowed = []VulnAllowance{
		{ID: "GO-2026-8888", Until: until, Why: "second. CLEARED BY: nothing, this is a test"},
		{ID: "GO-2026-1111", Until: until, Why: "first. CLEARED BY: nothing, this is a test"},
	}
	t.Cleanup(func() { vulnAllowed = restore })

	// Deliberately reported in the opposite order to the one wanted back.
	out := "Vulnerability #1: GO-2026-8888\nVulnerability #2: GO-2026-1111\n"
	why, ok := SuppressedVulnReason(out, until.AddDate(0, 0, -1))
	if !ok {
		t.Fatal("two allowed advisories must suppress")
	}
	first, second := strings.Index(why, "GO-2026-1111"), strings.Index(why, "GO-2026-8888")
	if first < 0 || second < 0 {
		t.Fatalf("both advisories must be named:\n%s", why)
	}
	if first > second {
		t.Fatalf("the reason is sorted by advisory id, not by report order:\n%s", why)
	}
	if !strings.Contains(why, "2 known vulnerability") {
		t.Fatalf("the reason counts what it allowed:\n%s", why)
	}
}

// AND ONE EXPIRED ENTRY AMONG SEVERAL STILL REDS THE RUN. The allowance is per
// advisory and per date; a list that passed because MOST of it was current
// would be the hole this whole file is shaped to avoid.
func TestOneExpiredEntryAmongSeveralStillReds(t *testing.T) {
	now := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	restore := vulnAllowed
	vulnAllowed = []VulnAllowance{
		{ID: "GO-2026-1111", Until: now.AddDate(0, 1, 0), Why: "current. CLEARED BY: nothing, this is a test"},
		{ID: "GO-2026-8888", Until: now.AddDate(0, 0, -1), Why: "lapsed. CLEARED BY: nothing, this is a test"},
	}
	t.Cleanup(func() { vulnAllowed = restore })

	out := "Vulnerability #1: GO-2026-1111\nVulnerability #2: GO-2026-8888\n"
	if _, ok := SuppressedVulnReason(out, now); ok {
		t.Fatal("one lapsed entry must red the whole run")
	}
}

// THE VERDICT IS BUILT HERE, AND TESTED HERE. The atom's own test exercises it
// from package main, which this package's coverage never sees — the lane said
// so (vulnallow.go:145 NOT COVERED, twice, 2026-09-28).
//
// WHAT IT PINS is the property the whole file exists for: a PASS that still
// says what it allowed. VerdictOf would have answered "<id>: PASS" here and
// made a suppressed vulnerability read exactly like a clean scan.
func TestAnAllowedVerdictPassesAndStillSaysWhatItAllowed(t *testing.T) {
	a := AtomByID("go:govulncheck")
	v := AllowedVulnVerdict(a, "1 known vulnerability(ies) found and ALLOWED", "Vulnerability #1: GO-2026-6508\nFixed in: v0.21.0\n")

	if v.State != int(StatePass) {
		t.Fatalf("an allowed advisory is a pass, got state %d", v.State)
	}
	if v.Atom != "go:govulncheck" {
		t.Fatalf("the verdict carries its atom, got %q", v.Atom)
	}
	// The reason is the atom's id, a colon-space, then the allowance — the
	// shape every other reason in this package takes.
	want := "go:govulncheck: 1 known vulnerability(ies) found and ALLOWED"
	if v.Reason != want {
		t.Fatalf("reason\n want %q\n  got %q", want, v.Reason)
	}
	// AND THE SCAN'S OWN REPORT IS THE EVIDENCE, captured as the atom's lines
	// exactly as a red run's would be, so the allowance can be checked rather
	// than taken on trust.
	joined := strings.Join(v.Logs, "\n")
	if !strings.Contains(joined, "GO-2026-6508") || !strings.Contains(joined, "Fixed in") {
		t.Fatalf("the report must survive as the atom's lines:\n%v", v.Logs)
	}
}

// AN EMPTY REPORT STILL PRODUCES A WELL-FORMED VERDICT rather than a nil log
// list: "this printed nothing" is a different fact from "nobody set this
// field", which is the rule every atom in this package follows.
func TestAnAllowedVerdictNeverCarriesANilLogList(t *testing.T) {
	v := AllowedVulnVerdict(AtomByID("go:govulncheck"), "allowed", "")
	if v.Logs == nil {
		t.Fatal("an allowed verdict carries [], never nil")
	}
}
