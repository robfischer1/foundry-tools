package gomut

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// measure grades the units the diff touched that no stored grading answers. A
// read that fails here keys nothing and changes no verdict.
func (m *module) measure(ctx context.Context, since, changed string, tags []string) checks.Verdict {
	// THE KEYS of the units this diff touches, and — under reuse — which of them
	// a stored grading already answers. Those are not graded again; with every
	// unit answered, nothing below runs at all.
	keys, owner := m.gradingKeys(ctx, since, changed, tags)
	reused, reuseNote := m.reusable(ctx, keys)
	// A SAMPLED RUN GRADES COLD ANYWAY, and audits what it would have reused.
	var audited map[string]checks.ReusedGrading
	if m.audit {
		audited, reused = reused, nil
	}
	misses := checks.Misses(keys, reused)
	if len(reused) > 0 && len(misses) == 0 {
		return m.folded(checks.GoMutationRun{Canary: checks.CanaryOK, MainCanary: checks.CanaryOK, Workers: goWorkers()},
			reused, reuseNote, misses, owner, nil)
	}
	// THE SCOPE IS THE UNITS IT GRADES, on every run (checks.GoMutationScope says
	// why that loses no kill): the changed packages, less any a stored grading
	// answered, as the module's own `go list` names them — never a nested module's
	// directory or a package with no source, which `go test` would refuse and the
	// whole run with it.
	pkgArgs := []string{"list", "-e", "-f", checks.GoSourcePackagesFormat}
	pkgArgs = append(pkgArgs, tags...)
	listed, _ := m.run(ctx, atoms.Cmd{Dir: m.cwd, Name: "go", Args: append(pkgArgs, "./...")})
	mutateArgs := checks.GoMutationScope(m.dir, misses, listed, m.in.Root)

	canary := m.control(ctx, "canary", checks.GoMutationCanaryMod, "canary.go", checks.GoMutationCanaryCode, "canary_test.go", checks.GoMutationCanaryTest)
	// THE PACKAGE-MAIN CONTROL, in a module of its own, SHAPED LIKE THE BUG: one
	// `package main` in a subdirectory and nothing at the module root. That shape
	// was measured, not assumed (2026-09-24, gremlins 0.6.0, this config, the same
	// code both ways): a main package AT the module root is graded correctly and
	// answers LIVED, so a control shaped like the harness canary is blind to #268.
	// Only this shape sees it.
	mainCanary := m.control(ctx, "maincanary", checks.GoMutationMainCanaryMod, "cmd/tool/main.go", checks.GoMutationMainCanaryCode, "cmd/tool/main_test.go", checks.GoMutationMainCanaryTest)
	misgraded, misgradedErr := m.misgraded(ctx, tags)

	report := filepath.Join(m.work, goMutationReport)
	cache := filepath.Join(m.work, goMutationCacheName)
	cmd, log, status := m.mutate(ctx, report, cache, since, tags, mutateArgs)
	if !atoms.ToolRan(status) {
		return m.neverRan(cmd, status, log)
	}
	// gomutants writes no report when it has nothing to report; a read that
	// fails is that absence, and the verdict decides what it means.
	raw, _ := os.ReadFile(report)
	// THE PROFILE gomutants MEASURED, off its cache file (checks.GoCachedProfile).
	cached, _ := os.ReadFile(cache)
	profile := checks.GoCachedProfile(string(cached))

	// NAME THE FILES FROM THE MODULE ROOT before anything reads them. gomutants
	// writes file_name relative to the common import-path prefix of the packages it
	// ran, so a module whose packages all live below cmd/<name> reports "answer.go"
	// and the classifier, looking in the module root, refuses the whole report
	// (checks.GoReportBase). The packages are listed with the patterns and tags the
	// run itself was given, so the base is the run's own. A listing that fails
	// leaves the report as it was: the classifier's own refusal then speaks.
	if len(raw) > 0 {
		raw = m.rebased(ctx, raw, report, tags, mutateArgs)
	}

	// CLASSIFY, IN THE LANE, WHERE THE SOURCE IS. forge-testkit-go's gate parses
	// the tree to say which survivors no test could ever kill — a mutant in a
	// top-level const or var has no coverage block — and answers them under -json
	// for the scorer to fold in. Its own exit code is not the verdict here (this
	// atom holds the canary and the timeout budget); its stdout is. Nothing to
	// write is nothing to classify.
	var classified, classifyErr string
	if len(raw) > 0 {
		classified, classifyErr = m.classify(ctx, report)
	}
	return m.folded(checks.GoMutationRun{
		Status: status, Log: log, Report: raw, Profile: profile, Canary: canary, Workers: goWorkers(),
		Classified: []byte(classified), ClassifyErr: classifyErr,
		MainCanary: mainCanary, MisgradedFiles: misgraded, MisgradedFilesErr: misgradedErr,
	}, reused, reuseNote, misses, owner, audited)
}

