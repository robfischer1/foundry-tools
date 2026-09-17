package checks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// THE TYPESCRIPT MUTATION GATE'S DECISIONS, in Go. They were foundry-stocks'
// ci/lib/mutation/ts.sh and the four node helpers beside it — ranges.mjs (the
// diff as Stryker ranges), honest.mjs (the score), diagnose.mjs (why an install
// failed) and runner-patch.mjs (stryker-js#6210). The atom (atoms_ts.go
// tsMutation) runs git, bun, curl and stryker as plain execs and settles here.
//
// Only what the atom reached was ported: it never set ts.sh's full mode,
// workdir, install root, build command or gate switch, and a repo has no say in
// any of them (MutationScope).

// TSMutationSpecs is the pathspec a pull's diff is taken over.
//
// `:(glob)` ON EVERY DECLARED MODULE: the same string has to satisfy two glob
// dialects — Stryker's --mutate and git's pathspec — and they disagree about
// `**` (git diff -- 'src/**/*.ts' matched 0 files; with the magic it matches,
// nested included). Without it a star declaring the ordinary Stryker glob gets
// a diff that matches nothing and the gate stands down green having mutated
// nothing. An empty declaration is every TypeScript source outside the tests.
func TSMutationSpecs(mods string) []string {
	var specs []string
	for _, m := range strings.Fields(mods) {
		specs = append(specs, ":(glob)"+m)
	}
	if len(specs) > 0 {
		return specs
	}
	return []string{"*.ts", "*.tsx", ":!*.test.ts", ":!*.test.tsx", ":!*.spec.ts", ":!*.spec.tsx", ":!*__tests__/*", ":!node_modules/"}
}

// StrykerRange is one run of changed lines, as Stryker's --mutate names it.
type StrykerRange struct {
	File     string
	From, To int
}

func (r StrykerRange) String() string { return fmt.Sprintf("%s:%d-%d", r.File, r.From, r.To) }

var hunkHeader = regexp.MustCompile(`^@@ -\S+ \+(\d+)(?:,(\d+))? @@`)

// StrykerRanges reads a `git diff --unified=0` into the line ranges each hunk
// ADDS. Stryker has no diff mode, so the ranges are its scope. A hunk that adds
// nothing (+N,0) is a deletion and names no range; a pull whose every hunk is a
// deletion names none, and an empty --mutate is a hard Stryker error.
func StrykerRanges(diff string) []StrykerRange {
	var out []StrykerRange
	file := ""
	for _, line := range strings.Split(diff, "\n") {
		if p, ok := strings.CutPrefix(line, "+++ "); ok {
			file = strings.TrimPrefix(strings.TrimSpace(p), "b/")
			if file == "/dev/null" {
				file = ""
			}
			continue
		}
		m := hunkHeader.FindStringSubmatch(line)
		if file == "" || m == nil {
			continue
		}
		// The digits matched, so these parse.
		from, _ := strconv.Atoi(m[1])
		count := 1
		if m[2] != "" {
			count, _ = strconv.Atoi(m[2])
		}
		if count == 0 {
			continue
		}
		out = append(out, StrykerRange{file, from, from + count - 1})
	}
	return out
}

// strykerConfigs are the names a Stryker config goes by, each also dot-prefixed.
var strykerConfigs = func() map[string]bool {
	names := map[string]bool{}
	for _, stem := range []string{"stryker.config", "stryker.conf"} {
		for _, ext := range []string{"json", "js", "mjs", "cjs"} {
			names[stem+"."+ext] = true
			names["."+stem+"."+ext] = true
		}
	}
	return names
}()

// StrykerConfigDirs is every directory of a tree listing that holds a Stryker
// config, "." for the root.
func StrykerConfigDirs(paths []string) map[string]bool {
	dirs := map[string]bool{}
	for _, p := range paths {
		if strykerConfigs[path.Base(p)] {
			dirs[path.Dir(p)] = true
		}
	}
	return dirs
}

// StrykerPackage is one package a pull's ranges are mutated in: its directory,
// and the ranges relative to it.
type StrykerPackage struct {
	Dir    string
	Ranges []StrykerRange
}

