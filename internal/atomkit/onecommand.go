package atomkit

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// The vocabulary of R33 / R46 (modules.md IF-050 rules 3 to 6): what makes a
// command atom's argv more than one command.
var (
	shells       = map[string]bool{"sh": true, "bash": true, "dash": true, "zsh": true, "ksh": true, "ash": true, "busybox": true}
	interpreters = map[string]bool{"python": true, "python3": true, "node": true, "bun": true, "deno": true, "perl": true, "ruby": true, "pwsh": true}
	launchers    = map[string]bool{"env": true, "uv": true, "uvx": true, "bunx": true, "npx": true}
	inlineFlags  = []string{"-c", "-lc", "-ec", "-xc", "-e", "-E", "-p", "--eval", "--print", "-Command"}
	chainTokens  = []string{"&&", "||", ";", "|", "&"}
)

// OneCommand answers the breaches of "a manifest runs exactly one command"
// (IF-050 rules 3 to 6) as texts with no path prefix and no R46 ending, in
// rule order. A shell as the command (rule 3.1) is worded "argv runs <name>
// (R33)". ParseManifest and foundry-tools' fleet:manifest-one-command call the
// same code, so the run-time refusal and the CI check cannot disagree.
func OneCommand(argv []string, env map[string]string) []string {
	var out []string
	inline := inlineScript(argv)
	if len(argv) > 0 && shells[path.Base(argv[0])] && inline == "" {
		out = append(out, fmt.Sprintf("argv runs %s (R33)", path.Base(argv[0])))
	}
	return append(out, scriptBreaches(argv, env)...)
}

// scriptBreaches is rules 3.2 and 4 to 6: an inline script, a multi-line
// argument or env value, a heredoc marker, a chaining token.
func scriptBreaches(argv []string, env map[string]string) []string {
	var out []string
	if msg := inlineScript(argv); msg != "" {
		out = append(out, msg)
	}
	for i, a := range argv {
		if strings.ContainsAny(a, "\n\r") {
			out = append(out, fmt.Sprintf("argv[%d] is a multi-line argument", i))
		}
	}
	for _, k := range sortedKeys(env) {
		if strings.ContainsAny(env[k], "\n\r") {
			out = append(out, fmt.Sprintf("env %s is a multi-line argument", k))
		}
	}
	for i, a := range argv {
		if strings.Contains(a, "<<") {
			out = append(out, fmt.Sprintf("argv[%d] carries a heredoc marker", i))
		}
	}
	for i, a := range argv {
		if slices.Contains(chainTokens, a) {
			out = append(out, fmt.Sprintf("argv[%d] %q chains a second command", i, a))
		}
	}
	return out
}

// inlineScript is rule 3.2: the first element, past any launcher, that is a
// shell or an interpreter, followed by an inline-script flag. It answers the
// breach text, or "" when there is none.
func inlineScript(argv []string) string {
	for i := 0; i < len(argv); i++ {
		base := path.Base(argv[i])
		if launchers[base] {
			// a launcher is skipped with its sub-verb and its option flags
			if base == "uv" && i+1 < len(argv) && argv[i+1] == "run" {
				i++
			}
			for i+1 < len(argv) && strings.HasPrefix(argv[i+1], "-") {
				i++
			}
			continue
		}
		if !shells[base] && !interpreters[base] {
			continue
		}
		for _, a := range argv[i+1:] {
			if slices.Contains(inlineFlags, a) {
				return fmt.Sprintf("argv runs %s with an inline script (%s)", base, a)
			}
		}
		return ""
	}
	return ""
}
