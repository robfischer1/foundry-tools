package publishpack

import (
	"fmt"
	"os"
	"strings"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
)

// The verdicts, in the Dagger publish lane's sentences ("findings: ..." and
// "could not run: ..." lead them as they lead publish.go's returns).
func cannot(a checks.AtomDef, why string) checks.Verdict {
	return checks.VerdictOf(a, buildlane.CouldNotRun, why)
}

func absent(a checks.AtomDef, why string) checks.Verdict {
	return checks.VerdictOf(a, 0, a.ID+": ABSENT - "+why)
}

// ran is the tool's output when it exited zero, and when it did not the code
// class and the reason: a tool that would not start never ran (what did not
// run is Dagger's subject for it), and one that exited non-zero is classified
// as the Dagger lane classifies it (buildlane.ToolFailed). The tool's own words
// ride after the reason, where Dagger leaves them in its progress log.
func ran(step, what, out string, code int) (string, int, string) {
	switch {
	case code == 0:
		return out, buildlane.Clean, ""
	case code < 0:
		return "", buildlane.CouldNotRun, fmt.Sprintf("could not run: %s never ran: %s", what, out)
	}
	cls, why := buildlane.ToolFailed(step, out)
	return "", cls, why + "\n" + out
}

// pythonRoot says whether the root is a Python project, which is how the
// Dagger lane decides the Python half runs (publish_npm.go npmTree).
func pythonRoot(entries []string) bool {
	has := func(name string) bool {
		for _, e := range entries {
			if e == name {
				return true
			}
		}
		return false
	}
	return has("pyproject.toml") || has("setup.py")
}

func mkdir(a checks.AtomDef, dir string) *checks.Verdict {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		v := cannot(a, "could not run: the release directory could not be made: "+err.Error())
		return &v
	}
	return nil
}

func list(names []string) string { return strings.Join(names, ", ") }
