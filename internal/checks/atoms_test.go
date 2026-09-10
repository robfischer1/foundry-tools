package checks

import (
	"os/exec"
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
