package checks

import (
	"encoding/json"
	"path"
	"strings"
)

// THE CONTROLS FOR THE GO MUTATION GATE, and the file set one of them governs.
//
// A MUTATION RUN'S OWN OUTPUT CANNOT SAY WHETHER IT MEASURED ANYTHING. The
// oracle is the test process's exit code, so both ways it can be wrong produce a
// report that reads exactly like a measurement:
//
//  1. IT NEVER STARTS THE CHILD. Every mutant reads KILLED and a broken harness
//     scores perfectly (gremlins v0.6.0 mangled --test-cpu into one argv word,
//     #7649: 264 "kills" in 262ms). No wall-clock floor detects it — a failing
//     `go test` costs ~1ms in CI and ~250ms on a host re-execing a toolchain.
//  2. IT GRADES THE WRONG PACKAGE. gremlins resolved a file's package from the
//     PACKAGE CLAUSE, so every file in a `package main` resolved to the module
//     root (gremlins#268). The tests it then ran for such a mutant were not the
//     tests that cover it.
//
// BOTH CONTROLS SURVIVE THE MOVE TO gomutants, and #2 is why the second one is
// kept rather than deleted on a promise. gomutants resolves packages properly
// and MEASURED CLEAN against the shape of #268 on 2026-09-29 — a `package main`
// at cmd/tool with nothing at the module root, graded LIVED, the honest verdict.
// That is a measurement of one version of one tool, and the control is what
// re-takes it on every run. When it has stood green for a while, ScoreGoMutation's
// `ungraded` bucket and GoMisgradedFiles below go with it, and the control last.
//
// Each control is a module whose mutants have a known honest verdict, run by the
// same binary with the same flags, so a wrong answer there is the same wrongness
// the real run carries. Both read through GoMutationCanary and both expect a
// survivor. What differs is what a broken answer MEANS: the harness control is
// fatal — a harness that cannot run tests measured nothing — while the
// package-main control switches on an exclusion instead. GoMutationVerdict holds
// that asymmetry.

// The canary's answers.
const (
	CanaryOK      = "ok"
	CanaryBroken  = "broken"
	CanaryUnknown = "unknown"
)

// GoMutationCanary reads a control run's JSON REPORT — the same wire shape
// ScoreGoMutation parses, so the control and the run being controlled are read
// by one contract.
//
// A CONTROL'S HONEST VERDICT IS A SHAPE, NOT A COUNT. Every mutant in both
// fixtures is under-tested on purpose, so a run that GRADED them kills none and
// survives at least one. Killing any of them means the runner is not running the
// tests it thinks it is — the failure this exists to catch — and that is broken
// whatever else the report says.
//
// It is deliberately NOT "lived == 1". The old form matched the literal string
// "Killed: 0, Lived: 1" in gremlins' summary, which pinned both the formatting
// and the exact population; gomutants finds two mutants in the same four-line
// fixture, so that test would answer `unknown` forever and a control that cannot
// fail controls nothing. A mutator set changing the count is not a broken
// harness.
//
// NOT COVERED AND NOT VIABLE DO NOT VOTE. Neither is a graded verdict, so
// neither can say whether the tests ran; the maincanary's `func main` body earns
// a few of the former and it is not evidence either way.
func GoMutationCanary(report string) string {
	var d struct {
		Files []struct {
			Mutations []struct {
				Status string `json:"status"`
			} `json:"mutations"`
		} `json:"files"`
	}
	// A PARSE FAILURE NEEDS NO BRANCH OF ITS OWN, and that is a measurement
	// rather than a preference. `if err != nil { return CanaryUnknown }` here was
	// an UNKILLABLE mutant: a failed unmarshal leaves d zero-valued, so the count
	// below is 0 and 0 and the fall-through already answers unknown — identical,
	// for every input, which is why deleting the branch is the fix and forgiving
	// the mutant would not have been. Found by gomutants on this very diff
	// (2026-09-29, 23 mutants, this the only survivor).
	_ = json.Unmarshal([]byte(report), &d)
	var killed, lived int
	for _, f := range d.Files {
		for _, m := range f.Mutations {
			switch m.Status {
			case "KILLED":
				killed++
			case "LIVED":
				lived++
			}
		}
	}
	switch {
	case killed > 0:
		return CanaryBroken
	case lived > 0:
		return CanaryOK
	}
	// Nothing graded either way: no mutants, or every one inert. That is not a
	// control that passed and not one that failed — it is one that did not run.
	return CanaryUnknown
}