// Mutate is the --mutate argument: the package's ranges, comma-separated.
func (p StrykerPackage) Mutate() string {
	parts := make([]string, len(p.Ranges))
	for i, r := range p.Ranges {
		parts[i] = r.String()
	}
	return strings.Join(parts, ",")
}

// PlanStryker hands each range to the package that owns its file — the nearest
// directory at or above it that carries a Stryker config — in the order the
// diff first names each package, and returns the files no config owns, sorted.
//
// WHY THE LANE FINDS IT. In a bun monorepo Stryker is a devDependency of the
// PACKAGE (theia: packages/aglaia, packages/graph-client), so the root has
// neither a config nor ./node_modules/.bin/stryker, and every TypeScript pull
// read CANNOT RUN with stryker exit 127 (theia #57, 2026-09-13). The config is
// the package's own declaration that it is mutation-tested, so it decides.
func PlanStryker(ranges []StrykerRange, configDirs map[string]bool) ([]StrykerPackage, []string) {
	var plan []StrykerPackage
	at := map[string]int{}
	orphaned := map[string]bool{}
	for _, r := range ranges {
		owner := ""
		for d := path.Dir(r.File); owner == ""; d = path.Dir(d) {
			if configDirs[d] {
				owner = d
			} else if d == "." || d == "/" {
				break
			}
		}
		if owner == "" {
			orphaned[r.File] = true
			continue
		}
		if owner != "." {
			r.File = strings.TrimPrefix(r.File, owner+"/")
		}
		i, seen := at[owner]
		if !seen {
			i = len(plan)
			at[owner] = i
			plan = append(plan, StrykerPackage{Dir: owner})
		}
		plan[i].Ranges = append(plan[i].Ranges, r)
	}
	orphans := make([]string, 0, len(orphaned))
	for f := range orphaned {
		orphans = append(orphans, f)
	}
	sort.Strings(orphans)
	return plan, orphans
}

// StrykerBinCandidates is where a package's stryker bin may be linked, nearest
// first: the package's own node_modules/.bin, then each ancestor's up to the
// root, since a workspace install can link a devDependency's bin at the root.
func StrykerBinCandidates(dir string) []string {
	var out []string
	for d := dir; ; d = path.Dir(d) {
		out = append(out, path.Join(d, "node_modules/.bin/stryker"))
		if d == "." || d == "/" {
			return out
		}
	}
}

// ---- the install -----------------------------------------------------------

// RegistryLookup is what DiagnoseInstall asks of the registry the lockfile was
// resolved against: the versions a package's metadata lists, in the order it
// lists them, and whether a tarball URL serves.
type RegistryLookup interface {
	Versions(registry, name string) []string
	Serves(url string) bool
}

var (
	tarballGet = regexp.MustCompile(`GET (https?://\S+?\.tgz) - (\d{3})`)
	// networkFault is the substrate not answering, in every tool's own words: the
	// Go and node shapes bun, curl and govulncheck print, and the Python shapes
	// pip-audit's stack prints for the same thing (Read timed out, ReadTimeout,
	// TimeoutError), plus the resolver's. One vocabulary, used by DiagnoseInstall
	// and AuditVerdict alike, so a phrase learned in one place is known in both.
	networkFault = regexp.MustCompile(`(?i)connection refused|connection reset|i/o timeout|ETIMEDOUT|ECONNRESET|ENOTFOUND|EAI_AGAIN|502 Bad Gateway|503 Service Unavailable|504 Gateway|too many requests|429 Too Many Requests|toomanyrequests|read timed out|ReadTimeout|TimeoutError|temporary failure in name resolution|network is unreachable|could not connect|dial tcp`)
)

