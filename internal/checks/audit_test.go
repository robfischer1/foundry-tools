package checks

import (
	"strings"
	"testing"
)

// The exact tail pip-audit printed on the iris push of 2026-09-17: a
// 15-second read timeout against pypi.org, exit 1, no finding anywhere in it.
const pipAuditTimeout = `Installed 64 packages in 2.05s
Traceback (most recent call last):
  File ".../pip_audit/_service/pypi.py", line 64, in query
    response: requests.Response = self.session.get(url=url, timeout=self.timeout)
requests.exceptions.ReadTimeout: HTTPSConnectionPool(host='pypi.org', port=443): Read timed out. (read timeout=15)
`

// The exact tail cargo-audit printed on furnace #65 (2026-09-19): GitHub
// answered the RustSec fetch 408, exit 1, and the verdict truncated the cause
// before the status line — so the head alone has to be enough.
const cargoAuditFetch408 = `    Fetching advisory database from ` + "`https://github.com/RustSec/advisory-db.git`" + `
error: couldn't fetch advisory database: git operation failed: Could not decode server reply
Caused by:
  -> Could not decode server reply
  -> Failed to read from line reader
  -> An`

func TestAuditVerdictReadsANetworkFaultAsCouldNotRun(t *testing.T) {
	a := AtomByID("python:pip-audit")

	v := AuditVerdict(a, 1, pipAuditTimeout)
	if v.State != int(StateCannotRun) {
		t.Fatalf("a read timeout against the advisory source is not a finding: state %d\n%s", v.State, v.Reason)
	}
	for _, want := range []string{"could not reach its advisory source", "rc=1", "run it again", "Read timed out"} {
		if !strings.Contains(v.Reason, want) {
			t.Errorf("reason lacks %q:\n%s", want, v.Reason)
		}
	}

	// Each auditor's own phrasing for the same thing.
	for name, out := range map[string]string{
		"go dial":      "govulncheck: fetching vulnerability database: Get \"https://vuln.go.dev/index/db.json\": dial tcp: lookup vuln.go.dev: Temporary failure in name resolution",
		"bun reset":    "error: ECONNRESET reading https://registry.npmjs.org/-/npm/v1/security/advisories/bulk",
		"cargo 503":    "error: couldn't fetch advisory database: 503 Service Unavailable",
		"cargo 408":    cargoAuditFetch408,
		"gitoxide 408": "-> An IO error occurred when talking to the server\n-> Received HTTP status 408",
		"unreachable":  "Network is unreachable",
	} {
		if v := AuditVerdict(a, 1, out); v.State != int(StateCannotRun) {
			t.Errorf("%s: state %d, want could-not-run", name, v.State)
		}
	}
}

func TestAuditVerdictKeepsARealFindingAndACleanRun(t *testing.T) {
	a := AtomByID("python:pip-audit")

	// A finding is a table, and it exits 1 without a fault in sight.
	finding := "Found 1 known vulnerability in 1 package\nName    Version ID             Fix Versions\nurllib3 2.2.0   GHSA-34jh-p97f 2.2.2\n"
	if v := AuditVerdict(a, 1, finding); v.State != int(StateFindings) {
		t.Errorf("a real finding must stay a finding: state %d\n%s", v.State, v.Reason)
	}

	// A clean run exits 0, and stays clean even if its output mentions a
	// timeout in passing — the fault is read off a NON-ZERO exit only.
	if v := AuditVerdict(a, 0, "No known vulnerabilities found\n(retried once after a read timed out)\n"); v.State != int(StatePass) {
		t.Errorf("a clean audit must pass: state %d\n%s", v.State, v.Reason)
	}

	// A non-zero exit with no fault and no table is whatever it is: findings
	// (exit 1) or could-not-run (anything else), exactly as VerdictOf reads it.
	if v := AuditVerdict(a, 1, "something else went wrong"); v.State != int(StateFindings) {
		t.Errorf("exit 1 without a fault is findings: state %d", v.State)
	}
	if v := AuditVerdict(a, 2, "usage: pip-audit [-h]"); v.State != int(StateCannotRun) {
		t.Errorf("exit 2 is could-not-run as ever: state %d", v.State)
	}
}