// THE HARNESS CONTROL, written into the lane's container from here: a mutant
// whose honest verdict is LIVED. Add's `+` is mutable and its test runs it
// without asserting the result, so an arithmetic mutation changes no outcome.
// Keep it dependency-free — a canary that cannot build is a control that
// cannot control.
const (
	GoMutationCanaryMod  = "module canary\n\ngo 1.21\n"
	GoMutationCanaryCode = "package canary\n\n// Add is mutable and deliberately under-tested: its mutant must survive.\nfunc Add(a, b int) int { return a + b }\n"
	GoMutationCanaryTest = "package canary\n\nimport \"testing\"\n\n// TestAddRuns covers Add without checking it, on purpose.\nfunc TestAddRuns(t *testing.T) {\n\tgot := Add(2, 3)\n\tt.Logf(\"Add(2, 3) = %d\", got)\n}\n"
)

// THE PACKAGE-MAIN CONTROL: the same under-tested mutant, in a `package main`
// in a SUBDIRECTORY, in a module with NO PACKAGE AT ITS ROOT. That shape is
// load-bearing and was measured, not assumed — 2026-09-24, gremlins 0.6.0 with
// the canonical config, the same code both ways:
//
//	package main at the module ROOT   → LIVED  (graded correctly)
//	package main at cmd/tool, no root → KILLED (graded against the empty root)
//
// So a control shaped like the harness canary is BLIND to #268: the misresolved
// path lands on a real package and the answer comes out right by accident. Only
// a main package with nothing at the root exposes it, and that is the shape 36
// of the fleet's 38 Go repos have.
//
// GoMutationCanaryCode is not reused because the shape needs a `func main` to be
// a main package at all, and because these two controls must be free to drift:
// this one exists to answer a question about one upstream bug.
const (
	GoMutationMainCanaryMod  = "module maincanary\n\ngo 1.21\n"
	GoMutationMainCanaryCode = "package main\n\n// Add is mutable and deliberately under-tested: its mutant must survive.\nfunc Add(a, b int) int { return a + b }\n\nfunc main() { _ = Add(2, 3) }\n"
	GoMutationMainCanaryTest = "package main\n\nimport \"testing\"\n\n// TestAddRuns covers Add without checking it, on purpose.\nfunc TestAddRuns(t *testing.T) {\n\tgot := Add(2, 3)\n\tt.Logf(\"Add(2, 3) = %d\", got)\n}\n"
)

// GoMisgradedFiles is the set of module-relative Go files whose mutants a
// gremlins with #268 grades against the WRONG PACKAGE. Keyed the way gremlins
// names a file in its report.
//
// IT IS NOT EVERY `package main` FILE, and the difference is measured. #268
// resolves a main package's path to the MODULE ROOT — so a main package that IS
// the module root resolves to itself and is graded CORRECTLY (2026-09-24:
// `package main` at the root answered LIVED, the honest verdict; the same code
// at cmd/tool answered KILLED). Only a main package BELOW the root is misgraded.
//
// The first cut of this set was every main file, and foundry-tools is its own
// counter-example: its module root is `package main`, so the gate excluded 11
// mutants in atoms_go.go — three LIVED and two NOT COVERED among them — on the
// same pull that introduced the exclusion. An over-wide set hides real mutants,
// which is the one direction this whole change exists to prevent.
type GoMisgradedFiles map[string]bool

// GoMainFilesFormat is the `go list -f` template that names them: every
// non-test Go file of every package called main, one absolute path per line.
//
// THE TOOLCHAIN ANSWERS THIS, NOT A GREP. `go list` reports the package it
// actually resolved, so a testdata fixture whose first line reads `package main`
// is not counted and a build-tagged file is placed by the same tags the run
// used — where `grep -l '^package main'` would name the fixture and mis-scope
// the exclusion in the direction that hides mutants. _test.go files are left
// out because gremlins does not mutate them.
const GoMainFilesFormat = `{{if eq .Name "main"}}{{$d := .Dir}}{{range .GoFiles}}{{$d}}/{{.}}{{"\n"}}{{end}}{{end}}`

// ParseGoMisgradedFiles reads that template's stdout into paths relative to the
// module at root, keeping only the main-package files BELOW it. A line that is
// not under root does not belong to this module and is dropped; an empty result
// is an answer ("nothing here is misgraded"), which is why the map is always
// non-nil.
func ParseGoMisgradedFiles(out, root string) GoMisgradedFiles {
	files := GoMisgradedFiles{}
	prefix := path.Clean(root) + "/"
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || !strings.HasPrefix(ln, prefix) {
			continue
		}
		rel := strings.TrimPrefix(ln, prefix)
		// A file at the module root is the module root's own package, which #268
		// resolves to correctly. Keeping it would exclude mutants that WERE
		// graded — see GoMisgradedFiles.
		if !strings.Contains(rel, "/") {
			continue
		}
		files[rel] = true
	}
	return files
}
