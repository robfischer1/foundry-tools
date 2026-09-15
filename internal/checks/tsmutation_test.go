package checks

import (
	"fmt"
	"strings"
	"testing"
)

func TestTSMutationSpecsGlobTheDeclarationOrTakeEverySource(t *testing.T) {
	if got := strings.Join(TSMutationSpecs(" src/**/*.ts  lib/a.ts "), "|"); got != ":(glob)src/**/*.ts|:(glob)lib/a.ts" {
		t.Errorf("declared: %s", got)
	}
	if got := strings.Join(TSMutationSpecs("  "), "|"); got != "*.ts|*.tsx|:!*.test.ts|:!*.test.tsx|:!*.spec.ts|:!*.spec.tsx|:!*__tests__/*|:!node_modules/" {
		t.Errorf("undeclared: %s", got)
	}
}

func TestStrykerRangesAreTheLinesEachHunkAdds(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/src/a.ts b/src/a.ts",
		"--- a/src/a.ts",
		"+++ b/src/a.ts\t",
		"@@ -1,0 +2,2 @@",
		"+x",
		"@@ -9 +11 @@",
		"@@ -20,3 +22,0 @@", // a deletion adds nothing
		"@@ malformed @@",
		"+++ /dev/null", // a deleted file: its hunks name no range
		"@@ -1,5 +0,0 @@",
		"@@ -1 +1 @@",
		"+++ b/lib/b.tsx",
		"@@ -4,2 +4,3 @@",
	}, "\n")
	var got []string
	for _, r := range StrykerRanges(diff) {
		got = append(got, r.String())
	}
	if strings.Join(got, ",") != "src/a.ts:2-3,src/a.ts:11-11,lib/b.tsx:4-6" {
		t.Errorf("ranges %v", got)
	}
	if StrykerRanges("@@ -1 +1 @@") != nil {
		t.Errorf("a hunk before any file header names no range")
	}
}

func TestPlanStrykerHandsEachRangeToItsNearestConfig(t *testing.T) {
	dirs := StrykerConfigDirs([]string{
		"stryker.config.json", "packages/engine/.stryker.conf.mjs", "packages/engine/src/stryker.md",
		"apps/web/strykerconfig.json",
	})
	if len(dirs) != 2 || !dirs["."] || !dirs["packages/engine"] {
		t.Fatalf("config dirs %v", dirs)
	}
	ranges := []StrykerRange{
		{"packages/engine/src/index.ts", 2, 2},
		{"src/identity.ts", 2, 3},
		{"packages/engine/src/deep/x.ts", 5, 6},
	}
	plan, orphans := PlanStryker(ranges, dirs)
	if len(orphans) != 0 || len(plan) != 2 {
		t.Fatalf("plan %+v orphans %v", plan, orphans)
	}
	if plan[0].Dir != "packages/engine" || plan[0].Mutate() != "src/index.ts:2-2,src/deep/x.ts:5-6" {
		t.Errorf("the first package the diff names comes first, its ranges relative to it: %+v", plan[0])
	}
	if plan[1].Dir != "." || plan[1].Mutate() != "src/identity.ts:2-3" {
		t.Errorf("a root range keeps its path: %+v", plan[1])
	}

	// With no root config, what no package owns is orphaned, sorted, once.
	delete(dirs, ".")
	_, orphans = PlanStryker(append(ranges, StrykerRange{"src/identity.ts", 9, 9}, StrykerRange{"/abs/x.ts", 1, 1}), dirs)
	if strings.Join(orphans, ",") != "/abs/x.ts,src/identity.ts" {
		t.Errorf("orphans %v", orphans)
	}
}