// DiagnoseInstall settles a `bun install --frozen-lockfile` that exited status
// with log as its output.
//
// A tarball fetch that failed is a FINDING about the star's lockfile, and
// running again changes nothing: either the registry never indexed the pinned
// version, or it lists the version and its proxy will not serve the tarball — a
// defect a metadata check passes, so the versions near it that DO serve are
// named for an override. No tarball failure and a network fault in the log is
// could-not-run; anything else is a finding with the log's tail.
func DiagnoseInstall(log string, status int, look RegistryLookup) (int, string) {
	seen := map[string]bool{}
	fetched := false
	for _, m := range tarballGet.FindAllStringSubmatch(log, -1) {
		url, code := m[1], m[2]
		if seen[url] {
			continue
		}
		seen[url] = true
		fetched = true
		if !strings.Contains(url, "/-/") {
			continue
		}
		cut := strings.LastIndex(url, "/-/")
		segs := strings.Split(url[:cut], "/")
		basename := segs[len(segs)-1]
		segs = segs[:len(segs)-1]
		name := basename
		if strings.HasPrefix(segs[len(segs)-1], "@") {
			name = segs[len(segs)-1] + "/" + basename
			segs = segs[:len(segs)-1]
		}
		registry := strings.Join(segs, "/")
		version := strings.TrimSuffix(strings.TrimPrefix(url[cut+3:], basename+"-"), ".tgz")

		listed := look.Versions(registry, name)
		idx := -1
		for i, v := range listed {
			if v == version {
				idx = i
				break
			}
		}
		if idx < 0 {
			return 1, installFinding(fmt.Sprintf("Version not in registry: %s@%s is not indexed by %s. The lockfile pins a version this registry has never proxied — regenerate it against the registry CI uses.",
				name, version, registry))
		}
		var serving []string
		for i := idx - 1; i >= max(0, idx-4) && len(serving) < 2; i-- {
			if look.Serves(fmt.Sprintf("%s/%s/-/%s-%s.tgz", registry, name, basename, listed[i])) {
				serving = append(serving, listed[i])
			}
		}
		advice := "No nearby version served either — check the registry itself before pinning."
		if len(serving) > 0 {
			advice = "Versions that DO serve: " + strings.Join(serving, ", ") + `. Pin one in the ROOT package.json "overrides" block, re-run bun install, and commit the lockfile.`
		}
		return 1, installFinding(fmt.Sprintf("Registry index/tarball mismatch: %s@%s IS listed in %s's metadata but its tarball returns %s. This is a proxy defect, not a missing version — which is why a metadata check passes while the install fails. %s",
			name, version, registry, code, advice))
	}
	if fetched {
		return 1, installFinding("a tarball fetch failed at a URL with no /-/ to read a package from")
	}
	if networkFault.MatchString(log) {
		return 2, fmt.Sprintf("CANNOT RUN - bun install failed on a network fault (rc=%d) — run it again\n%s", status, tail(log, 20))
	}
	return 1, fmt.Sprintf("bun install --frozen-lockfile exited %d and not on a tarball fetch — the install's own output:\n%s", status, tail(log, 20))
}

func installFinding(diagnosis string) string {
	return "bun install --frozen-lockfile failed on a lockfile the CI registry cannot serve — " + diagnosis + "; running again changes nothing"
}

// tail is the last n lines of s.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}

// RegistryVersions reads npm package metadata into the versions it lists, in
// the order the document lists them: the near-miss search walks back from the
// pinned version in that order.
func RegistryVersions(metadata []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(metadata))
	var skip json.RawMessage
	// object reads one JSON object, handing each key to member, which must
	// consume that key's value.
	object := func(member func(key string) error) error {
		if t, err := dec.Token(); err != nil || t != json.Delim('{') {
			return fmt.Errorf("npm metadata is not an object where one belongs (%v, %v)", t, err)
		}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			if err := member(fmt.Sprint(key)); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	}
	var versions []string
	err := object(func(key string) error {
		if key != "versions" {
			return dec.Decode(&skip)
		}
		return object(func(v string) error {
			versions = append(versions, v)
			return dec.Decode(&skip)
		})
	})
	if err != nil {
		return nil, err
	}
	return versions, nil
}

// ---- stryker-js#6210 -------------------------------------------------------

// The vitest runner's test-name join, as 10.0.0 ships it and as Vitest 5 needs
// it, in the two files that carry it.
const (
	VitestRunnerBroken = "10.0.0"
	vitestRunnerOld    = "return nameParts.join(' ').trim();"
	vitestRunnerNew    = "return nameParts.filter(Boolean).join(' > ').trim();"
)

// VitestRunnerFiles are the runner's files that carry the join, relative to its
// package directory.
var VitestRunnerFiles = []string{"dist/src/stryker-setup.js", "dist/src/test-helpers.js"}

