package checks

import (
	"strings"
)

// THE OPS LANE'S JUDGEMENTS, AS FUNCTIONS. What the atoms
// used to decide in shell — a quiet recursive match over a workflow tree, a
// `sed -n` at a summary line, a `case` on a linter's exit code — lives here
// instead, over strings and ints, where a table test can hold it. Nothing in
// package main is unit-testable (dag panics without an engine), so a judgement
// that is not in this package is a judgement nobody checks.

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
		return 2, "ops:kube-linter: CANNOT RUN - kube-linter parsed no object under flux/. That is the same exit code as a finding, and it is not one."
	}
	if code == 0 {
		return 0, "ops:kube-linter: clean"
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

// headLines and tailLines are the two cuts KubeLinterState composes, and they
// are CLAMPS rather than branches on purpose.
//
// `if len(lines) > n { return lines[:n] }; return lines` and the same line with
// `>=` answer identically for every input — at n == len(lines) the cut IS the
// whole slice — so the boundary mutant on that comparison is EQUIVALENT, and an
// equivalent mutant is a red mutation gate that no test can ever clear. Both
// LIVED on PR #31 (internal/checks/sweeplane.go 164:16 and 171:16, measured
// 2026-09-12) and both were still alive after a table test covering n == len,
// n == len±1 and n == 0. A clamp has no comparison to mutate. The behaviour is
// unchanged and TestHeadAndTailLinesAtTheirBoundaries holds it at every
// boundary the branch used to decide.
func headLines(lines []string, n int) []string {
	return lines[:min(n, len(lines))]
}

func tailLines(lines []string, n int) []string {
	return lines[len(lines)-min(n, len(lines)):]
}
