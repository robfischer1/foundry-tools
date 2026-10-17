package gomutation

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// THE FIXTURE: a pull on a one-module Go repository, with every program the atom
// starts answered from a script, first rule wins. A rule that matches nothing
// the atom runs would be a test that proves nothing, so the happy script covers
// each question the atom asks, and a test changes the answer to the one it is
// about. gomutants and mutation-gate also leave their files where the atom reads
// them, as the real tools do.

const (
	sinceSha = "since0"
	// cleanReport is a report where every mutant was killed.
	cleanReport = `{"elapsed_time":1,"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"KILLED","line":1,"column":1}]}]}`
	// survivors has one killed mutant and one that lived.
	survivors = `{"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"KILLED","line":1,"column":1},{"type":"T","status":"LIVED","line":2,"column":3}]}]}`
	// THE CONTROLS ARE READ AS REPORTS, so the fixtures are reports. Shapes taken
	// from real gomutants runs on the real fixtures (2026-09-29): the harness
	// canary's four lines earn TWO mutants, not one, which is exactly why the
	// canary no longer counts them.
	canaryHonest = `{"go_module":"canary","files":[{"file_name":"canary.go","mutations":[` +
		`{"type":"RETURN_ZERO","status":"LIVED","line":4,"column":33},` +
		`{"type":"ARITHMETIC_BASE","status":"LIVED","line":4,"column":35}]}]}`
	// A KILL IS THE BROKEN SHAPE. The fixture's mutants are under-tested on
	// purpose, so a runner that reports one dead did not run the tests it thinks
	// it did.
	canaryKilled = `{"go_module":"canary","files":[{"file_name":"canary.go","mutations":[` +
		`{"type":"ARITHMETIC_BASE","status":"KILLED","line":4,"column":35}]}]}`
	// The package-main control's honest shape: two LIVED in cmd/tool, plus NOT
	// COVERED mutants in `func main` that no test can reach.
	mainCanaryHonest = `{"go_module":"maincanary","files":[{"file_name":"main.go","mutations":[` +
		`{"type":"RETURN_ZERO","status":"LIVED","line":4,"column":33},` +
		`{"type":"ARITHMETIC_BASE","status":"LIVED","line":4,"column":35},` +
		`{"type":"INTEGER_INCREMENT","status":"NOT COVERED","line":6,"column":23}]}]}`
	mainCanaryKilled = `{"go_module":"maincanary","files":[{"file_name":"main.go","mutations":[` +
		`{"type":"ARITHMETIC_BASE","status":"KILLED","line":4,"column":35}]}]}`
)

// rule answers a program whose argv line contains needle.
type rule struct {
	needle string
	out    string
	code   int
	// stderr is what mutation-gate says on its own stream.
	stderr string
}

