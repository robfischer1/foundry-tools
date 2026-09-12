package checks

import (
	"strings"
	"testing"
)

// The two enumerations spell a directory differently, and an atom that reads
// the wrong one reports ABSENT on a tree that is right there.
func TestHasEntryReadsEitherSpelling(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string
		want    string
		found   bool
	}{
		{"Entries spelling", []string{"Makefile", "flux", "ansible"}, "flux", true},
		{"Glob spelling", []string{"Makefile", "flux/", "ansible/"}, "flux", true},
		{"a dotted directory", []string{".forgejo/", "README.md"}, ".forgejo", true},
		{"a file", []string{"Dockerfile", "go.mod"}, "Dockerfile", true},
		{"absent", []string{"Dockerfile", "go.mod"}, "flux", false},
		{"a prefix is not a match", []string{"fluxcd"}, "flux", false},
		{"an empty tree", nil, "flux", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasEntry(tc.entries, tc.want); got != tc.found {
				t.Fatalf("HasEntry(%v, %q) = %v, want %v", tc.entries, tc.want, got, tc.found)
			}
		})
	}
}

// THE 57 FALSE COULD-NOT-RUNS, ASSERTED. A star that calls the reusable
// workflow carries no digest of its own — that is an empty population, not a
// broken scan — and one that carries a pin anywhere in the tree has a surface
// the canonical extractor is then obliged to find.
func TestHasPinSurfaceSeparatesAnEmptyTreeFromAPinnedOne(t *testing.T) {
	callsTheReusableWorkflow := `on: [push]
jobs:
  gate:
    uses: foundry/foundry-stocks/.forgejo/workflows/gate.yml@main
`
	pinsAnImage := `jobs:
  build:
    container: registry.notusmi.com/rob/stellar_core:go-ci@sha256:5f684657c2ba294752edcb456efbdf3237290b8a666ebdcd4cb7025431bbdf7a
`
	if HasPinSurface([]string{callsTheReusableWorkflow}) {
		t.Error("a repo that only calls the reusable workflow has an EMPTY pin population; calling it a broken scan is the defect this probe exists to end")
	}
	if !HasPinSurface([]string{pinsAnImage}) {
		t.Error("a tree carrying a digest reference has a surface, and an extractor that finds none there is broken")
	}
	// The surface is the WHOLE workflow tree, not one file: the pin may sit in
	// any of them.
	if !HasPinSurface([]string{callsTheReusableWorkflow, pinsAnImage}) {
		t.Error("one pinned file anywhere under .forgejo/workflows is a surface")
	}
	if HasPinSurface(nil) {
		t.Error("no files is no surface")
	}
	if HasPinSurface([]string{""}) {
		t.Error("an empty file is no surface")
	}
}

// The surface probe must admit every reference the canonical extractor accepts
// — a false negative files a live pin as an absence. digestpins_test.go holds
// that against the canonical pin list; this holds it against the function the
// atom actually calls.
func TestHasPinSurfaceAdmitsEveryCanonicalRef(t *testing.T) {
	for _, pin := range canonicalPins {
		if !HasPinSurface([]string{"image: " + pin + "\n"}) {
			t.Errorf("HasPinSurface missed %q — the atom would call a live pin an absence", pin)
		}
	}
}

