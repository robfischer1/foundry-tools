package atoms

import (
	"os"

	"dagger/foundry-tools/internal/checks"
)

// WorkDir is where an atom writes what it needs for a run: ATOM_WORK when the
// executor names one, the system's temporary directory otherwise.
func WorkDir() string {
	if d := os.Getenv("ATOM_WORK"); d != "" {
		return d
	}
	return os.TempDir()
}

// AbsentRuleset answers for a tree with no rules/sast (checks.AbsentRuleset),
// reading the copier answers file that says which template stamped it.
func (t tree) AbsentRuleset(a checks.AtomDef) checks.Verdict {
	answers, _ := t.read(checks.CopierAnswersFile)
	return checks.AbsentRuleset(a, answers)
}
