package gomut

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atoms"
)

// What the run measured decides the verdict, through checks.GoMutationVerdict.
func TestGoMutationSettlesWhatItMeasured(t *testing.T) {
	declaration := `{"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"KILLED","line":1,"column":1},{"type":"ARITHMETIC_BASE","status":"NOT COVERED","line":3,"column":16}]}]}`
	declarationAndSurvivor := `{"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"LIVED","line":2,"column":3},{"type":"ARITHMETIC_BASE","status":"NOT COVERED","line":3,"column":16}]}]}`
	for _, tc := range []struct {
		name   string
		script func(p *pull)
		state  int
		reason []string
	}{
		{"every mutant caught", nil, 0, nil},
		{"a survivor", func(p *pull) { p.report = survivors }, 1,
			[]string{"1 mutant(s) survived or were never covered", "a.go:2:3  LIVED"}},
		{"a canary killed", func(p *pull) { p.canary = canaryKilled }, 2, []string{"the harness scores unrun tests as kills"}},
		// a canary that could not build is a control that could not control, and a
		// clean report still stands on its own
		{"a canary that does not build", func(p *pull) { p.first(rule{"-cache=off", "", 1, ""}) }, 0, nil},
		{"the runner broken, no report", func(p *pull) {
			p.report = ""
			p.first(rule{"-changed-since", "FAIL TestX", 3, ""})
		}, 2, []string{"gomutants exited 3 and wrote no mutation-go.json", "FAIL TestX"}},
		{"nothing to report", func(p *pull) { p.report = "" }, 0, nil},
		// THE CLASS THIS RUN EXISTS FOR. A mutant in a top-level declaration has no
		// coverage block, so gremlins says NOT COVERED forever; the testkit's gate
		// reads the source in the lane and names it. Forgiven is a pass that SAYS
		// what it set aside, and a survivor the classifier did not name is still a
		// survivor.
		{"a declaration the classifier forgave", func(p *pull) {
			p.report = declaration
			p.first(rule{"mutation-gate", `{"noise":[{"file":"a.go","line":3,"column":16,"type":"ARITHMETIC_BASE","status":"NOT COVERED","noise_reason":"declaration"}]}`, 0, ""})
		}, 0, nil},
		{"a declaration forgiven beside a real survivor", func(p *pull) {
			p.report = declarationAndSurvivor
			p.first(rule{"mutation-gate", `{"noise":[{"file":"a.go","line":3,"column":16,"noise_reason":"declaration"}]}`, 0, ""})
		}, 1, []string{"1 mutant(s) survived or were never covered", "[declaration]", "a.go:2:3  LIVED"}},
		// A CLASSIFIER THAT DID NOT ANSWER IS NOT A CLASSIFIER THAT SAID "NOTHING".
		// Scored without it, a clean pull is red for a reason no test can fix; so a
		// survivor with no classification is "could not measure", carrying what the
		// gate said, and a clean run is still clean — there was nothing to classify.
		{"a survivor and the classifier said nothing", func(p *pull) {
			p.report = survivors
			p.first(rule{"mutation-gate", "", 1, "mutation-gate: mutation-go.json: report names a.go, which is not under /src\n"})
		}, 2, []string{"the unkillability classifier did not answer", "report names a.go, which is not under /src"}},
		{"a survivor and the classifier wrote noise that is not JSON", func(p *pull) {
			p.report = survivors
			p.first(rule{"mutation-gate", "| killed | missed |\n", 0, ""})
		}, 2, []string{"the unkillability classifier did not answer", "not mutation-gate JSON"}},
		{"every mutant caught and the classifier said nothing", func(p *pull) { p.first(rule{"mutation-gate", "", 0, ""}) }, 0, nil},
		// The classifier failing to start is a reason the verdict carries by name,
		// never an empty answer that reads as "nothing".
		{"a survivor and the classifier could not run", func(p *pull) {
			p.report = survivors
			p.first(rule{"mutation-gate", "mutation-gate: not found", -1, ""})
		}, 2, []string{"the unkillability classifier did not answer (the engine could not run mutation-gate: mutation-gate: not found)"}},
		{"a survivor and the classifier was killed", func(p *pull) {
			p.report = survivors
			p.first(rule{"mutation-gate", "", 137, ""})
		}, 2, []string{"the unkillability classifier did not answer (the engine could not run mutation-gate: exit code: 137"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPull(t, ".")
			if tc.script != nil {
				tc.script(p)
			}
			v := p.run()
			if v.State != tc.state {
				t.Fatalf("state %d (%s), want %d:\n%s", v.State, v.Result, tc.state, v.Reason)
			}
			for _, r := range tc.reason {
				if !strings.Contains(said(v), r) {
					t.Errorf("the verdict lacks %q:\n%s", r, said(v))
				}
			}
		})
	}

	// The classifier runs on the report the mutants wrote, and a run with no
	// report has nothing to classify.
	p := newPull(t, ".")
	p.run()
	c, ok := p.call("mutation-gate")
	report := filepath.Join(p.work, "go", "root", goMutationReport)
	if !ok || strings.Join(c.Args, " ") != "-report "+report+" -C . -json" || c.Dir != p.in.Root || c.Stderr == nil {
		t.Errorf("mutation-gate: %+v", c)
	}
	q := newPull(t, ".")
	q.report = ""
	q.run()
	if q.ran("mutation-gate") {
		t.Error("mutation-gate ran with no report to read")
	}
}