// reusable answers the gradings a run may reuse for its keys, and a line for the
// verdict when it asked and was not answered. No lookup asks nothing.
func (m *module) reusable(ctx context.Context, keys []checks.UnitKey) (map[string]checks.ReusedGrading, string) {
	if m.lookup == nil {
		return nil, ""
	}
	got, err := m.lookup(ctx, "go", goMutationEngine(), keys)
	if err != nil {
		return nil, "reuse: the store did not answer, so every unit was graded cold — " + err.Error()
	}
	return got, ""
}

// control writes a control module and answers what the harness did with it. THE
// CANARIES RUN FIRST AND DECIDE WHETHER THE REPORT IS A MEASUREMENT. A harness
// that cannot start the test child scores every mutant KILLED; the control is a
// module whose one mutant must LIVE (checks.GoMutationCanary). The bound it
// existed for — gremlins v0.6.0 mangled --test-cpu into one argv word, #7649 — is
// kept through the environment the child inherits: GOMAXPROCS caps the test
// binary, GOFLAGS=-p caps concurrent builds. A control that could not be written
// or whose test would not pass answers unknown, as a report that could not be
// read does.
func (m *module) control(ctx context.Context, name, mod, srcPath, src, testPath, test string) string {
	dir := filepath.Join(m.work, name)
	for rel, body := range map[string]string{"go.mod": mod, srcPath: src, testPath: test} {
		if err := put(dir, rel, body); err != nil {
			return checks.CanaryUnknown
		}
	}
	if _, code := m.run(ctx, atoms.Cmd{Dir: dir, Name: "go", Args: []string{"test", "-cover", "-coverprofile", goMutationProfile, "./..."}}); code != 0 {
		return checks.CanaryUnknown
	}
	return m.canaryVerdict(ctx, dir)
}

// canaryVerdict runs a control and reads its JSON REPORT, never its stdout.
//
// THE REPORT IS THE ONLY SURFACE WITH A CONTRACT. The old canary matched the
// literal string "Killed: 0, Lived: 1" in gremlins' summary line, which tied a
// control — the one thing in this lane that decides whether anything was
// measured at all — to a tool's human-readable formatting AND to the exact
// mutant COUNT. The honest verdict is stated as a shape: the fixture's mutants
// are all under-tested on purpose, so a run that GRADED them kills none and
// survives at least one. Killing them means the runner is not running what it
// thinks it is.
//
// EXIT 0 OR IT DID NOT RUN, measured rather than guessed (gomutants v0.6.1):
// the canary with TWO survivors exits 0, while an unbuildable package and a
// package that does not exist both exit 1. A run that did not exit 0 never has
// its report read, so GoMutationCanary answers unknown, as it does for an empty
// or unparseable one, because they are the same fact: nothing was graded.
func (m *module) canaryVerdict(ctx context.Context, dir string) string {
	report := filepath.Join(dir, goMutationReport)
	args := []string{"-config", goMutationNoConfig, "-workers", "1", "-cache=off", "-disable", goMutationDisable, "-output", report, "./..."}
	var body string
	if _, code := m.run(ctx, atoms.Cmd{Dir: dir, Name: "gomutants", Args: args}); code == 0 {
		raw, _ := os.ReadFile(report)
		body = string(raw)
	}
	return checks.GoMutationCanary(body)
}