// PackageVersion is a package.json's version, "" when it does not read.
func PackageVersion(packageJSON string) string {
	var p struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal([]byte(packageJSON), &p)
	return p.Version
}

// PatchVitestRunner answers the patched contents of VitestRunnerFiles for one
// installed copy of @stryker-mutator/vitest-runner, or nil to leave it as
// shipped.
//
// Vitest 5 matches testNamePattern against the suite chain joined with " > ",
// and vitest-runner 10.0.0 joins with a space, so every covered mutant runs
// ZERO tests and survives. Measured 2026-09-13 on stellar-core-ts src/jwt.ts,
// 39 per-test-covered mutants: vitest 5.0.0, 39 Survived at 0 tests; the join
// changed to " > ", all 39 Killed; vitest 4.1.11 unpatched, all 39 Killed. It is
// fixed in the lane rather than by pinning vitest or patching each repo,
// because a repo has no say in how it is measured.
//
// Only a copy PROVEN broken is patched: runner 10.0.0, a vitest beside it at
// major 5 or later, and the old join exactly once in each file with no new
// join. Anything else — another release, vitest 4, a join that moved, a copy
// already patched — is left alone.
func PatchVitestRunner(runnerVersion, vitestVersion string, files []string) []string {
	major, err := strconv.Atoi(strings.SplitN(vitestVersion, ".", 2)[0])
	if runnerVersion != VitestRunnerBroken || err != nil || major < 5 {
		return nil
	}
	patched := make([]string, len(files))
	for i, src := range files {
		if strings.Count(src, vitestRunnerOld) != 1 || strings.Contains(src, vitestRunnerNew) {
			return nil
		}
		patched[i] = strings.Replace(src, vitestRunnerOld, vitestRunnerNew, 1)
	}
	return patched
}

// VitestRunnerNewJoin is the join a patched copy carries, for the lane to read
// back out of the container it will run stryker in.
const VitestRunnerNewJoin = vitestRunnerNew

// VitestRunnerCopy is one installed copy of the runner as the lane found it:
// the two versions stryker-js#6210 turns on, how many of its files the patch
// rewrote, and how many of those read back carrying the new join.
type VitestRunnerCopy struct {
	Dir      string
	Runner   string
	Vitest   string
	Patched  int
	Verified int
}

// VitestRunnerReport is what the search did, in the words the verdict carries.
// Note is always said; Blocked is the reason the measurement cannot be trusted,
// empty when it can.
type VitestRunnerReport struct {
	Note    string
	Blocked string
}

// ReportVitestRunners is the patcher's account of itself.
//
// IT SAYS WHAT IT DID EVEN WHEN IT DID NOTHING. The patcher used to answer a
// search that matched no copies, and a search that failed outright, by handing
// the container back unchanged and saying neither — so a repo whose runner was
// never patched arrived at the scorer as zero-test survivors with nothing
// anywhere naming the cause (gijmo-ui#28, measured 2026-09-16: the mutation
// lane read CANNOT RUN on "42 survivor(s) completed ZERO tests" and no line of
// its log mentioned the runner at all). A patch nobody can see applied is
// indistinguishable from a patch that never applied.
//
// THE ROOT IS NAMED because the search root is the whole question. The retired
// bash lane searched $MUT_INSTALL_ROOT, defaulting to $MUT_WORKDIR — the
// PACKAGE — while a hoisted runner is a property of the WORKSPACE: from
// gijmo-ui's repo root find matches one copy, from packages/ui none.
func ReportVitestRunners(root string, copies []VitestRunnerCopy) VitestRunnerReport {
	if len(copies) == 0 {
		return VitestRunnerReport{Note: "@stryker-mutator/vitest-runner: no installed copy under " + root +
			" — nothing was patched for stryker-js#6210, so a vitest 5 run measured here attributes no test to any mutant"}
	}
	lines := []string{fmt.Sprintf("@stryker-mutator/vitest-runner: %d copy(s) under %s", len(copies), root)}
	var blocked []string
	for _, c := range copies {
		// An if-chain, not a switch: Go's cover tool starts a case block AFTER
		// the case expression, so gremlins reports a mutant sitting on one as
		// NOT COVERED however well tested it is (the go gate's scorer has to
		// correct exactly that misread, checks.ScoreGoMutation). Measured here
		// 2026-09-16: as a switch these three conditions were 20 killed and 2
		// COVERED-UNRUN; as an if-chain every one of them is measured.
		where := fmt.Sprintf("  %s: runner %s, vitest %s — ", c.Dir, saidVersion(c.Runner), saidVersion(c.Vitest))
		if c.Patched == 0 {
			lines = append(lines, where+"left as shipped (not the release stryker-js#6210 is about, or already patched)")
		} else if c.Verified == c.Patched {
			lines = append(lines, where+fmt.Sprintf("patched for stryker-js#6210, %d file(s), each read back carrying the new join", c.Patched))
		} else {
			lines = append(lines, where+fmt.Sprintf("PATCH DID NOT LAND — %d of %d file(s) read back carrying the new join", c.Verified, c.Patched))
			blocked = append(blocked, c.Dir)
		}
	}
	report := VitestRunnerReport{Note: strings.Join(lines, "\n")}
	if len(blocked) > 0 {
		report.Blocked = "the vitest runner is still as shipped at " + strings.Join(blocked, ",") +
			" — it joins test names with a space where vitest 5 matches them joined with \" > \", so every covered mutant would run zero tests (stryker-js#6210). A score measured through it would be a number about nothing, so it is not measured."
	}
	return report
}

