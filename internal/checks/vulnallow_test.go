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