// misgraded names the files the package-main control says this gomutants grades
// against the wrong package — WHICH FILES THAT CONTROL GOVERNS, THE TOOLCHAIN
// ANSWERS, NOT A GREP. `go list` reports the package it actually resolved, so a
// testdata fixture whose first line reads `package main` is not named and a
// build-tagged file is placed by the same tags the cover step used. A grep would
// name the fixture and widen the exclusion — the direction that hides mutants.
// ParseGoMisgradedFiles then drops the module root's own main package, which
// #268 resolves to correctly; THIS repo is the reason that filter exists — its
// root is `package main`, and the first cut excluded 11 mutants of atoms_go.go,
// three of them LIVED, on the pull that added the exclusion. Failing to list them
// is carried, not swallowed: without the set there is nothing to exclude with,
// and the verdict makes that a could-not-measure.
func (m *module) misgraded(ctx context.Context, tags []string) (checks.GoMisgradedFiles, string) {
	args := append(append([]string{"list", "-e", "-f", checks.GoMainFilesFormat}, tags...), "./...")
	out, code := m.run(ctx, atoms.Cmd{Dir: m.cwd, Name: "go", Args: args})
	switch {
	case code < 0:
		return checks.GoMisgradedFiles{}, out
	case code != 0:
		return checks.GoMisgradedFiles{}, fmt.Sprintf("go list exited %d", code)
	}
	return checks.ParseGoMisgradedFiles(out, m.cwd), ""
}

