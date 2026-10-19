package atomkit

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// srcDir is where a run's tree is, as the commands see it.
const srcDir = "/src"

// moduleFiles maps a toolchain to the file that makes a directory one of its
// modules (lld.md FN-054).
var moduleFiles = map[string]string{
	"go":        "go.mod",
	"rust":      "Cargo.toml",
	"rust-wasm": "Cargo.toml",
	"python":    "pyproject.toml",
	"bun":       "package.json",
	"node":      "package.json",
}

// ModuleFile is the module manifest of a toolchain, "" for one with none.
func ModuleFile(toolchain string) string { return moduleFiles[toolchain] }

var workspaceRE = regexp.MustCompile(`(?m)^\s*\[workspace\]`)

// ModuleRoots are the directories of the tree that hold a module of
// toolchain, as paths under /src, /src first (lld.md FN-054). A Cargo crate
// inside a workspace is not a root of its own: the workspace builds it.
// facts.Root is where the tree is on this side, for reading Cargo.toml.
func ModuleRoots(facts Tree, toolchain string) []string {
	manifest := ModuleFile(toolchain)
	roots := []string{}
	if manifest == "" {
		return roots
	}
	for _, f := range facts.Files {
		if path.Base(f) == manifest {
			roots = append(roots, path.Join(srcDir, path.Dir(f)))
		}
	}
	sort.Strings(roots)
	if manifest != "Cargo.toml" {
		return roots
	}
	var workspaces, kept []string
	for _, r := range roots {
		if inside(r, workspaces) {
			continue
		}
		kept = append(kept, r)
		if isWorkspace(filepath.Join(facts.Root, strings.TrimPrefix(r, srcDir), manifest)) {
			workspaces = append(workspaces, r)
		}
	}
	return kept
}

// bareWorkspaceMembers are the member directories (under /src) a script runner
// runs in when the tree's root package.json is a bare workspace root
// (checks.TSBareWorkspaceMembers): a private workspace declaration with no
// scripts, which has no `test` or `format` for `bun run` to find. Only a
// `bun run <script>` of the bun and node toolchains is redirected; `bun audit`
// and every other command reads the root lockfile and stays at the root, and
// every tree that is not bare answers nil, so nothing else changes.
func bareWorkspaceMembers(facts Tree, toolchain string, argv []string) []string {
	if (toolchain != "bun" && toolchain != "node") || len(argv) < 2 || argv[0] != "bun" || argv[1] != "run" {
		return nil
	}
	manifest, err := os.ReadFile(filepath.Join(facts.Root, "package.json"))
	if err != nil {
		return nil
	}
	members, _ := checks.TSBareWorkspaceMembers(string(manifest), facts.Files)
	dirs := make([]string, len(members))
	for i, m := range members {
		dirs[i] = path.Join(srcDir, m)
	}
	return dirs
}

// inside is true when dir lies under one of the roots.
func inside(dir string, roots []string) bool {
	for _, r := range roots {
		if strings.HasPrefix(dir, r+"/") {
			return true
		}
	}
	return false
}

func isWorkspace(cargoToml string) bool {
	b, err := os.ReadFile(cargoToml)
	return err == nil && workspaceRE.Match(b)
}

// ExecResult is one finished command. NotRun is true when it never started:
// Code is then 127 (not on PATH) or 126 (would not start) and Stderr says why.
type ExecResult struct {
	Code           int
	Stdout, Stderr string
	NotRun         bool
}

// ExecFunc runs argv, with no shell, in dir (a path under /src) and answers
// what it did. It is the seam between the command runner and whatever runs
// the process: an os/exec call in a pod, a WithExec in a Dagger container.
type ExecFunc func(ctx context.Context, argv []string, dir string) ExecResult

// Command is one command atom to run (lld.md FN-053): its manifest, the
// tree facts, and the toolchain's preflight.
type Command struct {
	ID, Stage, Lane string
	Manifest        Manifest
	// Args are the caller's arguments, composed into argv when the manifest says args = "append" (Manifest.Compose).
	Args []string
	// Preflight is the toolchain's preflight argv from tools.toml; nil is none.
	Preflight []string
	Facts     Tree
	Exec      ExecFunc
}

// dirResult is one directory's run.
type dirResult struct {
	dir            string
	state, code    int
	stdout, stderr string
}