// saidVersion names a version that did not read rather than leaving a hole in
// the sentence: an empty string there reads as a missing word, and the read
// failing is itself the finding.
func saidVersion(v string) string {
	if v == "" {
		return "unreadable"
	}
	return v
}

// ZeroTestDiagnosis names WHY survivors completed no test, from the mutants
// that did it and which of them Stryker called static.
//
// THERE ARE TWO CAUSES AND THIS USED TO NAME ONE. The old sentence read "this
// is a runner that measured nothing, not a test gap — seen with
// @stryker-mutator/vitest-runner 10.0.0 under vitest 5.0.0", which is a
// confident diagnosis of the upstream bug and, on gijmo-ui#28, wrong. It cost
// a window of hunting that bug, two disproved hypotheses about Dagger mounts
// and bun's symlink store, and a ratified exemption for six mutants that were
// never unkillable.
//
// STRYKER'S OWN `static` FLAG TELLS THEM APART, and that is why it is read:
//
//   - STATIC. A static mutant sits in code that runs at LOAD time, and Stryker
//     runs the whole suite against it with NO testNamePattern — so
//     stryker-js#6210, which is a testNamePattern mismatch, cannot reach it.
//     What reaches it is a mutant that breaks module-scope code in a test file:
//     the file fails to COLLECT, no test fails, and the mutant survives having
//     completed none. MEASURED gijmo-ui#28, 2026-09-16: emptying one `fg`
//     left "Test Files 1 failed | 14 passed, Tests 237 passed" — that file's 25
//     tests never ran. Calling the module-scope work in a try and asserting in
//     a test body that it did not throw took the file from 28 killed / 42
//     survived to 70 / 0.
//
//   - NOT STATIC. The mutant had covering tests and the runner did not
//     attribute them: the stryker-js#6210 shape. MEASURED on the same repo's
//     scripts/dtcg.ts: 16 non-static survivors at zero tests as shipped, 0 with
//     the runner patched.
//
// A repo can carry both at once — that one did — so the count is given per
// cause rather than a single verdict.
func ZeroTestDiagnosis(unmeasured, static []string) string {
	lead := fmt.Sprintf("CANNOT RUN - Nothing was measured per mutant: %d survivor(s) completed ZERO tests (first: %s).",
		len(unmeasured), unmeasured[0])
	const collect = "A mutant that breaks code running at MODULE SCOPE in a test file makes the FILE fail to collect: no test fails, so the mutant survives having completed none. Call that work in a try and assert in a test body that it did not throw."
	const runner = "Each had covering tests the runner did not attribute — the stryker-js#6210 shape, seen with @stryker-mutator/vitest-runner 10.0.0 under vitest 5.0.0. Check the lane's own line for whether it patched the runner."
	if len(static) == 0 {
		return lead + " None is a STATIC mutant. " + runner
	}
	if len(static) == len(unmeasured) {
		return lead + " Every one is a STATIC mutant, which Stryker runs the whole suite against with no testNamePattern — so stryker-js#6210 cannot be the cause. " + collect
	}
	return fmt.Sprintf("%s %d of them are STATIC, which Stryker runs the whole suite against with no testNamePattern, so stryker-js#6210 cannot be their cause: %s The other %d are not static. %s",
		lead, len(static), collect, len(unmeasured)-len(static), runner)
}

