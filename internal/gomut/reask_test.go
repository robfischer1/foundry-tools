package gomut

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// A MODULE THAT COULD NOT RUN IS ASKED AGAIN, on its own: the second answer is
// the one reported, the modules that graded are kept, and a second could-not-run
// says it was asked twice.
func TestAModuleThatCouldNotRunIsAskedOnceMore(t *testing.T) {
	// the first ask draws a broken run; the second grades
	p := newPull(t, ".")
	p.asked = map[string]func(int) (string, int){"-changed-since": func(n int) (string, int) {
		if n == 0 {
			return "FAIL TestX", 3
		}
		return "", 0
	}}
	p.report = ""
	v := p.run()
	if v.State != 0 || strings.Contains(said(v), "asked twice") {
		t.Fatalf("the second ask is the answer: %s", said(v))
	}
	if n := p.count["-changed-since"]; n != 2 {
		t.Errorf("gomutants was asked %d times, want 2", n)
	}

	// both asks draw a broken run
	q := newPull(t, ".")
	q.report = ""
	q.first(rule{"-changed-since", "FAIL TestX", 3, ""})
	v = q.run()
	if v.State != 2 || !strings.Contains(v.Reason, "(asked twice, the second time past the engine's cache: it could not run both times)") {
		t.Fatalf("%s", said(v))
	}

	// only a could-not-run is asked again: a finding is not
	r := newPull(t, ".")
	r.report = survivors
	if v := r.run(); v.State != 1 {
		t.Fatalf("%s", said(v))
	}
	if n := countRuns(r, "-changed-since"); n != 1 {
		t.Errorf("a finding was asked %d times", n)
	}
}

func countRuns(p *pull, needle string) int {
	n := 0
	for _, r := range p.runs {
		if strings.Contains(r, needle) {
			n++
		}
	}
	return n
}

// ONLY THE MODULE THAT COULD NOT RUN IS RE-ASKED: a nested module that graded is
// kept, with its gradings.
func TestOnlyTheModuleThatCouldNotRunIsReasked(t *testing.T) {
	p := newPull(t, "tools/forge")
	putFile(t, p.in.Root, "go.mod", "module root\n\ngo 1.26\n")
	p.in.Files = append(p.in.Files, "go.mod")
	p.report = ""
	p.asked = map[string]func(int) (string, int){"-changed-since": func(n int) (string, int) {
		// the root module is asked first and graded nothing (exit 0, no report); the
		// nested one fails once, then grades
		if n == 1 {
			return "FAIL", 3
		}
		return "", 0
	}}
	v := p.run()
	if v.State != 0 {
		t.Fatalf("%s", said(v))
	}
	if got := p.count["-changed-since"]; got != 3 {
		t.Errorf("gomutants was asked %d times, want 3 (root once, nested twice)", got)
	}
}

// AN OVER-BUDGET TIMEOUT IS NOT RERUN (foundry-tools#16081, Rob's ruling): it is a
// deterministic result of the tree and the lane's config, so a second ask is the
// same answer. A could-not-run that is not that one is still asked again.
func TestAnOverBudgetTimeoutIsNotRerun(t *testing.T) {
	over := `{"files":[{"file_name":"a.go","mutations":[` +
		`{"type":"T","status":"TIMED OUT","line":1,"column":1},{"type":"T","status":"TIMED OUT","line":2,"column":1},` +
		`{"type":"T","status":"TIMED OUT","line":3,"column":1},{"type":"T","status":"KILLED","line":4,"column":1}]}]}`
	p := newPull(t, ".")
	p.report = over
	v := p.run()
	if !checks.GoMutationOverBudget(v.State, v.Reason) {
		t.Fatalf("the fixture is not an over-budget run: %s", said(v))
	}
	if n := countRuns(p, "-changed-since"); n != 1 {
		t.Errorf("an over-budget timeout was asked %d times", n)
	}
}