func TestStrykerBinCandidatesWalkToTheRoot(t *testing.T) {
	if got := strings.Join(StrykerBinCandidates("packages/engine"), ","); got != "packages/engine/node_modules/.bin/stryker,packages/node_modules/.bin/stryker,node_modules/.bin/stryker" {
		t.Errorf("candidates %s", got)
	}
	if got := strings.Join(StrykerBinCandidates("."), ","); got != "node_modules/.bin/stryker" {
		t.Errorf("root candidates %s", got)
	}
}

// fakeRegistry answers DiagnoseInstall from maps, and records what it was asked.
type fakeRegistry struct {
	versions map[string][]string
	serves   map[string]bool
	asked    []string
}

func (f *fakeRegistry) Versions(registry, name string) []string {
	f.asked = append(f.asked, registry+" "+name)
	return f.versions[registry+"/"+name]
}

func (f *fakeRegistry) Serves(url string) bool {
	f.asked = append(f.asked, url)
	return f.serves[url]
}

func TestDiagnoseInstall(t *testing.T) {
	const reg = "https://nexus.example/npm"
	tgz := func(name, base, v string) string { return fmt.Sprintf("%s/%s/-/%s-%s.tgz", reg, name, base, v) }

	scoped := func(v string) string { return tgz("@scope/pkg", "pkg", v) }
	listed := map[string][]string{reg + "/@scope/pkg": {"0.9.0", "1.0.0", "1.1.0", "1.2.0", "1.3.0", "1.4.0", "2.0.0", "2.1.0"}}
	log := "GET " + scoped("2.1.0") + " - 503\nGET " + scoped("2.1.0") + " - 503\n"

	// A scoped package listed but not served: the versions before it that serve,
	// nearest first, stopping at two.
	f := &fakeRegistry{versions: listed, serves: map[string]bool{scoped("2.0.0"): true, scoped("1.4.0"): true, scoped("1.3.0"): true}}
	state, reason := DiagnoseInstall(log, 1, f)
	if state != 1 || !strings.Contains(reason, "@scope/pkg@2.1.0 IS listed in "+reg+"'s metadata but its tarball returns 503") ||
		!strings.Contains(reason, "Versions that DO serve: 2.0.0, 1.4.0.") || !strings.Contains(reason, "running again changes nothing") {
		t.Errorf("mismatch: %d %s", state, reason)
	}
	if strings.Join(f.asked, "|") != reg+" @scope/pkg|"+scoped("2.0.0")+"|"+scoped("1.4.0") {
		t.Errorf("a repeated URL is diagnosed once, and the walk stops at two: %v", f.asked)
	}

	// The walk looks back through four versions and no further.
	f = &fakeRegistry{versions: listed, serves: map[string]bool{scoped("1.2.0"): true, scoped("1.1.0"): true}}
	if state, reason = DiagnoseInstall(log, 1, f); state != 1 || !strings.Contains(reason, "Versions that DO serve: 1.2.0.") {
		t.Errorf("window: %d %s", state, reason)
	}
	if strings.Join(f.asked, "|") != reg+" @scope/pkg|"+scoped("2.0.0")+"|"+scoped("1.4.0")+"|"+scoped("1.3.0")+"|"+scoped("1.2.0") {
		t.Errorf("window asked %v", f.asked)
	}

	// Nothing near it serves, and the walk stops at the start of the list.
	f = &fakeRegistry{versions: map[string][]string{reg + "/left-pad": {"1.0.0", "1.1.0"}}}
	if state, reason = DiagnoseInstall("GET "+tgz("left-pad", "left-pad", "1.1.0")+" - 404", 1, f); state != 1 || !strings.Contains(reason, "No nearby version served either") {
		t.Errorf("none serve: %d %s", state, reason)
	}
	if len(f.asked) != 2 {
		t.Errorf("walked past the first version: %v", f.asked)
	}

	// Not indexed at all.
	f = &fakeRegistry{versions: map[string][]string{reg + "/left-pad": {"1.0.0"}}}
	if state, reason = DiagnoseInstall("GET "+tgz("left-pad", "left-pad", "9.9.9")+" - 404", 1, f); state != 1 || !strings.Contains(reason, "Version not in registry: left-pad@9.9.9 is not indexed by "+reg) {
		t.Errorf("unindexed: %d %s", state, reason)
	}

	// A URL with nothing to read a package from is skipped for the next one.
	f = &fakeRegistry{}
	if state, reason = DiagnoseInstall("GET https://x.example/odd.tgz - 404\nGET "+tgz("a", "a", "1.0.0")+" - 404", 1, f); state != 1 || !strings.Contains(reason, "Version not in registry: a@1.0.0") {
		t.Errorf("skip unparsed: %d %s", state, reason)
	}
	if state, reason = DiagnoseInstall("GET https://x.example/odd.tgz - 404", 1, f); state != 1 || !strings.Contains(reason, "no /-/") {
		t.Errorf("only unparsed: %d %s", state, reason)
	}

	// No tarball failure: a network fault is could-not-run, anything else a
	// finding carrying the install's tail.
	long := strings.Repeat("resolving\n", 30)
	if state, reason = DiagnoseInstall(long+"error: connect ETIMEDOUT 10.0.0.1:443", 1, f); state != 2 || !strings.Contains(reason, "network fault (rc=1)") || strings.Count(reason, "resolving") != 19 {
		t.Errorf("fault: %d %s", state, reason)
	}
	if state, reason = DiagnoseInstall("error: lockfile had changes, but lockfile is frozen\n", 1, f); state != 1 || !strings.Contains(reason, "exited 1 and not on a tarball fetch") || !strings.Contains(reason, "lockfile is frozen") {
		t.Errorf("other: %d %s", state, reason)
	}
}

