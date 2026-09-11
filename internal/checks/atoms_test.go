package checks

import (
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every atom's script is POSIX shell the lane image's sh will parse. A quoting
// slip in one of the Go string literals here is red in every repo the gate
// touches at once, and the module's own tests are the first place it can be
// caught — not the fleet's next forty pulls.
func TestEveryAtomScriptParsesAsShell(t *testing.T) {
	// sh is not optional: the lane images run these under sh, and a host
	// without one cannot vouch for the scripts at all — that is a failure.
	if _, err := exec.LookPath("sh"); err != nil {
		t.Fatalf("no sh on this host, so the scripts cannot be parsed: %v", err)
	}
	for _, a := range Atoms {
		if strings.TrimSpace(a.Script) == "" {
			t.Errorf("%s has no script", a.ID)
			continue
		}
		cmd := exec.Command("sh", "-n")
		cmd.Stdin = strings.NewReader(a.Script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: sh -n: %v\n%s", a.ID, err, out)
		}
	}
}

// The forge-testkit ports hand the CLI its paths, read over the gate's own
// population, and say ABSENT for a project that never took the dependency.
func TestForgeTestkitAtomsCarryPathsAndGuards(t *testing.T) {
	for id, pattern := range map[string]string{
		"python:forge-testkit-assertion-free": "tests/*.py",
		"python:forge-testkit-fake-placement": "*.py",
		"python:forge-testkit-schema-budget":  "src/*.py",
	} {
		s := AtomByID(id).Script
		for _, want := range []string{"population -- '" + pattern + "'", "xargs -r uv run --extra dev forge-testkit-lint", "not a dependency"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s lacks %q", id, want)
			}
		}
	}
	// pytest's exit 5 (nothing collected) used to fold to ABSENT here; it is a
	// finding now (TestNoTestsIsAFindingNotAnAbsence), and this guards that the
	// exit code is still read rather than flattened into a generic red.
	if s := AtomByID("python:pytest").Script; !strings.Contains(s, `"$rc" -eq 5`) || strings.Contains(s, "python:pytest: ABSENT") {
		t.Errorf("pytest must read exit 5 and answer it as a finding, never ABSENT:\n%s", s)
	}
}

