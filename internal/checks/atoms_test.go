package checks

import (
	"os/exec"
	"regexp"
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
		for _, want := range []string{"hookpopulation forge-testkit-", "'" + pattern + "'", "xargs -r uv run --extra dev forge-testkit-lint", "not a dependency"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s lacks %q", id, want)
			}
		}
	}
	if s := AtomByID("python:pytest").Script; !strings.Contains(s, `"$rc" -eq 5`) || !strings.Contains(s, "ABSENT") {
		t.Errorf("pytest does not fold an empty collection to ABSENT:\n%s", s)
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