func TestRegistryVersionsKeepTheDocumentsOrder(t *testing.T) {
	got, err := RegistryVersions([]byte(`{"name":"x","dist-tags":{"latest":"1.0.0"},"versions":{"2.0.0":{"a":[1]},"1.0.0":{},"10.0.0":{}},"time":{}}`))
	if err != nil || strings.Join(got, ",") != "2.0.0,1.0.0,10.0.0" {
		t.Errorf("versions %v %v", got, err)
	}
	for _, bad := range []string{"", `{"versions":`, `{"versions":{"1":`, `{"x":`, `{"versions":{"1":{}`, `{"versions":[`} {
		if got, err := RegistryVersions([]byte(bad)); err == nil && len(got) > 0 {
			t.Errorf("%q read as %v", bad, got)
		}
	}
	if _, err := RegistryVersions([]byte(`{"versions":{"1":`)); err == nil {
		t.Errorf("a truncated version is an error")
	}
	if _, err := RegistryVersions([]byte(`{1}`)); err == nil {
		t.Errorf("a non-object is an error")
	}
	if _, err := RegistryVersions(nil); err == nil {
		t.Errorf("nothing is an error")
	}
	if _, err := RegistryVersions([]byte(`{"a":}`)); err == nil {
		t.Errorf("a bad skipped value is an error")
	}
	if _, err := RegistryVersions([]byte(`{"versions":1}`)); err == nil {
		t.Errorf("versions that are not an object are an error")
	}
	if _, err := RegistryVersions([]byte(`{"versions":{"1":{}}`)); err == nil {
		t.Errorf("an unclosed document is an error")
	}
}

