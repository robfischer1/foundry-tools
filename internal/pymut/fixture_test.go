package pymut

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// THE FIXTURE: a pull that added a line to src/x.py in a project with a manifest,
// with every program the atom starts answered from a script (first rule wins).
// The testkit's subcommands also leave the files the atom reads back.

const (
	sinceSha = "since0"
	pyDiff   = "diff --git a/src/x.py b/src/x.py\n+++ b/src/x.py\n@@ -0,0 +1 @@\n+x = 1"
)

type rule struct {
	needle string
	out    string
	code   int
	stderr string
}

type pull struct {
	mu    sync.Mutex
	t     *testing.T
	in    atomkit.In
	rules []rule
	runs  []string
	cmds  []atoms.Cmd
	work  string
	// scopeOut is what scope writes to GITHUB_OUTPUT.
	scopeOut string
	// failSyncFrom, when set, fails the nth uv sync and every one after it.
	failSyncFrom, syncs int
}

func putFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newPull(t *testing.T, answers string) *pull {
	t.Helper()
	t.Setenv("ATOM_ORIGIN", "")
	t.Setenv(checks.CPUsEnv, "12")
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	root := t.TempDir()
	putFile(t, root, "pyproject.toml", "[project]\nname='x'\n")
	putFile(t, root, "src/x.py", "x = 1\n")
	putFile(t, root, ".venv/lib/marker", "the consumer's own environment\n")
	putFile(t, root, ".git/HEAD", "ref: refs/heads/main\n")
	if answers != "" {
		putFile(t, root, ".copier-answers.yml", answers)
	}
	p := &pull{t: t, work: work}
	p.in = atomkit.In{Root: root, Base: "abc123", Exec: p.exec}
	p.rules = []rule{
		{"merge-base", sinceSha + "\n", 0, ""},
		{"--name-only --diff-filter=AM", "src/x.py\n", 0, ""},
		{"diff -M --unified=0", pyDiff, 0, ""},
		{"forge-testkit-mutation plan", "pending=5\nworkers=2\n", 0, ""},
	}
	return p
}

func (p *pull) first(r rule) { p.rules = append([]rule{r}, p.rules...) }

// exec answers the programs, and does what the testkit and cosmic-ray do to the
// disk.
func (p *pull) exec(_ context.Context, c atoms.Cmd) (string, int) {
	line := strings.Join(append([]string{c.Name}, c.Args...), " ")
	// the workers run at once
	p.mu.Lock()
	p.runs = append(p.runs, filepath.Base(c.Dir)+": "+line)
	p.cmds = append(p.cmds, c)
	rules := p.rules
	if strings.Contains(line, "uv sync") {
		p.syncs++
		if p.failSyncFrom > 0 && p.syncs >= p.failSyncFrom {
			p.mu.Unlock()
			return "error: no wheel for a worker", 2
		}
	}
	p.mu.Unlock()
	for _, r := range rules {
		if strings.Contains(line, r.needle) {
			if c.Stderr != nil {
				*c.Stderr = r.stderr
			}
			return r.out, r.code
		}
	}
	args := c.Args
	flag := func(name string) string {
		for i, a := range args {
			if a == name && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	switch {
	case strings.Contains(line, "forge-testkit-mutation scope"):
		for _, e := range c.Env {
			if v, ok := strings.CutPrefix(e, "GITHUB_OUTPUT="); ok {
				putFile(p.t, "/", v, p.scopeOut)
			}
		}
	case strings.Contains(line, "forge-testkit-mutation plugin"):
		putFile(p.t, flag("--out"), "_forge_mutation_select.py", "plugin")
	case strings.Contains(line, "forge-testkit-mutation map"):
		putFile(p.t, "/", flag("--out"), "{}")
	case strings.Contains(line, "forge-testkit-mutation partition"):
		for i, a := range args {
			if a == "--out" {
				putFile(p.t, "/", args[i+1], "partition "+args[i+1])
			}
		}
	case strings.Contains(line, "cosmic-ray exec"):
		for _, e := range c.Env {
			if v, ok := strings.CutPrefix(e, "FORGE_MUT_SELECT_LOG="); ok {
				putFile(p.t, "/", v, "selected by "+filepath.Base(c.Dir)+"\n")
			}
		}
	case strings.Contains(line, "forge-testkit-mutation report") && c.Stderr != nil:
		*c.Stderr = ""
		return "", 0
	}
	return "", 0
}

func (p *pull) ran(needle string) bool {
	for _, r := range p.runs {
		if strings.Contains(r, needle) {
			return true
		}
	}
	return false
}

func (p *pull) count(needle string) int {
	n := 0
	for _, r := range p.runs {
		if strings.Contains(r, needle) {
			n++
		}
	}
	return n
}

func (p *pull) call(needle string) (atoms.Cmd, bool) {
	for i, r := range p.runs {
		if strings.Contains(r, needle) {
			return p.cmds[i], true
		}
	}
	return atoms.Cmd{}, false
}

func (p *pull) run() checks.Verdict { return Check(p.t.Context(), p.in) }

func said(v checks.Verdict) string { return v.Reason + "\n" + strings.Join(v.Logs, "\n") }

// excludes is the --exclude-operator pairs the Python rows put on the scope call.
func excludes() []string {
	var out []string
	for _, op := range checks.SkippedMutators(checks.MutatorPython) {
		out = append(out, "--exclude-operator", op)
	}
	return out
}