// THE RULESET IS THE FLEET'S, AND NO ATOM READS THE REPOSITORY'S. Rob,
// 2026-09-11: a repo has no say in anything that runs; the fleet decides the
// atoms AND their rulesets. Until then ruff read the repo's pyproject, mypy
// its [tool.mypy], eslint its eslint.config.mjs, staticcheck any
// staticcheck.conf, clippy the manifest's [lints], and the fleet atoms read
// .pre-commit-config.yaml for their population and their arguments — seven
// repos had drifted from the template's ruleset and three carried none. This
// pins the move: the linting atoms point at foundry-stocks ci/lib/rulesets and
// ask for the mount, the population exclude is a constant, and nothing in the
// table names the repository's config files or the hook-args parser.
func TestTheAtomsCarryTheFleetsRulesetsNotTheRepositorys(t *testing.T) {
	for id, want := range map[string][]string{
		"python:ruff-check":  {"--config " + rulesetsDir + "/ruff.toml"},
		"python:ruff-format": {"--config " + rulesetsDir + "/ruff.toml", "--check"},
		"python:mypy":        {"--config-file " + rulesetsDir + "/mypy.ini"},
		"ts:bun-gate":        {"cp " + rulesetsDir + "/eslint.config.mjs ./eslint.config.mjs"},
		"ts:bun-gate-commit": {"cp " + rulesetsDir + "/eslint.config.mjs ./eslint.config.mjs"},
	} {
		a := AtomByID(id)
		if !a.NeedsStocks {
			t.Errorf("%s: reads a fleet ruleset but does not ask for the /stocks mount", id)
		}
		for _, w := range want {
			if !strings.Contains(a.Script, w) {
				t.Errorf("%s: does not carry %q", id, w)
			}
		}
		if !strings.Contains(a.Script, rulesetsDir+"/") || !strings.Contains(a.Script, "CANNOT RUN") {
			t.Errorf("%s: a missing ruleset must be CANNOT RUN, never a pass", id)
		}
	}
	if s := AtomByID("python:mypy").Script; strings.Contains(s, "mypy --strict") {
		t.Error("python:mypy: strict lives in the fleet's mypy.ini, not on the flag — two places is one too many")
	}
	if s := AtomByID("go:staticcheck").Script; !strings.Contains(s, "-checks 'all,") {
		t.Error("go:staticcheck: the check set must be named on the command line, so a staticcheck.conf in the tree changes nothing")
	}
	if s := AtomByID("rust:cargo-clippy").Script; !strings.Contains(s, "-- -W clippy::all -D warnings") {
		t.Error("rust:cargo-clippy: the lint set must be named after `--`, so the manifest's [lints] table changes nothing")
	}
	if !strings.Contains(gatePopulation, "EXCL='"+gateExclude+"'") || strings.Contains(gatePopulation, "sed") {
		t.Errorf("the gate population's exclude must be the fleet constant, never read from a file:\n%s", gatePopulation)
	}
	for _, a := range Atoms {
		for _, forbidden := range []string{".pre-commit-config.yaml", "hookmeta", "hookpopulation", "staticcheck.conf"} {
			if strings.Contains(a.Script, forbidden) {
				t.Errorf("%s: reads %q — a repo-authored surface deciding what the gate does", a.ID, forbidden)
			}
		}
	}
	// detect-secrets: the baseline is the one repo-side file that survives this
	// pass (its fleet-side home is a ledger of its own); the population's
	// fixture excludes are the fleet's union, not a hook's.
	if s := AtomByID("fleet:detect-secrets").Script; !strings.Contains(s, "--baseline .secrets.baseline") || !strings.Contains(s, `(^|/)testdata/|^tests/fixtures/`) {
		t.Errorf("fleet:detect-secrets: baseline and fixture excludes must be the fleet's:\n%s", s)
	}
	if s := AtomByID("fleet:check-yaml").Script; !strings.Contains(s, "check-yaml --allow-multiple-documents --unsafe $files") {
		t.Errorf("fleet:check-yaml: the argument is the fleet's, unconditionally:\n%s", s)
	}
	if s := AtomByID("fleet:check-added-large-files").Script; !strings.Contains(s, "maxkb=2048\nlimit=") {
		t.Errorf("fleet:check-added-large-files: the threshold is the fleet's, unconditionally:\n%s", s)
	}
}

// announcedAbsence is the shape VerdictOf reads off an atom's stdout to tell
// "there was nothing to check" from "I checked and it was clean".
var announcedAbsence = regexp.MustCompile(`([A-Za-z0-9:_\- ]+): ABSENT`)

// EVERY ABSENCE AN ATOM ANNOUNCES MUST CARRY THAT ATOM'S OWN ID, because
// AnnouncedAbsence matches on the id as a prefix and reads anything else as a
// pass.
//
// MEASURED 2026-09-10, and it was live in three atoms: the forge-testkit ports
// announced `forge-testkit assertion-free: ABSENT`, while the id is
// `python:forge-testkit-assertion-free`. No prefix, no match — so on every
// repository that never took the forge-testkit dependency, which is most of the
// fleet, three verdicts read `pass` over a scan that had not happened. That is
// the exact conflation VerdictOf was written to prevent, and its own comment
// names the run where 28 of 86 verdicts read `pass` with `absent=0` while
// nothing had been examined.
//
// A prose rule ("write it `<id>: ABSENT - why`") is what was already in place;
// this is the same rule as an assertion, which is the difference between a
// convention and a fact about the code.
func TestEveryAnnouncedAbsenceCarriesItsOwnAtomID(t *testing.T) {
	for _, a := range Atoms {
		for _, m := range announcedAbsence.FindAllStringSubmatch(a.Script, -1) {
			if m[1] != a.ID {
				t.Errorf("atom %q announces its absence as %q — AnnouncedAbsence matches on the id, so this renders as a PASS over a scan that did not happen", a.ID, m[1])
			}
			if _, ok := AnnouncedAbsence(a.ID, m[1]+": ABSENT - x"); !ok {
				t.Errorf("atom %q announces an absence AnnouncedAbsence does not recognise", a.ID)
			}
		}
	}
}