// A control that did not build never runs the mutation tool, either control.
func TestAControlWhoseTestsDoNotBuildNeverRunsGomutants(t *testing.T) {
	for _, c := range []struct{ name, dir string }{{"the harness control", "canary"}, {"the package-main control", "maincanary"}} {
		p := newPull(t, ".")
		p.first(rule{"go test -cover", "FAIL", 1, ""})
		p.run()
		for _, cmd := range p.cmds {
			if cmd.Name == "gomutants" && strings.Contains(cmd.Dir, c.dir) {
				t.Errorf("gomutants ran %s, whose tests do not build", c.name)
			}
		}
	}
	// and a control that could not be written is a control that did not run
	p := newPull(t, ".")
	if err := os.MkdirAll(filepath.Join(p.work, "go", "root"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.work, "go", "root", "canary"), nil, 0o644); err != nil { // a file where the control's directory goes
		t.Fatal(err)
	}
	if v := p.run(); v.State != 0 || countRuns(p, "-cache=off") != 1 {
		t.Errorf("a control that could not be written still ran (the other control runs): %+v", v)
	}
}

// A CONTROL THAT DID NOT RUN NEVER READS A REPORT, and that is the assertion
// because it is the observable one. An `unknown` control does not red a clean
// run (a control that could not control leaves the report standing on its own),
// so the atom's STATE cannot tell a failed control from a healthy one. What can
// is whether the atom went on to read a verdict out of a run that did not
// produce one.
//
// THESE CASES WERE FOUND BY MUTATION, on this diff, by the tool this diff
// installs: 13 mutants in the root package, 6 survivors, all six inside
// canaryVerdict's two failure branches, because nothing covered them.
func TestAControlThatDidNotRunNeverReadsAReport(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		out  string
	}{
		// MEASURED 2026-09-29: gomutants exits 1 for an unbuildable package and for
		// a package that does not exist. Either is a control that cannot clear the
		// runner, and a report read after it would be a verdict about nothing — or
		// a stale file from a previous layer.
		{"the control run exits 1", 1, ""},
		{"the control run exits 2", 2, ""},
		{"the control could not be started", -1, "gomutants: not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPull(t, ".")
			// the controls would answer an honest report if they were read: a
			// killed canary is a CANNOT RUN, so a read shows in the verdict
			p.canary, p.mainCanary = canaryKilled, mainCanaryKilled
			p.first(rule{"-cache=off", tc.out, tc.code, ""})
			if v := p.run(); v.State != 0 {
				t.Errorf("a control whose run failed still had its report read: %+v", v)
			}
		})
	}
	// AND EXIT 0 IS THE HEALTHY CASE, so the report IS read: a killed canary is
	// read and refused. gomutants exits 0 with survivors — the fixture's mutants
	// are meant to live — which is why the boundary is `!= 0` and not `> 1`.
	p := newPull(t, ".")
	p.canary = canaryKilled
	if v := p.run(); v.State != 2 {
		t.Errorf("a control that exited 0 never had its report read, so it controlled nothing: %+v", v)
	}
}