// ---- the score -------------------------------------------------------------

// StrykerRun is what one package's stryker run left behind. Report and
// Exemptions are empty when the file was not there.
type StrykerRun struct {
	Dir        string
	Status     int
	Log        string
	Report     string
	Exemptions string
}

var (
	zeroInstrumented = regexp.MustCompile(`Instrumented [0-9]+ source file\(s\) with 0 mutant\(s\)`)
	messageCall      = regexp.MustCompile(`(console\.(log|error|warn|info|debug|trace)\s*\(|logger\.\w+\s*\(|new Error\s*\()`)
)

// TSMutationVerdict settles every planned package's run as one verdict: the
// worst of them, survivors summed.
func TSMutationVerdict(runs []StrykerRun) (int, string) {
	multi := len(runs) > 1
	var summary strings.Builder
	missed, zero := 0, 0
	for _, run := range runs {
		where := ""
		if multi {
			where = " in " + run.Dir
		}
		// ZERO MUTANTS INSTRUMENTED IS NOTHING TO MUTATE, NOT A BROKEN RUN. A
		// diff can change lines holding no mutable code (a renamed word in a
		// comment); Stryker instruments 0 mutants, its dry run executes no test,
		// and it throws "No tests were executed" without a report (theia#61,
		// 2026-09-15). Keyed on the instrumenter's own count, so "No tests were
		// executed" with mutants to run is still could-not-run.
		if zeroInstrumented.MatchString(run.Log) {
			zero++
			if multi {
				fmt.Fprintf(&summary, "### %s\n\nnothing to mutate: Stryker instrumented 0 mutants in the changed lines\n\n", run.Dir)
			}
			continue
		}
		if run.Report == "" {
			return 2, fmt.Sprintf("CANNOT RUN - stryker exited %d%s and wrote no reports/mutation/mutation.json — nothing was measured\n%s", run.Status, where, tail(run.Log, 20))
		}
		h, err := ScoreStryker(run.Report, run.Exemptions)
		if err != nil {
			return 2, "CANNOT RUN - the mutation report" + where + " could not be read: " + err.Error()
		}
		if h.State != 0 {
			return h.State, h.Error + where + "\n\n" + h.Summary
		}
		// Stryker exits non-zero for a broken run AND for a threshold break;
		// this lane sets no threshold, so any non-zero is a broken run.
		if run.Status != 0 {
			return 2, fmt.Sprintf("CANNOT RUN - stryker exited %d%s — a broken run, not a survivor report\n%s", run.Status, where, tail(run.Log, 20))
		}
		missed += h.Missed
		if multi {
			fmt.Fprintf(&summary, "### %s\n\n%s\n\n", run.Dir, h.Summary)
		} else {
			summary.WriteString(h.Summary)
		}
	}
	if zero == len(runs) {
		return 0, "the changed lines hold no mutable code — Stryker instrumented 0 mutants, nothing to mutate"
	}
	if missed == 0 {
		return 0, "every viable mutant was caught\n\n" + summary.String()
	}
	return 1, fmt.Sprintf("%d mutant(s) survived or were never covered — see the list below\n\n%s", missed, summary.String())
}

// StrykerScore is one package's report scored honestly: State 0 when it
// measured something (Missed counts what it missed), 1 or 2 with Error when it
// did not.
type StrykerScore struct {
	State   int
	Error   string
	Summary string
	Missed  int
}

type strykerExemption struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	Mutator    string `json:"mutator"`
	Reason     string `json:"reason"`
	RatifiedBy string `json:"ratifiedBy"`
}

