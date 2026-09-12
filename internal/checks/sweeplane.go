package checks

import (
	"regexp"
	"strconv"
	"strings"
)

// THE SWEEP'S JUDGEMENTS, AS FUNCTIONS. Everything the five repo-cadence atoms
// used to decide in shell — a quiet recursive match over a workflow tree, a
// `sed -n` at a summary line, a `case` on a linter's exit code — lives here
// instead, over strings and ints, where a table test can hold it. Nothing in
// package main is unit-testable (dag panics without an engine), so a judgement
// that is not in this package is a judgement nobody checks.

var pinSurfaceRE = regexp.MustCompile(PinSurfacePattern)

// HasPinSurface reports whether any of these file bodies carries a digest
// reference at all — the question PinSurfacePattern exists to ask, asked over
// the contents of .forgejo/workflows.
//
// THE ANSWER DECIDES ABSENT vs CANNOT RUN, which is the whole of the fix
// images.go records: on ca-sweep-manual-1788973171 (2026-09-09) 57 of 86 repos
// answered cannot-run with "no pins found under .forgejo/workflows - the scan
// is broken, not the tree clean". That sentence is true in foundry-stocks and
// false in a star that calls the reusable workflow, where the image pin lives
// in the callee's tree. No surface is an ABSENCE; a surface the canonical
// extractor then cannot read is the broken scan.
func HasPinSurface(bodies []string) bool {
	for _, b := range bodies {
		if pinSurfaceRE.MatchString(b) {
			return true
		}
	}
	return false
}

// AttestingWorkflowPattern is the call that puts a repository's image inside
// the workflow that attests its SBOM — build.yml, frontend-build.yml or
// bake-blade.yml, at their one home in foundry-stocks. Carried verbatim from
// the extended regular expression the atom's shell body matched with, so the
// two can be diffed by eye.
const AttestingWorkflowPattern = `uses:[[:space:]]*foundry/foundry-stocks/\.forgejo/workflows/(build|frontend-build|bake-blade)\.yml@`

var attestingWorkflowRE = regexp.MustCompile(AttestingWorkflowPattern)

// BuiltThroughAttestingWorkflow reports whether any of these workflow bodies
// calls one of the three attesting builds.
//
// WHAT A FALSE ANSWER MEANS. The weekly portfolio re-score (CronJob
// portfolio-weekly, ci-portfolio-pipeline) reads cosign SBOM attestations out
// of the registry. An image nobody attests contributes no attestation to
// read — so it is not scored badly, it is not scored at all, and the digest
// reports clean because nothing ever looked at it. That hole is structurally
// invisible to the scan itself, which is why the question is asked here, at
// the one place where the answer is a fact about the tree rather than a fact
// about the database.
func BuiltThroughAttestingWorkflow(bodies []string) bool {
	for _, b := range bodies {
		if attestingWorkflowRE.MatchString(b) {
			return true
		}
	}
	return false
}

var (
	kubeconformSummaryRE = regexp.MustCompile(`(?m)^Summary:.*$`)
	kubeconformValidRE   = regexp.MustCompile(`Valid: ([0-9]+)`)
)

// KubeconformSummary reads kubeconform's own summary line out of everything it
// printed and answers how many resources it VALIDATED.
//
//	Summary: 809 resources found in 245 files - Valid: 809, Invalid: 0, Errors: 0, Skipped: 101
//
// ok is false when there is no summary line, or a summary line with no Valid
// count — the `[ -z "$valid" ]` branch of the shell this replaces, and a CANNOT
// RUN either way: no count means nothing was measured.
//
// THE CALLER PASSES STDOUT AND STDERR TOGETHER. The shell read
// `cat /tmp/kc.out /tmp/kc.err` because which of the two the summary lands on
// is not a promise kubeconform makes.
//
// valid == 0 is the zero-resource refusal, and it is why this answers a count
// rather than a bool: measured against infra's flux/ tree on 2026-09-08, the
// default schema store alone validates 517 and SKIPS 395 — every HelmRelease,
// Kustomization, GitRepository and CiliumNetworkPolicy, which is most of what
// that tree IS. A run that validated nothing at all reports 0 invalid because
// it examined nothing, not because the tree is correct.
func KubeconformSummary(out string) (valid int, summary string, ok bool) {
	summary = kubeconformSummaryRE.FindString(out)
	if summary == "" {
		return 0, "", false
	}
	m := kubeconformValidRE.FindStringSubmatch(summary)
	if m == nil {
		return 0, summary, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, summary, false
	}
	return n, summary, true
}

// KubeconformFindings is what a reader of a failed validation needs:
// everything kubeconform printed EXCEPT the summary line, capped at limit
// lines. The shell form dropped the summary and took the first 80; the cap is
// there because a tree that fails schema validation wholesale prints thousands
// of lines, and a verdict nobody can read is a verdict nobody acts on.
func KubeconformFindings(out string, limit int) string {
	var kept []string
	for _, line := range splitOutputLines(out) {
		if strings.HasPrefix(line, "Summary:") {
			continue
		}
		kept = append(kept, line)
		if len(kept) == limit {
			break
		}
	}
	return strings.Join(kept, "\n")
}

// KubeLinterState maps kube-linter's exit code and output to an atom state.
//
// --fail-if-no-objects-found IS KUBE-LINTER'S OWN ZERO-POPULATION REFUSAL, AND
// IT EXITS 1 FOR IT — the same code it uses for a finding. Reading that as
// findings would be wrong in the direction that still looks like the check
// worked: a red nobody can fix, about a tree nothing parsed. So the message is
// matched and the state remapped to 2. Everything else is the plain mapping:
// 0 is clean, anything left is a finding carrying the linter's own words.
//
// The finding's reason leads with the LAST three lines — kube-linter closes
// with its own count, and that is the line a human reads first — then the
// first 120, which are the findings themselves.
func KubeLinterState(code int, out string) (state int, reason string) {
	if strings.Contains(out, "no valid objects found") {
		return 2, "sweep:kube-linter: CANNOT RUN - kube-linter parsed no object under flux/. That is the same exit code as a finding, and it is not one."
	}
	if code == 0 {
		return 0, "sweep:kube-linter: clean"
	}
	lines := splitOutputLines(out)
	body := make([]string, 0, len(lines)+3)
	body = append(body, tailLines(lines, 3)...)
	body = append(body, headLines(lines, 120)...)
	return 1, strings.Join(body, "\n")
}

// splitOutputLines is a tool's output as lines, with the trailing newline every
// tool writes discarded — otherwise a tail of three answers two lines and a
// blank. Command substitution stripped it for the shell; nothing strips it here.
func splitOutputLines(out string) []string {
	out = strings.TrimRight(out, "\r\n")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func headLines(lines []string, n int) []string {
	if len(lines) > n {
		return lines[:n]
	}
	return lines
}

func tailLines(lines []string, n int) []string {
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}
