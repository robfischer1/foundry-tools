package rustmutation

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

// THE FIXTURE: a pull that added a rust line to a one-crate workspace, with every
// program the atom starts answered from a script (first rule wins), and cargo
// mutants leaving the lists it leaves in the directory it was told to write to.

const (
	sinceSha = "since0"
	rustDiff = "diff --git a/src/lib.rs b/src/lib.rs\n--- a/src/lib.rs\n+++ b/src/lib.rs\n@@ -1 +1 @@\n-fn f() -> i32 { 1 }\n+fn f() -> i32 { 2 }"
	rustTree = "100644 blob m\tCargo.toml\x00100644 blob l\tCargo.lock\x00100644 blob s\tsrc/lib.rs\x00"
)

// rule answers a program whose argv line contains needle.
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
	work  string
	// lists are the files cargo mutants leaves in mutants.out, by name.
	lists map[string]string
	// printed is what the mutants run says, and code how it exits.
	printed string
	code    int
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
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	root := t.TempDir()
	putFile(t, root, "Cargo.toml", "[package]\nname='x'\n")
	putFile(t, root, "src/lib.rs", "fn f() -> i32 { 2 }\n")
	files := []string{"Cargo.toml", "src/lib.rs"}
	if answers != "" {
		putFile(t, root, ".copier-answers.yml", answers)
		files = append(files, ".copier-answers.yml")
	}
	moldAvailable = func() bool { return true }
	t.Cleanup(func() { moldAvailable = func() bool { return true } })
	p := &pull{t: t, work: work, lists: map[string]string{"caught": "src/lib.rs:1:1: replace f -> i32 with 0\n"}}
	p.in = atomkit.In{Root: root, Files: files, Base: "abc123", Exec: p.exec}
	p.rules = []rule{
		{"merge-base", sinceSha + "\n", 0},
		{"--name-only -z", "src/lib.rs\x00", 0},
		{"git diff --relative", rustDiff, 0},
		{"cargo metadata", `{"packages":[{"name":"x","id":"x","manifest_path":"` + root + `/Cargo.toml"}],"workspace_members":["x"]}`, 0},
		{"ls-tree", rustTree, 0},
	}
	return p
}

func (p *pull) first(r rule) { p.rules = append([]rule{r}, p.rules...) }

// exec answers the programs; the mutants run (through the execmem link) writes
// its lists and says what it printed.
func (p *pull) exec(_ context.Context, c atoms.Cmd) (string, int) {
	line := strings.Join(append([]string{c.Name}, c.Args...), " ")
	p.runs = append(p.runs, line)
	p.cmds = append(p.cmds, c)
	for _, r := range p.rules {
		if strings.Contains(line, r.needle) {
			return r.out, r.code
		}
	}
	if strings.Contains(line, "cargo mutants --colors") {
		for i, a := range c.Args {
			if a == "--output" {
				for name, body := range p.lists {
					putFile(p.t, c.Args[i+1], filepath.Join("mutants.out", name+".txt"), body)
				}
			}
		}
		return p.printed, p.code
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

func (p *pull) call(needle string) (atoms.Cmd, bool) {
	for i, r := range p.runs {
		if strings.Contains(r, needle) {
			return p.cmds[i], true
		}
	}
	return atoms.Cmd{}, false
}

func (p *pull) run() checks.Verdict { return check(p.t.Context(), p.in) }

func said(v checks.Verdict) string { return v.Reason + "\n" + strings.Join(v.Logs, "\n") }

// rustExcludes is the --exclude-re pairs the Rust rows put on the argv.
func rustExcludes() []string {
	var out []string
	for _, re := range checks.SkippedMutators(checks.MutatorRust) {
		out = append(out, "--exclude-re", re)
	}
	return out
}
