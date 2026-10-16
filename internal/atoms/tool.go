package atoms

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// THE HELPERS THE TOOLCHAIN ATOMS SHARE (the python and ts lanes' atoms, each in
// its own file under atoms/<ns>/<name>/). An atom of those lanes asks the tree
// three things the chains asked the engine (the root's entries, a file's body,
// the population under a glob), runs one tool and reads its exit. The chains
// answered them from a Dagger Directory; these answer them from the checkout the
// atom runs on, with the words the chains used (R20).

// Runner is the program runner this input names, or the real one.
func (in Input) Runner() Exec {
	if in.Exec != nil {
		return in.Exec
	}
	return RunProgram
}

// RootEntries lists the repository root as the chains' Directory.Entries did: a
// directory's name carries a trailing slash.
func RootEntries(root string) ([]string, error) {
	return tree{root: root}.entries(".")
}

// ReadIfPresent reads rel under root when it is a regular file there. A path
// that is a directory, is absent or would not read is not present: the chains
// read a file this way and took any failure of the read as an absence.
func ReadIfPresent(root, rel string) (body string, present bool) {
	fi, err := os.Stat(filepath.Join(root, rel))
	if err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	b, err := os.ReadFile(filepath.Join(root, rel))
	return string(b), err == nil
}

// Matching is the files a Dagger glob matches: `**` crosses directories and
// matches none, every other segment is path.Match's, so `*` stops at a slash.
// The order of files is kept.
func Matching(files []string, glob string) []string {
	want := strings.Split(glob, "/")
	var out []string
	for _, f := range files {
		if globSegments(want, strings.Split(f, "/")) {
			out = append(out, f)
		}
	}
	return out
}

// globSegments matches a path's segments against a pattern's.
func globSegments(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if globSegments(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], segs[0])
	return err == nil && ok && globSegments(pat[1:], segs[1:])
}

// WriteRuleset writes a fleet ruleset where the tool is pointed at it: under
// ATOM_WORK, which the pod gives each atom, or a directory of its own where none
// is named. It answers the file's path. The tool reads this copy and not the
// tree's, which is the point of a fleet ruleset (CONF-7-002).
func WriteRuleset(name, body string) (string, error) {
	dir := os.Getenv("ATOM_WORK")
	if dir == "" {
		d, err := os.MkdirTemp("", "atom-rulesets-")
		if err != nil {
			return "", err
		}
		dir = d
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	return p, os.WriteFile(p, []byte(body), 0o644)
}

// NeverRan is a tool run that did not happen: the program would not start, or
// it ended in the signal range (128 to 191), which the chains' engine answered
// with an error and no exit code. The reason reads as the chains' did: the atom
// never ran, then the exit and the tail of what the tool said.
func NeverRan(a checks.AtomDef, argv []string, code int, out string) checks.Verdict {
	if code < 0 {
		return checks.VerdictOf(a, int(checks.StateCannotRun), "the atom never ran: "+out)
	}
	return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf(
		"the atom never ran: exit code: %d\nfailed: %s\n%s", code, strings.Join(argv, " "), lastN(out, 20)))
}

// ToolRan reports whether an exit is one the tool answered: not a program that
// would not start (-1) and not the signal range the engine refuses to read.
func ToolRan(code int) bool { return code >= 0 && (code < 128 || code > 191) }

// lastN is the last n lines of s.
func lastN(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}

// Probe is the chains' provisioning exec: the tool answers its version, or the
// atom never ran. It answers nil when the tool is there.
func Probe(ctx context.Context, in Input, a checks.AtomDef, name string, args ...string) *checks.Verdict {
	out, code := in.Runner()(ctx, Cmd{Dir: in.Root, Name: name, Args: args, Both: true})
	if code == 0 {
		return nil
	}
	v := NeverRan(a, append([]string{name}, args...), code, out)
	return &v
}

// RunTool runs c and answers what it printed and how it exited. A tool that
// never ran answers its verdict instead, and the caller returns it.
func RunTool(ctx context.Context, in Input, a checks.AtomDef, c Cmd) (string, int, *checks.Verdict) {
	out, code := in.Runner()(ctx, c)
	if !ToolRan(code) {
		v := NeverRan(a, append([]string{c.Name}, c.Args...), code, out)
		return "", 0, &v
	}
	return out, code, nil
}

// SettleTool runs c and settles on its exit, stdout then stderr: the chains'
// verdict().
func SettleTool(ctx context.Context, in Input, a checks.AtomDef, c Cmd) checks.Verdict {
	c.Both = true
	out, code, bad := RunTool(ctx, in, a, c)
	if bad != nil {
		return *bad
	}
	return checks.VerdictOf(a, code, out)
}