// pull is the repository, the input over it, and the programs' answers.
type pull struct {
	t      *testing.T
	in     atomkit.In
	rules  []rule
	runs   []string
	cmds   []atoms.Cmd
	work   string
	module string // the module's directory in the tree
	// report is what the measured gomutants run writes; "" writes none.
	report string
	// canary and mainCanary are what the two controls' gomutants runs write.
	canary, mainCanary string
	// cache is gomutants' -cache file.
	cache string
	// asked answers a program by how many times it has been asked: n is the
	// count before this ask. Used for a run that fails once and then does not.
	asked map[string]func(n int) (string, int)
	count map[string]int
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

func newPull(t *testing.T, module string) *pull {
	t.Helper()
	t.Setenv("ATOM_ORIGIN", "")
	t.Setenv(atoms.EnvMutantsToken, "")
	t.Setenv(atoms.EnvSha, "")
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	root := t.TempDir()
	putFile(t, root, filepath.Join(module, "go.mod"), "module x\n\ngo 1.26\n")
	putFile(t, root, filepath.Join(module, "a.go"), "package x\n")
	files := []string{filepath.Join(module, "go.mod"), filepath.Join(module, "a.go")}
	p := &pull{t: t, work: work, module: module, count: map[string]int{}, report: cleanReport, canary: canaryHonest, mainCanary: mainCanaryHonest}
	p.in = atomkit.In{Root: root, Files: files, Base: "abc123", Exec: p.exec}
	abs := filepath.Join(root, module)
	p.rules = []rule{
		{"merge-base", sinceSha + "\n", 0, ""},
		{"git diff --relative", "a.go\n", 0, ""},
		{"ls-tree", "100644 blob b1\tgo.mod\x00100644 blob b2\ta.go\x00", 0, ""},
		{"git -C " + root + " diff --unified=0", "+++ b/a.go\n@@ -1 +1 @@\n+x\n", 0, ""},
		{"-deps -test", "m\t" + abs + "\t\n", 0, ""},
		{"-f {{if or .GoFiles .CgoFiles}}", abs + "\n", 0, ""},
		{"-f {{if eq .Name", "", 0, ""},
		{"-f {{.ImportPath}}", "", 0, ""},
		{"mutation-gate", `{"noise":[]}` + "\n", 0, ""},
	}
	// no tagged suite: grep finds no match, which is an answer
	p.first(rule{"grep -rhoE", "", 1, ""})
	return p
}

// first puts a rule in front of the others.
func (p *pull) first(r rule) { p.rules = append([]rule{r}, p.rules...) }

// exec is the programs' side: the answers, and the files the real tools leave.
func (p *pull) exec(_ context.Context, c atoms.Cmd) (string, int) {
	line := strings.Join(append([]string{c.Name}, c.Args...), " ")
	p.runs = append(p.runs, filepath.Base(c.Dir)+": "+line)
	p.cmds = append(p.cmds, c)
	for needle, f := range p.asked {
		if strings.Contains(line, needle) {
			n := p.count[needle]
			p.count[needle]++
			return f(n)
		}
	}
	for _, r := range p.rules {
		if strings.Contains(line, r.needle) {
			if c.Stderr != nil {
				*c.Stderr = r.stderr
			}
			return r.out, r.code
		}
	}
	if c.Name == "gomutants" {
		return p.gomutants(c)
	}
	return "", 0
}

var outputFlag = regexp.MustCompile(`-output (\S+)`)

// gomutants writes the report the script holds for whichever run it is: one of
// the two controls (-cache=off) or the measured run.
func (p *pull) gomutants(c atoms.Cmd) (string, int) {
	line := strings.Join(c.Args, " ")
	m := outputFlag.FindStringSubmatch(line)
	body := p.report
	switch {
	case strings.Contains(line, "-cache=off") && strings.Contains(c.Dir, "maincanary"):
		body = p.mainCanary
	case strings.Contains(line, "-cache=off"):
		body = p.canary
	case p.cache != "":
		if i := strings.Index(line, "-cache "); i >= 0 {
			putFile(p.t, "/", strings.Fields(line[i+len("-cache "):])[0], p.cache)
		}
	}
	if m != nil && body != "" {
		putFile(p.t, "/", m[1], body)
	}
	return "gomutants: done", 0
}

// ran says whether any run's argv line contains the needle.
func (p *pull) ran(needle string) bool {
	for _, r := range p.runs {
		if strings.Contains(r, needle) {
			return true
		}
	}
	return false
}

// call is the first command whose argv line contains the needle.
func (p *pull) call(needle string) (atoms.Cmd, bool) {
	for _, c := range p.cmds {
		if strings.Contains(strings.Join(append([]string{c.Name}, c.Args...), " "), needle) {
			return c, true
		}
	}
	return atoms.Cmd{}, false
}

// run is the atom over the pull.
func (p *pull) run() checks.Verdict { return check(p.t.Context(), p.in) }

// said is the verdict's reason and logs together: a pass keeps what it said in
// its logs.
func said(v checks.Verdict) string { return v.Reason + "\n" + strings.Join(v.Logs, "\n") }
