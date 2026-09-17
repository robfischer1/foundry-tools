package checks

import "fmt"

// AuditVerdict settles a dependency audit — pip-audit, bun audit, cargo audit,
// govulncheck — from how it exited and what it printed.
//
// A NETWORK FAULT IS A COULD-NOT-RUN, NOT A FINDING. Every one of these tools
// asks a live advisory source and exits non-zero when it cannot; read as a
// finding, that exit says the tree has a vulnerability it does not have. And
// the difference is not cosmetic: verdictFor re-asks a could-not-run past the
// engine's cache, and a finding it never re-asks. MEASURED 2026-09-17 on an
// iris push: pip-audit hit a 15-second read timeout against pypi.org, the atom
// filed FINDINGS, the exec was cached against the tree, and every push of that
// tree replayed the same traceback — while pypi answered in 47ms from the
// same cluster the whole time. Classified as could-not-run, the second ask
// would have looked again and passed.
//
// The fault is only read off a NON-ZERO exit: a clean audit whose output
// happened to mention a timeout is still clean, and a real finding's table
// does not carry a traceback.
func AuditVerdict(a AtomDef, code int, out string) Verdict {
	if code != 0 && networkFault.MatchString(out) {
		return VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the audit could not reach its advisory source (rc=%d) — the substrate, not the tree; run it again\n%s",
			a.ID, code, tail(out, 12)))
	}
	return VerdictOf(a, code, out)
}
