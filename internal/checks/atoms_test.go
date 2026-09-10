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
