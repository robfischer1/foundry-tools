package atoms

import (
	"context"
	"regexp"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// noInterpreter is uv's answer when the tree asks for a python the runner does
// not carry and downloads are off (UV_PYTHON_DOWNLOADS=never, on every runner):
// "No interpreter found for Python 3.15 in search path". The request is what
// follows "for", up to " in ".
var noInterpreter = regexp.MustCompile(`No interpreter found for (.+?) in `)

// interpreterRefused is the verdict of a uv run that ended because the tree
// asked for an interpreter the runner does not carry, or nil when the run was
// anything else. A runner never fetches an interpreter, so this is the runner's
// limit and the tree's request, not a finding in the code: the atom never ran,
// and the sentence names the version asked for and the one provided.
func interpreterRefused(ctx context.Context, in Input, a checks.AtomDef, c Cmd, out string, code int) *checks.Verdict {
	if c.Name != "uv" || code == 0 {
		return nil
	}
	m := noInterpreter.FindStringSubmatch(out)
	if m == nil {
		return nil
	}
	have := "an unreadable python"
	if v, vcode := in.Runner()(ctx, Cmd{Dir: in.Root, Name: "python3", Args: []string{"--version"}, Both: true}); vcode == 0 && strings.TrimSpace(v) != "" {
		have = strings.TrimSpace(v)
	}
	v := checks.VerdictOf(a, int(checks.StateCannotRun),
		"the atom never ran: the tree asks for "+m[1]+" but this runner provides "+have+" and never fetches an interpreter")
	return &v
}