type strykerReport struct {
	Config struct {
		TestRunner    *string `json:"testRunner"`
		CommandRunner *struct {
			Command *string `json:"command"`
		} `json:"commandRunner"`
		Mutate           json.RawMessage `json:"mutate"`
		CoverageAnalysis *string         `json:"coverageAnalysis"`
	} `json:"config"`
	TestFiles map[string]struct {
		Tests []json.RawMessage `json:"tests"`
	} `json:"testFiles"`
	Files map[string]struct {
		Source  string `json:"source"`
		Mutants []struct {
			Status         string `json:"status"`
			MutatorName    string `json:"mutatorName"`
			TestsCompleted *int   `json:"testsCompleted"`
			// Static is Stryker's own word for a mutant in code that runs at
			// load time. It is the one field that tells the two causes of a
			// zero-test survivor apart — see ZeroTestDiagnosis.
			Static   *bool `json:"static"`
			Location struct {
				Start struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"location"`
		} `json:"mutants"`
	} `json:"files"`
}

// ScoreStryker scores a Stryker mutation.json the way honest.mjs did, against
// the package's stryker-honest.json exemptions ("" when it has none).
//
// THE HONEST RATIO DISCOUNTS NOISE FROM BOTH SIDES. A survivor on a string
// literal inside a log call or an Error message asserts nothing a test should,
// and a RATIFIED exemption names the ruling that allows it; both leave the
// viable count as well as the missed one. An exemption with no ratifiedBy is
// refused outright (foundry-stocks#5065 §5b).
//
// A SCORE OVER A SUITE THAT NEVER RAN IS NOT A SCORE. A report recording zero
// tests (outside the command runner, which counts its command as one) is a
// finding; a survivor that completed zero tests is a runner that measured
// nothing (vitest-runner 10.0.0 under vitest 5, PatchVitestRunner) and is
// could-not-run.
//
// Files are scored in path order, where honest.mjs took the report's.
func ScoreStryker(report, exemptions string) (StrykerScore, error) {
	var ex struct {
		Exemptions []strykerExemption `json:"exemptions"`
	}
	if exemptions != "" {
		if err := json.Unmarshal([]byte(exemptions), &ex); err != nil {
			return StrykerScore{}, fmt.Errorf("stryker-honest.json did not parse: %w", err)
		}
		var unratified []string
		for _, e := range ex.Exemptions {
			if e.RatifiedBy == "" {
				unratified = append(unratified, fmt.Sprintf("%s:%d %s", e.File, e.Line, e.Mutator))
			}
		}
		if len(unratified) > 0 {
			return StrykerScore{State: 1, Error: fmt.Sprintf("Unratified exemption: stryker-honest.json carries %d exemption(s) with no `ratifiedBy` — %s. An exemption family needs a signoff (foundry-stocks#5065 §5b); refusing to discount them.",
				len(unratified), strings.Join(unratified, ", "))}, nil
		}
	}

	var r strykerReport
	if err := json.Unmarshal([]byte(report), &r); err != nil {
		return StrykerScore{}, fmt.Errorf("mutation.json did not parse: %w", err)
	}
	orUnset := func(s *string, unset string) string {
		if s == nil {
			return unset
		}
		return *s
	}
	runner := orUnset(r.Config.TestRunner, "(unset)")
	command := "(runner-internal)"
	if runner == "command" {
		command = "(unset)"
	}
	if r.Config.CommandRunner != nil {
		command = orUnset(r.Config.CommandRunner.Command, command)
	}
	// Stryker writes the scope it ran as an array; anything else is unset.
	var list []string
	scope := "(unset)"
	if json.Unmarshal(r.Config.Mutate, &list) == nil && list != nil {
		scope = strings.Join(list, " ")
	}
	testsSeen := 0
	for _, f := range r.TestFiles {
		testsSeen += len(f.Tests)
	}

	counts := map[string]int{}
	var real, noise, unmeasured, static []string
	paths := make([]string, 0, len(r.Files))
	for p := range r.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		f := r.Files[p]
		src := strings.Split(f.Source, "\n")
		for _, m := range f.Mutants {
			counts[m.Status]++
			line := m.Location.Start.Line
			if m.Status == "Survived" && m.TestsCompleted != nil && *m.TestsCompleted == 0 {
				where := fmt.Sprintf("%s:%d %s", p, line, m.MutatorName)
				unmeasured = append(unmeasured, where)
				if m.Static != nil && *m.Static {
					static = append(static, where)
				}
			}
			if m.Status != "Survived" && m.Status != "NoCoverage" {
				continue
			}
			reason := ""
			for _, e := range ex.Exemptions {
				if e.File == p && e.Line == line && e.Mutator == m.MutatorName {
					reason = "ratified:" + e.Reason
					break
				}
			}
			if reason == "" && m.Status == "Survived" && m.MutatorName == "StringLiteral" {
				ctx := strings.Join(src[min(max(0, line-4), len(src)):min(max(0, line), len(src))], "\n")
				if messageCall.MatchString(ctx) {
					reason = "unasserted-message-string"
				}
			}
			row := fmt.Sprintf("%s:%d  %-10s %s", p, line, m.Status, m.MutatorName)
			if reason != "" {
				noise = append(noise, row+"   ["+reason+"]")
			} else {
				real = append(real, row)
			}
		}
	}

	killed := counts["Killed"] + counts["Timeout"]
	missedRaw := counts["Survived"] + counts["NoCoverage"]
	inert := counts["CompileError"] + counts["RuntimeError"]
	viableRaw := killed + missedRaw
	missed := missedRaw - len(noise)
	viable := killed + missed
	coverage := orUnset(r.Config.CoverageAnalysis, "?")
	out := []string{
		"### Mutation gate — typescript (diff)", "",
		"| killed | survived | no-coverage | inert | raw | honest |",
		"|---|---|---|---|---|---|",
		fmt.Sprintf("| %d | %d | %d | %d | %s%% of %d | %s%% of %d |", killed, counts["Survived"], counts["NoCoverage"], inert, pct(killed, viableRaw), viableRaw, pct(killed, viable), viable), "",
		"**What ran** — a score is meaningless without the command behind it.", "",
		"| runner | test command | coverage analysis | tests seen | scope |",
		"|---|---|---|---|---|",
		fmt.Sprintf("| %s | `%s` | %s | %d | `%s` |", runner, command, coverage, testsSeen, scope), "",
	}

	if runner != "command" && testsSeen == 0 {
		return StrykerScore{State: 1, Summary: strings.Join(out, "\n"), Missed: 1,
			Error: "Nothing was measured: the report records ZERO tests. A mutation score over a suite that never ran is not a low score, it is not a measurement."}, nil
	}
	if len(unmeasured) > 0 {
		return StrykerScore{State: 2, Summary: strings.Join(out, "\n"),
			Error: ZeroTestDiagnosis(unmeasured, static)}, nil
	}
	if runner == "command" {
		out = append(out,
			"> `tests seen` is **1 by construction** under the command runner: Stryker",
			"> spawns the command and counts it as a single opaque test. The tell that the",
			"> suite ran at all is that mutants were KILLED — a command exiting 0 having run",
			"> nothing would leave every mutant alive.", "")
	}
	if len(noise) > 0 {
		out = append(out, fmt.Sprintf("**Discounted (%d)** — excluded from BOTH sides of the honest ratio, not merely hidden. A ratified entry names the ruling that allows it.", len(noise)), "", "```")
		out = append(out, noise...)
		out = append(out, "```", "")
	}
	if len(real) > 0 {
		out = append(out, "**Survivors** — each is a HYPOTHESIS, not a finding. A mutant that lives may be a real test gap, or it may be equivalent to the original, which is undecidable in general. `NoCoverage` is the sharper signal of the two: no test executes that code at all.", "", "```")
		out = append(out, real...)
		out = append(out, "```")
	}
	return StrykerScore{Summary: strings.Join(out, "\n"), Missed: missed}, nil
}

// pct is killed over viable as a percentage to one decimal, printed the way
// JavaScript prints a number: 100, not 100.0.
func pct(k, v int) string {
	if v == 0 {
		return "0"
	}
	return strconv.FormatFloat(math.Round(float64(k)/float64(v)*1000)/10, 'f', -1, 64)
}
