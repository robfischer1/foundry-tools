package tsmut

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// THE FIXTURE: a pull that added TypeScript lines at the root of a repo whose
// root carries a Stryker config and its bin, with every program the atom starts
// answered from a script (first rule wins). Stryker leaves its report where the
// atom reads it.

const (
	sinceSha = "since0"
	tsDiff   = "diff --git a/src/gate.ts b/src/gate.ts\n--- a/src/gate.ts\n+++ b/src/gate.ts\n@@ -1,0 +2,2 @@\n+a\n+b"
	tsKilled = `{"status":"Killed","mutatorName":"BooleanLiteral","testsCompleted":1,"location":{"start":{"line":2}}}`
)

// tsReport is a Stryker mutation.json over one file, with the mutants given.
func tsReport(mutants string) string {
	return `{"config":{"testRunner":"vitest","mutate":["src/gate.ts:2-3"],"coverageAnalysis":"perTest"},` +
		`"testFiles":{"a.test.ts":{"tests":[{"id":"1"}]}},` +
		`"files":{"src/gate.ts":{"source":"a\nb\n","mutants":[` + mutants + `]}}}`
}

type rule struct {
	needle string
	out    string
	code   int
}

type pull struct {
	t     *testing.T
	in    atomkit.In
	rules []rule
	runs  []string
	cmds  []atoms.Cmd
	// reports are the mutation.json each package's stryker leaves, by package
	// directory; a package with none leaves none.
	reports map[string]string
	// printed is what stryker says, and code how it exits.
	printed string
	code    int
	// lcov is what `bun test --coverage` writes to its coverage dir; "" is none.
	lcov string
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
	t.Setenv(checks.CPUsEnv, "12")
	// The pod's memory budget (the dev lane's is 1.25 GiB) would cap the workers
	// below the CPU budget these tests pin.
	t.Setenv(checks.MemEnv, "")
	t.Helper()
	t.Setenv("ATOM_ORIGIN", "")
	t.Setenv("ATOM_WORK", t.TempDir())
	root := t.TempDir()
	files := []string{"package.json", "stryker.config.json"}
	putFile(t, root, "package.json", "{}")
	putFile(t, root, "stryker.config.json", "{}")
	putFile(t, root, "node_modules/.bin/stryker", "")
	if answers != "" {
		putFile(t, root, ".copier-answers.yml", answers)
	}
	p := &pull{t: t, reports: map[string]string{".": tsReport(tsKilled)}}
	p.in = atomkit.In{Root: root, Files: files, Base: "abc123", Exec: p.exec}
	p.rules = []rule{
		{"merge-base", sinceSha + "\n", 0},
		{"git diff --unified=0", tsDiff, 0},
	}
	return p
}

func (p *pull) first(r rule) { p.rules = append([]rule{r}, p.rules...) }

// exec answers the programs; stryker writes its report into its package.
func (p *pull) exec(_ context.Context, c atoms.Cmd) (string, int) {
	line := strings.Join(append([]string{c.Name}, c.Args...), " ")
	p.runs = append(p.runs, line)
	p.cmds = append(p.cmds, c)
	// bun writes its coverage however the suite exits.
	if strings.Contains(line, "bun test --coverage") && p.lcov != "" {
		for i, a := range c.Args {
			if a == "--coverage-dir" {
				putFile(p.t, c.Args[i+1], "lcov.info", p.lcov)
			}
		}
	}
	for _, r := range p.rules {
		if strings.Contains(line, r.needle) {
			return r.out, r.code
		}
	}
	if strings.Contains(line, "--reporters clear-text,json") {
		rel, _ := filepath.Rel(p.tree(), c.Dir)
		if body, ok := p.reports[rel]; ok {
			putFile(p.t, c.Dir, "reports/mutation/mutation.json", body)
		}
		return p.printed, p.code
	}
	return "", 0
}

// tree is where bun installed and stryker ran.
func (p *pull) tree() string {
	for _, c := range p.cmds {
		if len(c.Args) > 0 && c.Name == "bun" && c.Args[0] == "install" {
			return c.Dir
		}
	}
	return p.in.Root
}

func (p *pull) ran(needle string) bool {
	for _, r := range p.runs {
		if strings.Contains(r, needle) {
			return true
		}
	}
	return false
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