// The compose: ports carry the surface guard, the workflow's own regexes, and
// the three-valued reading of grep that both validate.yml bodies had to learn.
func TestComposeAtomsGuardTheirSurfaceAndReadGrepsExitCode(t *testing.T) {
	for _, id := range []string{"compose:config", "compose:no-tracked-secrets", "compose:third-party-pins"} {
		s := AtomByID(id).Script
		for _, want := range []string{
			// The surface: tracked files, from a git the atom made readable.
			"git ls-files > /tmp/tracked-files",
			`(^|/)(docker-)?compose\.ya?ml$`,
			id + ": ABSENT",
			// rc >= 2 is a refusal, never an empty answer.
			`-gt 1 ]`,
			"CANNOT RUN",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("%s lacks %q", id, want)
			}
		}
	}
	// The parse is the workflow's, --no-interpolate included: these specs use
	// ${VAR:?message} to make a missing variable a DEPLOY-time error, and
	// interpolating would fail every file for the wrong reason.
	if s := AtomByID("compose:config").Script; !strings.Contains(s, "config --no-interpolate --quiet") {
		t.Errorf("compose:config does not parse with --no-interpolate:\n%s", s)
	}
	// The credential scan reads the INDEX, not the gate population: a .env a
	// repo's pre-commit exclude hides is still a tracked .env.
	if s := AtomByID("compose:no-tracked-secrets").Script; strings.Contains(s, "hookpopulation") || strings.Contains(s, "population(") {
		t.Errorf("compose:no-tracked-secrets honours a pre-commit exclude; a credential does not stop being one because a config said not to look:\n%s", s)
	}
	// The ratchet is a COUNT gate. A list gate reopens; a count does not.
	if s := AtomByID("compose:third-party-pins").Script; !strings.Contains(s, `image:.*\${PIN_`) || !strings.Contains(s, "wc -l") {
		t.Errorf("compose:third-party-pins is not the counted ${PIN_} ratchet:\n%s", s)
	}
}

// The dies: ports are conditioned on the tree's SHAPE and pin their engine.
func TestDiesAtomsGuardTheirShapeAndPinOpa(t *testing.T) {
	opaAtoms := map[string]bool{
		"dies:opa-test": true, "dies:admission-dogfood": true,
		"dies:data-keys": true, "dies:canary-visibility": true,
	}
	for _, id := range []string{
		"dies:opa-test", "dies:admission-dogfood", "dies:data-keys",
		"dies:canary-visibility", "dies:contracts", "dies:schema",
	} {
		s := AtomByID(id).Script
		for _, want := range []string{"policy/.manifest", "fleet/stars", id + ": ABSENT"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s lacks its shape guard %q", id, want)
			}
		}
		if !opaAtoms[id] {
			continue
		}
		// The pin is the question, not a detail: rego semantics are a
		// property of the binary, so a suite graded by another major answers
		// a different question.
		if !strings.Contains(s, `"Version: ${OPA_VERSION}"`) || !strings.Contains(s, "$OPA_MIRROR") {
			t.Errorf("%s does not provision opa at the pinned version from the mirror:\n%s", id, s)
		}
	}
	// opa test exits 0 over a policy tree with no assertion in it, which
	// renders as a clean suite and is not one.
	if s := AtomByID("dies:opa-test").Script; !strings.Contains(s, "REFUSING a zero-test run") {
		t.Errorf("dies:opa-test does not refuse a zero-test run:\n%s", s)
	}
	// Both artifact atoms interrogate the BUILT bundle, because a source tree
	// that passes every assertion can still build one that admits everything.
	for _, id := range []string{"dies:data-keys", "dies:canary-visibility"} {
		s := AtomByID(id).Script
		if !strings.Contains(s, "opa build -b policy/") && !strings.Contains(s, `"$OPA" build -b policy/`) {
			t.Errorf("%s grades the source tree rather than the built bundle:\n%s", id, s)
		}
		if !strings.Contains(s, "/dies-data.json") {
			t.Errorf("%s does not read the bundle's own data.json:\n%s", id, s)
		}
	}
	// The canary is READ OFF THE ROSTER. Naming one goes stale the day its
	// verb is retired, and it did — twice.
	if s := AtomByID("dies:canary-visibility").Script; !strings.Contains(s, `get("chaos")`) {
		t.Errorf("dies:canary-visibility names a canary instead of reading one off the bundle:\n%s", s)
	}
	// The gate must prove it DETECTS before the live check is worth anything:
	// seven fixtures that must fail, three controls that must pass.
	s := AtomByID("dies:contracts").Script
	for _, c := range []string{
		"lagging", "undeclared", "bad_pending", "unreadable",
		"expired_pending", "undated_pending", "old_shape_pending",
		"agreeing", "holding_pending", "unmeasurable_pending",
	} {
		if !strings.Contains(s, c) {
			t.Errorf("dies:contracts does not exercise fixture %q", c)
		}
	}
	if !strings.Contains(s, "the door's raw API is unreachable") {
		t.Errorf("dies:contracts reads an unreachable door as divergence rather than as a cannot-run:\n%s", s)
	}
}