func TestPatchVitestRunnerOnlyWhatItCanProveBroken(t *testing.T) {
	old := "a;\n" + vitestRunnerOld + "\n"
	fixed := "a;\n" + vitestRunnerNew + "\n"
	if PackageVersion(`{"version":"10.0.0"}`) != "10.0.0" || PackageVersion("not json") != "" {
		t.Fatal("PackageVersion")
	}
	got := PatchVitestRunner("10.0.0", "5.0.0", []string{old, old})
	if len(got) != 2 || got[0] != fixed || got[1] != fixed {
		t.Errorf("a broken copy is patched: %q", got)
	}
	for name, c := range map[string]struct {
		runner, vitest string
		files          []string
	}{
		"another release": {"10.0.1", "5.0.0", []string{old, old}},
		"vitest 4":        {"10.0.0", "4.1.11", []string{old, old}},
		"no vitest":       {"10.0.0", "", []string{old, old}},
		"already patched": {"10.0.0", "6.2.0", []string{fixed, fixed}},
		"the join moved":  {"10.0.0", "5.0.0", []string{old, "a;"}},
		"joined twice":    {"10.0.0", "5.0.0", []string{old + old, old}},
		"half patched":    {"10.0.0", "5.0.0", []string{old, old + fixed}},
		"a missing file":  {"10.0.0", "5.0.0", []string{old, ""}},
	} {
		if got := PatchVitestRunner(c.runner, c.vitest, c.files); got != nil {
			t.Errorf("%s: patched %q", name, got)
		}
	}
}

// report builds a Stryker mutation.json.
func report(cfg, testFiles, files string) string {
	return `{"config":` + cfg + `,"testFiles":` + testFiles + `,"files":` + files + `}`
}

const (
	vitestCfg = `{"testRunner":"vitest","mutate":["src/a.ts:1-9"],"coverageAnalysis":"perTest"}`
	oneTest   = `{"a.test.ts":{"tests":[{"id":"1"},{"id":"2"}]}}`
)

func mutant(status, mutator string, line int, tests string) string {
	return fmt.Sprintf(`{"status":%q,"mutatorName":%q,%s"location":{"start":{"line":%d}}}`, status, mutator, tests, line)
}