// mutate runs gomutants over the scope and answers everything it printed and its
// exit code (-1 with the reason when it would not start).
//
// THE KNOBS ARE THE ARGV AND THE CONSTS BESIDE IT; goMutationNoConfig is what
// stops the tree's own .gomutants.yml joining them. THE SCOPE IS THE CHANGED
// LINES, and gomutants reads them itself off `-changed-since` rather than being
// handed a file list. diff.context=0 is kept as cheap insurance: it can only
// narrow what git reports. THE BASELINE IS NEVER CACHED: -count=1 keeps every run
// a real one, and the cache file is outside the tree and starts empty.
//
// GOMUTANTS' MEMORY CAP NEEDS NO ps. Since v0.7.1 (upstream #107) it reads each
// mutant group's resident set from /proc itself on Linux
// (internal/runner/worker_rss_linux.go; the `ps -A` path is for other Unixes, by
// absolute path), so nothing on PATH stands in for ps.
func (m *module) mutate(ctx context.Context, report, cache, since string, tags, scope []string) (atoms.Cmd, string, int) {
	bin := filepath.Join(atoms.WorkDir(), "bin")
	gitEnv, err := m.extra.gitEnv(bin)
	if err != nil {
		return atoms.Cmd{Name: "gomutants"}, "the git that hands gomutants the carried ranges could not be made: " + err.Error(), -1
	}
	args := []string{"-output", report, "-config", goMutationNoConfig, "-workers", strconv.Itoa(goWorkers()),
		"-disable", goMutationDisable, "-exclude-files", goMutationExclude, "-cache", cache, "-changed-since", since}
	args = append(append(args, tags...), scope...)
	env := append([]string{"GOMAXPROCS=1", "GOFLAGS=" + goMutationGoflags},
		atoms.GitConfig("diff.relative=true", "diff.context=0")...)
	if len(gitEnv) > 0 {
		// The linked git that hands gomutants the carried ranges is first on PATH.
		env = append(env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	env = append(env, gitEnv...)
	cmd := atoms.Cmd{Dir: m.cwd, Name: "gomutants", Args: args, Env: env, Both: true, Tee: true}
	out, code := m.run(ctx, cmd)
	return cmd, out, code
}

// rebased names the report's files from the module root (see measure).
func (m *module) rebased(ctx context.Context, raw []byte, report string, tags, scope []string) []byte {
	args := append(append([]string{"list", "-e", "-f", checks.GoPackagesFormat}, tags...), scope...)
	out, code := m.run(ctx, atoms.Cmd{Dir: m.cwd, Name: "go", Args: args})
	if code != 0 {
		return raw
	}
	rel := checks.GoReportBase(out, m.cwd)
	if rel == "" {
		return raw
	}
	rebased, err := checks.RebaseGoReport(raw, rel)
	if err != nil || writeReport(report, rebased, 0o644) != nil {
		return raw
	}
	return rebased
}

// writeReport is os.WriteFile, a variable so a test can say the report cannot be
// written back.
var writeReport = os.WriteFile

// classify answers mutation-gate's stdout and stderr separately: the JSON is the
// answer, and what it said when it had none is the reason the verdict carries. A
// program that would not start is a reason too, never a silent empty answer.
func (m *module) classify(ctx context.Context, report string) (out, errOut string) {
	var stderr string
	out, code := m.run(ctx, atoms.Cmd{Dir: m.cwd, Name: "mutation-gate", Args: []string{"-report", report, "-C", ".", "-json"}, Stderr: &stderr})
	if !atoms.ToolRan(code) {
		why := strings.TrimPrefix(atoms.NeverRanReason([]string{"mutation-gate"}, code, out), "the atom never ran: ")
		return "", "the engine could not run mutation-gate: " + why
	}
	return out, stderr
}

// linkHelper is atoms.LinkHelper, a variable so a test can say it cannot link.
var linkHelper = atoms.LinkHelper

// put writes a file under dir, making its directories.
func put(dir, rel, body string) error {
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(body), 0o644)
}

// folded settles a Go mutation run: what it graded, folded with what it reused
// (checks.GoMutationVerdictReusing — with nothing reused, the cold verdict
// exactly), and the gradings of the units it graded itself.
//
// THE FINDINGS COME FROM THE SCORE, NOT FROM THE REASON. checks.VerdictOf leaves
// them nil for this atom on purpose — FindingsOf does not parse the mutation
// report, because the report is a RENDERING of the score and reading it back
// would lose a distinction the score keeps (an ungraded LIVED mutant and a missed
// LIVED mutant render identically).
func (m *module) folded(run checks.GoMutationRun, reused map[string]checks.ReusedGrading, note string,
	misses []checks.UnitKey, owner func(string) (string, bool), audited map[string]checks.ReusedGrading) checks.Verdict {
	list := checks.Reused(reused)
	f := checks.GoMutationVerdictReusing(run, m.dir, list)
	v := m.settle(f.State, f.Reason)
	// THE REUSE RIDES BESIDE THE VERDICT, like the scope line: a pass keeps no
	// output, and what a pass did not grade itself is what its reader needs.
	for _, line := range []string{note, checks.ReuseLine(id, list, len(list)+len(misses)), m.coverNote} {
		if line != "" {
			v.Reason = line + "\n" + v.Reason
		}
	}
	v.Findings = f.Findings
	if m.extra != nil {
		v.Findings = append(v.Findings, m.extra.moved...)
	}
	v.Gradings = checks.GoGradings(f.Fresh, m.dir, goMutationEngine(), checks.GoGradingTrusted(f.State, run.Canary), misses, owner)
	if len(audited) > 0 {
		v.Audit = checks.AuditGradings(checks.Reused(audited), v.Gradings)
		v.Reason = id + ": audit — the " + strconv.Itoa(len(audited)) + " unit(s) a lookup answered were graded cold again: " + v.Audit + "\n" + v.Reason
	}
	return v
}