func TestBuiltThroughAttestingWorkflowFindsTheThreeBuilds(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"build.yml", "    uses: foundry/foundry-stocks/.forgejo/workflows/build.yml@main\n", true},
		{"frontend-build.yml", "    uses: foundry/foundry-stocks/.forgejo/workflows/frontend-build.yml@main\n", true},
		{"bake-blade.yml", "    uses: foundry/foundry-stocks/.forgejo/workflows/bake-blade.yml@v2\n", true},
		{"no space after uses:", "uses:foundry/foundry-stocks/.forgejo/workflows/build.yml@main\n", true},
		// The gate is not a build, and it attests nothing.
		{"the gate", "    uses: foundry/foundry-stocks/.forgejo/workflows/gate.yml@main\n", false},
		// A repo's own build workflow is not the attesting one — that is the
		// whole finding.
		{"a local build", "    uses: ./.forgejo/workflows/build.yml@main\n", false},
		{"someone else's build.yml", "    uses: other/other-stocks/.forgejo/workflows/build.yml@main\n", false},
		// No ref pinned at all: the pattern requires the @.
		{"no ref", "    uses: foundry/foundry-stocks/.forgejo/workflows/build.yml\n", false},
		{"nothing", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := BuiltThroughAttestingWorkflow([]string{tc.body}); got != tc.want {
				t.Fatalf("BuiltThroughAttestingWorkflow(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

// ONE workflow calling the attesting build is enough — the image is attested,
// whichever file does it.
func TestBuiltThroughAttestingWorkflowReadsTheWholeTree(t *testing.T) {
	bodies := []string{
		"jobs:\n  gate:\n    uses: foundry/foundry-stocks/.forgejo/workflows/gate.yml@main\n",
		"jobs:\n  image:\n    uses: foundry/foundry-stocks/.forgejo/workflows/build.yml@main\n",
	}
	if !BuiltThroughAttestingWorkflow(bodies) {
		t.Fatal("the second file builds through the attesting workflow, so the repo is visible to the re-score")
	}
	if BuiltThroughAttestingWorkflow(bodies[:1]) {
		t.Fatal("the gate does not attest an SBOM")
	}
}

func TestKubeconformSummaryReadsTheValidCount(t *testing.T) {
	for _, tc := range []struct {
		name      string
		out       string
		wantValid int
		wantOK    bool
	}{
		{
			"the measured shape",
			"Summary: 910 resources found in 245 files - Valid: 809, Invalid: 0, Errors: 0, Skipped: 101\n",
			809, true,
		},
		{
			"a summary after findings",
			"flux/app.yaml - Deployment app is invalid: problem\nSummary: 12 resources found in 3 files - Valid: 11, Invalid: 1, Errors: 0, Skipped: 0\n",
			11, true,
		},
		{
			// Every resource skipped: nothing was examined, and 0 invalid is
			// not a statement about the tree.
			"a zero-resource validation",
			"Summary: 395 resources found in 120 files - Valid: 0, Invalid: 0, Errors: 0, Skipped: 395\n",
			0, true,
		},
		{
			// The summary lands on stderr as readily as on stdout, which is
			// why the caller concatenates the two.
			"a summary arriving after stderr noise",
			"failed to open file\nSummary: 2 resources found in 1 files - Valid: 2, Invalid: 0, Errors: 0, Skipped: 0",
			2, true,
		},
		{"no summary at all", "panic: runtime error\n", 0, false},
		{"a summary with no Valid field", "Summary: something else entirely\n", 0, false},
		{"the word Summary mid-line is not the summary", "  Summary: Valid: 4\n", 0, false},
		{"nothing printed", "", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			valid, summary, ok := KubeconformSummary(tc.out)
			if ok != tc.wantOK {
				t.Fatalf("KubeconformSummary(%q) ok = %v, want %v", tc.out, ok, tc.wantOK)
			}
			if valid != tc.wantValid {
				t.Fatalf("KubeconformSummary(%q) valid = %d, want %d", tc.out, valid, tc.wantValid)
			}
			if ok && !strings.HasPrefix(summary, "Summary:") {
				t.Fatalf("the summary line came back as %q", summary)
			}
		})
	}
}

// The summary is printed on a pass, so it must come back whole rather than as
// a count: the skipped figure is the half that says how much the catalogue
// covered.
func TestKubeconformSummaryReturnsTheWholeLine(t *testing.T) {
	line := "Summary: 910 resources found in 245 files - Valid: 809, Invalid: 0, Errors: 0, Skipped: 101"
	_, summary, ok := KubeconformSummary(line + "\n")
	if !ok || summary != line {
		t.Fatalf("KubeconformSummary returned %q, want %q", summary, line)
	}
}

func TestKubeconformFindingsDropsTheSummaryAndCaps(t *testing.T) {
	out := "a.yaml - invalid\nSummary: 2 resources found in 1 files - Valid: 1, Invalid: 1, Errors: 0, Skipped: 0\nb.yaml - invalid\n"
	got := KubeconformFindings(out, 80)
	if strings.Contains(got, "Summary:") {
		t.Errorf("the summary is printed on its own; repeating it in the findings is noise: %q", got)
	}
	if got != "a.yaml - invalid\nb.yaml - invalid" {
		t.Errorf("KubeconformFindings = %q", got)
	}

	var many []string
	for i := 0; i < 500; i++ {
		many = append(many, "line")
	}
	capped := KubeconformFindings(strings.Join(many, "\n"), 80)
	if n := len(strings.Split(capped, "\n")); n != 80 {
		t.Errorf("a wholesale failure printed %d lines past the cap; want 80", n)
	}
	if KubeconformFindings("", 80) != "" {
		t.Error("nothing printed is nothing to report")
	}
}

// THE REMAP THAT MATTERS. kube-linter's zero-population refusal and its
// findings share exit 1, and only the message tells them apart.
func TestKubeLinterStateRemapsTheZeroPopulationRefusal(t *testing.T) {
	state, reason := KubeLinterState(1, "Error: no valid objects found\n")
	if state != 2 {
		t.Fatalf("state = %d, want 2 — a tree nothing parsed is not a tree with findings", state)
	}
	if !strings.Contains(reason, "sweep:kube-linter: CANNOT RUN") {
		t.Fatalf("the refusal must announce itself, got %q", reason)
	}
	// Even at exit 0 the message means nothing was parsed, and a pass would be
	// the silent-skip this module exists to delete.
	if state, _ := KubeLinterState(0, "no valid objects found"); state != 2 {
		t.Fatalf("state = %d at exit 0 with nothing parsed, want 2", state)
	}
}

func TestKubeLinterStatePassesAndFinds(t *testing.T) {
	state, reason := KubeLinterState(0, "KubeLinter 0.8.3\n\nNo lint errors found!\n")
	if state != 0 {
		t.Fatalf("state = %d, want 0", state)
	}
	if !strings.Contains(reason, "clean") {
		t.Fatalf("a pass says so, got %q", reason)
	}

	out := "flux/a.yaml: (object: app apps/v1, Deployment) container \"app\" does not have a read-only root file system\nflux/b.yaml: (object: api apps/v1, Deployment) no resource limits\n\nError: found 2 lint errors\n"
	state, reason = KubeLinterState(1, out)
	if state != 1 {
		t.Fatalf("state = %d, want 1", state)
	}
	// The count leads: it is the line a human reads first, and the tail is
	// where kube-linter puts it.
	lead := strings.SplitN(reason, "\n", 4)
	if len(lead) < 3 || !strings.Contains(strings.Join(lead[:3], "\n"), "Error: found 2 lint errors") {
		t.Fatalf("the reason must lead with the linter's own count, got %q", reason)
	}
	if !strings.Contains(reason, "read-only root file system") {
		t.Fatalf("the findings themselves must survive, got %q", reason)
	}
}

// Every non-zero code that is not the refusal is a finding carrying the
// linter's words — including the codes StateFor would call could-not-run,
// because this function is what decides, not the raw code.
func TestKubeLinterStateCapsALongReport(t *testing.T) {
	var many []string
	for i := 0; i < 500; i++ {
		many = append(many, "finding")
	}
	state, reason := KubeLinterState(1, strings.Join(many, "\n")+"\n")
	if state != 1 {
		t.Fatalf("state = %d, want 1", state)
	}
	if n := len(strings.Split(reason, "\n")); n != 123 {
		t.Fatalf("the reason carried %d lines; want 3 of tail plus 120 of head", n)
	}
}

// A trailing newline must not become a blank line in the tail — the count
// kube-linter prints last is the point of taking a tail at all.
func TestKubeLinterStateTailIgnoresTheTrailingNewline(t *testing.T) {
	_, reason := KubeLinterState(1, "one\ntwo\nthree\nfour\n")
	if !strings.HasPrefix(reason, "two\nthree\nfour\n") {
		t.Fatalf("the tail was %q, want the last three real lines", reason)
	}
}