func TestScoreStryker(t *testing.T) {
	src := `"source":"one\ntwo\nthree\nconsole.log(\nfive\nsix\nseven\neight\n"`
	files := `{"src/b.ts":{"source":"","mutants":[` + mutant("Survived", "BooleanLiteral", 1, `"testsCompleted":2,`) + `]},` +
		`"src/a.ts":{` + src + `,"mutants":[` + strings.Join([]string{
		mutant("Killed", "X", 1, ""), mutant("Timeout", "X", 2, ""), mutant("CompileError", "X", 3, ""), mutant("RuntimeError", "X", 3, ""),
		mutant("Survived", "StringLiteral", 5, `"testsCompleted":1,`), // console.log( two lines up: a message
		mutant("Survived", "StringLiteral", 9, `"testsCompleted":1,`), // console.log( five lines up: not
		mutant("Survived", "StringLiteral", 0, `"testsCompleted":1,`), // no line
		mutant("NoCoverage", "StringLiteral", 5, ""),                  // uncovered, never message noise
		mutant("NoCoverage", "BlockStatement", 7, ""),                 // ratified below
		mutant("Survived", "EqualityOperator", 8, `"testsCompleted":1,`),
	}, ",") + `]}}`
	ex := `{"exemptions":[{"file":"src/a.ts","line":7,"mutator":"BlockStatement","reason":"ruling-12","ratifiedBy":"rob"},{"file":"src/a.ts","line":8,"mutator":"Other","reason":"x","ratifiedBy":"rob"}]}`
	s, err := ScoreStryker(report(vitestCfg, oneTest, files), ex)
	if err != nil || s.State != 0 {
		t.Fatalf("%+v %v", s, err)
	}
	// killed 2 (Killed+Timeout), survived 5, no-coverage 2, inert 2; noise 2.
	for _, want := range []string{
		"### Mutation gate — typescript (diff)",
		"| 2 | 5 | 2 | 2 | 22.2% of 9 | 28.6% of 7 |",
		"| vitest | `(runner-internal)` | perTest | 2 | `src/a.ts:1-9` |",
		"**Discounted (2)**",
		"src/a.ts:5  Survived   StringLiteral   [unasserted-message-string]",
		"src/a.ts:7  NoCoverage BlockStatement   [ratified:ruling-12]",
		"src/a.ts:9  Survived   StringLiteral\n",
		"src/a.ts:0  Survived   StringLiteral\n",
		"src/a.ts:5  NoCoverage StringLiteral\n",
		"src/a.ts:8  Survived   EqualityOperator\n",
		"**Survivors** — each is a HYPOTHESIS",
	} {
		if !strings.Contains(s.Summary, want) {
			t.Errorf("summary lacks %q:\n%s", want, s.Summary)
		}
	}
	if s.Missed != 5 {
		t.Errorf("missed %d, want 5", s.Missed)
	}
	if strings.Index(s.Summary, "src/a.ts:8") > strings.Index(s.Summary, "src/b.ts:1") {
		t.Errorf("files are scored in path order")
	}
	if strings.Contains(s.Summary, "1 by construction") {
		t.Errorf("the command-runner note is for the command runner")
	}

	// Clean: no discount block, no survivor block, 100 printed as 100.
	s, _ = ScoreStryker(report(vitestCfg, oneTest, `{"src/a.ts":{"mutants":[`+mutant("Killed", "X", 1, "")+`]}}`), "")
	if s.State != 0 || s.Missed != 0 || !strings.Contains(s.Summary, "| 1 | 0 | 0 | 0 | 100% of 1 | 100% of 1 |") ||
		strings.Contains(s.Summary, "**Discounted") || strings.Contains(s.Summary, "**Survivors") {
		t.Errorf("clean: %+v", s)
	}

	// The command runner: one opaque test, said so; the config left unset reads unset.
	s, _ = ScoreStryker(report(`{"testRunner":"command","commandRunner":{}}`, `{"x":{"tests":[{}]}}`, `{}`), "")
	if s.State != 0 || !strings.Contains(s.Summary, "| command | `(unset)` | ? | 1 | `(unset)` |") || !strings.Contains(s.Summary, "1 by construction") {
		t.Errorf("command runner: %+v", s)
	}
	s, _ = ScoreStryker(report(`{"testRunner":"command","commandRunner":{"command":"npm test"},"mutate":[]}`, `{}`, `{}`), "")
	if s.State != 0 || !strings.Contains(s.Summary, "| command | `npm test` | ? | 0 | `` |") {
		t.Errorf("a command runner with zero tests is still a measurement: %+v", s)
	}
	s, _ = ScoreStryker(report(`{}`, `{}`, `{}`), "")
	if s.State != 1 || s.Missed != 1 || !strings.Contains(s.Error, "Nothing was measured: the report records ZERO tests") ||
		!strings.Contains(s.Summary, "| (unset) | `(runner-internal)` | ? | 0 | `(unset)` |") {
		t.Errorf("zero tests: %+v", s)
	}

	// A survivor that completed zero tests: the runner measured nothing.
	s, _ = ScoreStryker(report(vitestCfg, oneTest, `{"src/a.ts":{"mutants":[`+
		mutant("Survived", "X", 3, `"testsCompleted":0,`)+","+mutant("Survived", "Y", 4, `"testsCompleted":0,`)+`]}}`), "")
	if s.State != 2 || !strings.Contains(s.Error, "2 survivor(s) completed ZERO tests (first: src/a.ts:3 X)") || !strings.Contains(s.Summary, "| 0 | 2 |") {
		t.Errorf("unmeasured: %+v", s)
	}

	// An unratified exemption refuses the discount before the report is read.
	s, err = ScoreStryker("not json", `{"exemptions":[{"file":"a","line":1,"mutator":"M","ratifiedBy":"rob"},{"file":"b","line":2,"mutator":"N"}]}`)
	if err != nil || s.State != 1 || !strings.Contains(s.Error, "carries 1 exemption(s) with no `ratifiedBy` — b:2 N.") {
		t.Errorf("unratified: %+v %v", s, err)
	}
	if _, err := ScoreStryker(report(vitestCfg, oneTest, `{}`), "{"); err == nil || !strings.Contains(err.Error(), "stryker-honest.json") {
		t.Errorf("exemptions that do not parse: %v", err)
	}
	if _, err := ScoreStryker("{", ""); err == nil || !strings.Contains(err.Error(), "mutation.json") {
		t.Errorf("a report that does not parse: %v", err)
	}
}