// A MODULE WHOSE ONLY PACKAGE IS cmd/<name> (paneless's layout) gets a report
// naming "a.go", relative to the package and not the module root. The lane
// renames the files from the root before the classifier or the scorer reads
// them: measured on paneless@1630cf5, where 37 survivors read "the classifier did
// not answer" because mutation-gate looked for ./answer.go.
func TestGoMutationNamesFilesFromTheModuleRootBeforeClassifying(t *testing.T) {
	p := newPull(t, ".")
	p.report = survivors
	p.first(rule{"-f {{.ImportPath}}", "git.notusmi.com/rob/x/cmd/x\t" + filepath.Join(p.in.Root, "cmd/x") + "\n", 0, ""})
	v := p.run()
	if v.State != 1 || !strings.Contains(said(v), "cmd/x/a.go:2:3  LIVED") {
		t.Fatalf("%s", said(v))
	}
	raw, err := os.ReadFile(filepath.Join(p.work, "go", "root", goMutationReport))
	if err != nil || !strings.Contains(string(raw), "cmd/x/a.go") {
		t.Errorf("mutation-gate was handed the report as gomutants wrote it: %s %v", raw, err)
	}

	// THE LISTING FAILING LEAVES THE REPORT AS GOMUTANTS WROTE IT, whatever it
	// printed: the classifier's own refusal then speaks.
	q := newPull(t, ".")
	q.report = survivors
	q.first(rule{"-f {{.ImportPath}}", "git.notusmi.com/rob/x/cmd/x\t" + filepath.Join(q.in.Root, "cmd/x") + "\n", 1, ""})
	q.run()
	if raw, _ := os.ReadFile(filepath.Join(q.work, "go", "root", goMutationReport)); strings.Contains(string(raw), "cmd/x/a.go") {
		t.Errorf("the report was rewritten from a listing that failed: %s", raw)
	}

	// A module-relative report is left exactly as it was written.
	r := newPull(t, ".")
	r.report = survivors
	r.first(rule{"-f {{.ImportPath}}", "git.notusmi.com/rob/x\t" + r.in.Root + "\ngit.notusmi.com/rob/x/cmd/x\t" + filepath.Join(r.in.Root, "cmd/x") + "\n", 0, ""})
	v = r.run()
	if !strings.Contains(said(v), "a.go:2:3  LIVED") || strings.Contains(said(v), "cmd/x/a.go") {
		t.Errorf("a module-relative report was rewritten: %s", said(v))
	}

	// A report that cannot be written back is left as it was, and so is one that
	// does not parse.
	for name, mutate := range map[string]func(){
		"a report that cannot be written back": func() {
			writeReport = func(string, []byte, os.FileMode) error { return os.ErrPermission }
			t.Cleanup(func() { writeReport = os.WriteFile })
		},
		"a report that does not parse": func() {},
	} {
		t.Run(name, func(t *testing.T) {
			mutate()
			u := newPull(t, ".")
			u.report = survivors
			if name == "a report that does not parse" {
				u.report = "this is not json"
			}
			u.first(rule{"-f {{.ImportPath}}", "git.notusmi.com/rob/x/cmd/x\t" + filepath.Join(u.in.Root, "cmd/x") + "\n", 0, ""})
			v := u.run()
			if strings.Contains(said(v), "cmd/x/a.go") {
				t.Errorf("a report that could not be rebased was rewritten: %s", said(v))
			}
			if name == "a report that does not parse" && v.State != 2 {
				t.Errorf("%s", said(v))
			}
		})
	}
}