// THE GO TEST ATOM MOUNTS THE FLEET'S RECORD TREE, and the reason is not
// visible from the go lane — which is exactly why this test exists.
//
// hephaestus's internal/slag goldens grade every record committed in
// foundry-dies. A gate lane checks out one repository, so before the mount
// they resolved nothing and SKIPPED, and a skipped test is `ok` to `go test`
// and a success to the gate: three whole-fleet gates reported green having
// examined nothing (#2453, and #8118 for the same defect one level up).
//
// A future reader sees an unexplained field on a plain `go test -race` row and
// drops it as dead weight. Dropping it does not break a build, does not red a
// gate, and silently returns those goldens to reporting success without
// running — so the guard has to be a test rather than a comment.
func TestTheGoTestAtomCarriesTheFleetRecordTree(t *testing.T) {
	if !AtomByID("go:test-race").NeedsDies {
		t.Error("go:test-race no longer asks for foundry-dies. hephaestus's " +
			"internal/slag goldens are its reader: without the mount they " +
			"resolve nothing, SKIP, and report success — which is the whole " +
			"of #2453 and #8118. Restore NeedsDies, or move the goldens.")
	}
	// The mount is a fetch on every run of every atom that asks for it, so the
	// ask stays deliberate. Widening it is a decision, not a default.
	//
	// WHY TWO. go:mutation runs the repo's `go test` too (gremlins gathers
	// coverage with it), and hephaestus's armed goldens refuse in any
	// container that runs `go test` under CI=true without /dies — measured
	// 2026-09-11 on hephaestus #53, the mutation lane could-not-run on every
	// hephaestus pull. The rule is: an atom that runs `go test` needs the
	// tree the armed goldens read. Those are the two.
	var asked []string
	for _, a := range Atoms {
		if a.NeedsDies {
			asked = append(asked, a.ID)
		}
	}
	sort.Strings(asked)
	if strings.Join(asked, ",") != "go:mutation,go:test-race" {
		t.Errorf("NeedsDies is declared by %v; go:test-race and go:mutation are expected — "+
			"the two atoms that run a repo's `go test`. Adding one is fine — say why "+
			"here, because each one is another clone of foundry-dies on every gate "+
			"run in the fleet.", asked)
	}
}