func TestTSMutationVerdict(t *testing.T) {
	clean := report(vitestCfg, oneTest, `{"src/a.ts":{"mutants":[`+mutant("Killed", "X", 1, "")+`]}}`)
	survived := report(vitestCfg, oneTest, `{"src/a.ts":{"mutants":[`+mutant("Survived", "X", 1, `"testsCompleted":1,`)+","+mutant("NoCoverage", "Y", 2, "")+`]}}`)
	zeroLog := "Instrumented 1 source file(s) with 0 mutant(s)\nNo tests were executed"
	for _, c := range []struct {
		name   string
		runs   []StrykerRun
		state  int
		want   []string
		absent []string
	}{
		{"clean", []StrykerRun{{Dir: ".", Report: clean}}, 0, []string{"every viable mutant was caught", "| 1 | 0 |"}, []string{"###  ."}},
		{"survivors summed across packages", []StrykerRun{{Dir: "a", Report: survived}, {Dir: ".", Report: survived}, {Dir: "z", Log: zeroLog, Status: 1}}, 1,
			[]string{"4 mutant(s) survived or were never covered", "### a\n\n### Mutation gate", "### .\n\n", "### z\n\nnothing to mutate: Stryker instrumented 0 mutants"}, nil},
		{"one package's survivors carry no heading", []StrykerRun{{Dir: "pkg", Report: survived}}, 1, []string{"2 mutant(s) survived", "\n\n### Mutation gate"}, []string{"### pkg"}},
		{"nothing instrumented anywhere", []StrykerRun{{Dir: "a", Log: zeroLog, Status: 1}, {Dir: "b", Log: zeroLog}}, 0, []string{"hold no mutable code"}, nil},
		{"no report", []StrykerRun{{Dir: ".", Report: clean}, {Dir: "b", Status: 1, Log: "ConfigError: boom"}}, 2,
			[]string{"stryker exited 1 in b and wrote no reports/mutation/mutation.json", "ConfigError: boom"}, nil},
		{"a report that does not parse", []StrykerRun{{Dir: ".", Report: "{"}}, 2, []string{"the mutation report could not be read"}, []string{" in ."}},
		{"a finding from the score", []StrykerRun{{Dir: "a", Report: report(`{}`, `{}`, `{}`)}, {Dir: "b", Report: clean}}, 1, []string{"Nothing was measured: the report records ZERO tests", "it is not a measurement. in a\n"}, nil},
		{"could-not-run from the score", []StrykerRun{{Dir: ".", Report: report(vitestCfg, oneTest, `{"f":{"mutants":[`+mutant("Survived", "X", 1, `"testsCompleted":0,`)+`]}}`)}}, 2,
			[]string{"completed ZERO tests"}, nil},
		{"a broken run with a report", []StrykerRun{{Dir: ".", Report: clean, Status: 1, Log: "Error: runner crashed"}}, 2,
			[]string{"stryker exited 1 — a broken run, not a survivor report", "runner crashed"}, nil},
	} {
		state, reason := TSMutationVerdict(c.runs)
		if state != c.state {
			t.Errorf("%s: state %d, want %d\n%s", c.name, state, c.state, reason)
		}
		for _, w := range c.want {
			if !strings.Contains(reason, w) {
				t.Errorf("%s: reason lacks %q:\n%s", c.name, w, reason)
			}
		}
		for _, a := range c.absent {
			if strings.Contains(reason, a) {
				t.Errorf("%s: reason carries %q:\n%s", c.name, a, reason)
			}
		}
	}
}