// THE PACKAGE-MAIN CONTROL EXCLUDES WHAT GREMLINS GRADES AGAINST THE WRONG
// PACKAGE, and the exclusion expires by measurement: while the control answers
// broken, the mutants of the files `go list` names are not counted; the day it
// answers OK they count again.
func TestGoMutationCountsAMisgradedFileOnceItsControlPasses(t *testing.T) {
	// a LIVED mutant in cmd/tool/main.go, a main package below the module root
	lived := `{"files":[{"file_name":"cmd/tool/main.go","mutations":[{"type":"T","status":"LIVED","line":2,"column":3}]}]}`
	list := func(p *pull) string { return filepath.Join(p.in.Root, "cmd/tool/main.go") + "\n" }
	broken := newPull(t, ".")
	broken.report = lived
	broken.mainCanary = mainCanaryKilled
	broken.first(rule{"-f {{if eq .Name", list(broken), 0, ""})
	if v := broken.run(); v.State != 0 {
		t.Errorf("a misgraded file was counted while its control is broken: %s", said(v))
	}
	ok := newPull(t, ".")
	ok.report = lived
	ok.first(rule{"-f {{if eq .Name", list(ok), 0, ""})
	if v := ok.run(); v.State != 1 || !strings.Contains(said(v), "cmd/tool/main.go:2:3  LIVED") {
		t.Errorf("a misgraded file was not counted once its control passes: %s", said(v))
	}
}

// WITHOUT THE MISGRADED SET THERE IS NOTHING TO EXCLUDE WITH, and the verdict
// makes that a could-not-measure — only when the control says the exclusion is
// needed.
func TestGoMutationCannotMeasureWithoutTheMisgradedSet(t *testing.T) {
	for _, tc := range []struct {
		name string
		rule rule
	}{
		{"go list exited 1", rule{"-f {{if eq .Name", "", 1, ""}},
		{"go list never ran", rule{"-f {{if eq .Name", "go: not found", -1, ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPull(t, ".")
			p.report = survivors
			p.mainCanary = mainCanaryKilled
			p.first(tc.rule)
			v := p.run()
			if v.State != 2 {
				t.Errorf("%s", said(v))
			}
			// the control passing makes the set irrelevant
			q := newPull(t, ".")
			q.report = survivors
			q.first(tc.rule)
			if v := q.run(); v.State != 1 {
				t.Errorf("a passing control still needed the set: %s", said(v))
			}
		})
	}
}

// THE PROFILE IS gomutants' OWN, read off the cache file it wrote outside the
// tree: the lane runs no `go test -coverprofile` of its own over the pull.
func TestTheProfileIsReadOffGomutantsCache(t *testing.T) {
	p := newPull(t, ".")
	p.report = `{"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"NOT COVERED","line":3,"column":16}]}]}`
	// the suite covered line 3: the profile corrects a misjudged NOT COVERED
	p.cache = `{"coverage_profile":"mode: set\nx/a.go:3.1,4.2 1 1\n"}`
	p.first(rule{"-f {{.ImportPath}}", "x\t" + p.in.Root + "\n", 0, ""})
	p.run()
	cached := filepath.Join(p.work, "go", "root", goMutationCacheName)
	if _, err := os.Stat(cached); err != nil {
		t.Fatalf("the cache file is not where gomutants was told: %v", err)
	}
	for _, r := range p.runs {
		if strings.Contains(r, "-coverprofile mutation-cover.out") && !strings.Contains(r, "canary") && !strings.Contains(r, filepath.Base(p.work)) {
			t.Errorf("the lane covered the pull itself: %s", r)
		}
	}
	// go:test-race's profile is <GoCoverRoot>/go__test-race/<module>.out.
	if _, ok := p.call("go__test-race/"); ok {
		t.Error("the atom ran the pull's suite for a profile")
	}
}

// A ps the atom cannot link is the atom's could-not-run: gomutants would run
// with a memory cap that never fires.
func TestGoMutationCannotRunWithoutItsPS(t *testing.T) {
	p := newPull(t, ".")
	linkHelper = func(string, string) (string, error) { return "", os.ErrPermission }
	t.Cleanup(func() { linkHelper = atoms.LinkHelper })
	v := p.run()
	if v.State != 2 || !strings.Contains(said(v), "the ps for gomutants' memory cap could not be made: permission denied") {
		t.Errorf("%s", said(v))
	}
}