// The mutation stage is four atoms, one per lane, and it is asked for BY NAME:
// the default vector (a pull's gate) must never carry one, and neither may the
// sweep. Each runs the canonical script at its one home in diff mode against
// the base the door names, and the three that need a declaration read it off
// the answers file and say ABSENT when there is none.
func TestTheMutationStageIsFourLanesAskedForByName(t *testing.T) {
	got := MutationAtoms()
	want := map[string]Lane{"go:mutation": LaneGo, "python:mutation": LanePython, "rust:mutation": LaneRust, "ts:mutation": LaneTS}
	if len(got) != len(want) {
		t.Fatalf("mutation stage has %d atoms, want %d", len(got), len(want))
	}
	for _, a := range got {
		lane, ok := want[a.ID]
		if !ok || a.Lane != lane {
			t.Errorf("%s: lane %q, want a mutation atom per lane", a.ID, a.Lane)
		}
		if !a.NeedsStocks {
			t.Errorf("%s: must mount foundry-stocks — the script lives there and nowhere else", a.ID)
		}
		script := "/stocks/ci/lib/mutation/" + string(a.Lane) + ".sh"
		for _, need := range []string{script, "MUT_MODE=diff", `MUT_BASE="${GATE_BASE:-}"`, "guard bash --version", "/tmp/mutation/verdict"} {
			if !strings.Contains(a.Script, need) {
				t.Errorf("%s: script lacks %q", a.ID, need)
			}
		}
		// A repo has no say: no env file, no MUT_GATE, nothing sourced from the
		// tree but critical_modules (Rob, 2026-09-11).
		for _, forbidden := range []string{"mutation.env", "MUT_GATE", "MUT_SETUP", "MUT_WORKDIR"} {
			if strings.Contains(a.Script, forbidden) {
				t.Errorf("%s: script reads %q — a repo-level dial on a fleet gate", a.ID, forbidden)
			}
		}
		if a.Lane == LaneGo && !strings.Contains(a.Script, `dagger\.gen\.go`) {
			t.Errorf("%s: generated Go must be excluded by default", a.ID)
		}
		// AN EMPTY MODULE LIST IS NOT AN OPT-OUT. Rob, 2026-09-11: nothing
		// with tests goes un-mutation-tested. The three that read a
		// declaration still scope to it when there is one; with none, the
		// script mutates the whole diff, and the atom must never say ABSENT.
		if a.Lane != LaneGo && !strings.Contains(a.Script, "critical_modules") {
			t.Errorf("%s: must still read critical_modules to scope a declared list", a.ID)
		}
		if strings.Contains(a.Script, ": ABSENT") {
			t.Errorf("%s: says ABSENT — a repo cannot opt out of the mutation gate by declaring nothing", a.ID)
		}
	}
	for _, a := range PullPathAtoms() {
		if a.Stage == StageMutation {
			t.Errorf("%s: a mutation atom in the default vector — the gate would run it", a.ID)
		}
	}
	for _, a := range SweepAtoms() {
		if a.Stage == StageMutation {
			t.Errorf("%s: a mutation atom in the sweep", a.ID)
		}
	}
}

// NO TESTS IS A FINDING, NOT AN ABSENCE. Rob, 2026-09-11: "absent tests red
// the PR and refuse the publish. We don't build anything without tests."
// Each language's test atom used to read green on a tree with nothing to
// run — go test prints "[no test files]" and exits 0, cargo test runs 0
// tests and exits 0, pytest's exit 5 was mapped to ABSENT, and a repo's gate
// script may never reach bun test. Each now counts before it runs and exits
// 1 with a line that names the rule.
func TestNoTestsIsAFindingNotAnAbsence(t *testing.T) {
	for id, counter := range map[string]string{
		"go:test-race":    `go list -f '{{len .TestGoFiles}}{{len .XTestGoFiles}}'`,
		"python:pytest":   `population -- 'test_*.py' '*_test.py'`,
		"rust:cargo-test": `-- --list`,
		"ts:bun-gate":     `-name '*.test.ts'`,
	} {
		s := AtomByID(id).Script
		if strings.Contains(s, ": ABSENT") {
			t.Errorf("%s: answers ABSENT — a tree with no tests is red, never green", id)
		}
		if !strings.Contains(s, counter) {
			t.Errorf("%s: does not count its tests with %q before running them", id, counter)
		}
		if !strings.Contains(s, "nothing is built without tests") || !strings.Contains(s, id+": FINDINGS - no test") {
			t.Errorf("%s: a tree with no tests must exit 1 under its own id and say why", id)
		}
	}
	// pytest's "no tests ran" is exit 5, and it must be a finding, not a pass.
	if s := AtomByID("python:pytest").Script; !strings.Contains(s, `if [ "$rc" -eq 5 ]; then echo "python:pytest: FINDINGS`) {
		t.Error("python:pytest: exit 5 (no tests collected) is not a finding")
	}
}