// RunCommand runs the command atom c: its argv, once per module directory
// when it is per_module, each behind the toolchain's preflight, judged by its
// judge (lld.md FN-053). ctx is the atom's deadline.
func RunCommand(ctx context.Context, c Command) checks.Verdict {
	m := c.Manifest
	dirs := []string{srcDir}
	if m.PerModule {
		if dirs = ModuleRoots(c.Facts, m.Toolchain); len(dirs) == 0 {
			return c.verdict(checks.StatePass, "absent", "no "+moduleFileName(m.Toolchain)+" in the tree")
		}
	}
	argv := m.Compose(c.Args)
	if members := bareWorkspaceMembers(c.Facts, m.Toolchain, argv); len(members) > 0 && !m.PerModule {
		dirs = members
	}
	results := make([]dirResult, 0, len(dirs))
	for _, d := range dirs {
		if len(c.Preflight) > 0 && m.RunsPreflight() {
			if out := c.Exec(ctx, c.Preflight, d); out.Code != 0 {
				return c.cannotRun(fmt.Sprintf("%s: CANNOT RUN - preflight `%s` exited %d: %s",
					c.ID, strings.Join(c.Preflight, " "), out.Code, lastLine(out.Stderr)))
			}
		}
		out := c.Exec(ctx, argv, d)
		if out.NotRun {
			return c.cannotRun(fmt.Sprintf("exit %d: %s", out.Code, out.Stderr))
		}
		results = append(results, dirResult{
			dir: d, code: out.Code, state: judge(m.Judge, out.Code, out.Stdout != ""),
			stdout: out.Stdout, stderr: out.Stderr,
		})
	}
	return c.fold(results)
}

func moduleFileName(toolchain string) string {
	if f := ModuleFile(toolchain); f != "" {
		return f
	}
	return "module manifest"
}

func lastLine(s string) string {
	ls := strings.Split(strings.TrimRight(s, " \r\n\t"), "\n")
	return strings.TrimSpace(ls[len(ls)-1])
}

// judge turns an exit into a state: exit takes the code as checks.StateFor
// does; output reads a clean exit with something on stdout as findings.
func judge(how string, code int, hasStdout bool) int {
	if how == "output" {
		switch {
		case code != 0:
			return int(checks.StateCannotRun)
		case hasStdout:
			return int(checks.StateFindings)
		}
		return int(checks.StatePass)
	}
	return int(checks.StateFor(code))
}

// verdict is a verdict with no output, carrying the command's identity.
func (c Command) verdict(s checks.State, result, reason string) checks.Verdict {
	return checks.Verdict{
		Atom: c.ID, Stage: c.Stage, Lane: c.Lane,
		State: int(s), Result: result, Reason: oneLine(reason), Logs: []string{},
	}
}

// cannotRun is the could-not-run verdict for a command that did not run.
func (c Command) cannotRun(reason string) checks.Verdict {
	return c.verdict(checks.StateCannotRun, checks.StateCannotRun.String(), reason)
}

// fold folds the per-directory runs into the atom's verdict: the worst state
// and the combined output as logs (lld.md FN-053 step 4).
func (c Command) fold(results []dirResult) checks.Verdict {
	worst := results[0]
	var out strings.Builder
	for _, res := range results {
		if res.state > worst.state {
			worst = res
		}
		out.WriteString(lines(res.stdout, prefix(len(results), res.dir)))
		out.WriteString(lines(res.stderr, prefix(len(results), res.dir)))
	}
	v := c.verdict(checks.State(worst.state), checks.State(worst.state).String(), "")
	v.Logs, v.Truncated, v.OriginalBytes = checks.CaptureLogs(out.String())
	switch checks.State(worst.state) {
	case checks.StatePass:
		v.Reason = c.ID + ": PASS"
	case checks.StateFindings:
		v.Reason = c.ID + ": FINDINGS (exit 1)"
	default:
		v.Reason = fmt.Sprintf("%s: CANNOT RUN (exit %d) — a check that could not run is not a check that passed.", c.ID, worst.code)
	}
	return v
}

// prefix is the label each log line carries when more than one directory ran:
// the directory under /src, "." for /src itself.
func prefix(dirs int, dir string) string {
	if dirs == 1 {
		return ""
	}
	r := strings.TrimPrefix(strings.TrimPrefix(dir, srcDir), "/")
	if r == "" {
		r = "."
	}
	return r + ": "
}

// lines is text with every line prefixed and the last one terminated.
func lines(text, pre string) string {
	if text == "" {
		return ""
	}
	text = strings.TrimSuffix(text, "\n")
	if pre != "" {
		text = pre + strings.ReplaceAll(text, "\n", "\n"+pre)
	}
	return text + "\n"
}
