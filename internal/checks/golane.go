package checks

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// GoHasTestFiles reads the output of
//
//	go list -f '{{len .TestGoFiles}}{{len .XTestGoFiles}}' ./...
//
// — one line per package, two counts run together — and answers whether ANY
// package carries a test file. A module where every line is some spelling of
// zero has nothing for `go test` to run, and go would print "[no test files]"
// per package and exit 0; that is the green this check exists to refuse.
func GoHasTestFiles(goListOutput string) bool {
	for _, line := range strings.Split(goListOutput, "\n") {
		if strings.Trim(strings.TrimSpace(line), "0") != "" {
			return true
		}
	}
	return false
}

// argvBudget is the byte budget a file list is allowed to occupy as arguments
// before it goes in as a file instead.
//
// Linux's ARG_MAX is 2 MiB and the whole argv plus the environment has to fit
// inside it. 128 KiB is a sixteenth of that and is NOT a measured fleet
// maximum — no repository here has been counted against it, and the expected
// answer for every one of them is false. The budget exists so that a tree
// nobody anticipated meets a documented second path rather than an E2BIG from
// the kernel, which would reach the vector as an engine error naming nothing.
const argvBudget = 128 << 10

// NeedsArgFile reports whether a file list is too long to hand a tool as
// arguments, so the caller writes it NUL-joined to a file and runs the tool
// through xargs -0 -a instead. One NUL per file, so the joined length is
// exactly what the kernel would carry.
func NeedsArgFile(files []string) bool {
	n := 0
	for _, f := range files {
		n += len(f) + 1
		if n > argvBudget {
			return true
		}
	}
	return false
}

// GovulncheckExit translates govulncheck's exit vocabulary into the atom's:
// govulncheck answers 3 when it FOUND A VULNERABILITY, 0 when it found none,
// and 1 or 2 when it could not load or run. StateFor reads a 3 as could-not-
// run, so without this a real finding would be filed as a broken check — the
// old shell body's `|| exit 1` hid the code entirely, and the first typed cut
// passed it through raw (caught by the paper engine, 2026-09-12).
func GovulncheckExit(code int) int {
	if code == 3 {
		return 1
	}
	return code
}

// GoModuleDirs answers the directory of every Go module a population carries,
// the root first as "." and the rest sorted.
//
// EVERY go.mod, NOT ONLY THE ROOT'S. The lane used to be declared by a root
// go.mod alone, which left a module one directory down ungated in every
// repository: foundry-stocks' tools/forge — the forge that publishes the
// fleet's base images — ran none of this lane's atoms, and neither did chaos'
// styx and bigintschema beside a root module, because `go vet ./...` at a
// root does not descend into a nested module. Rob, 2026-09-16: fleet wide.
//
// A go.mod the go command itself would never build is not a module here: one
// under vendor/ or testdata/, or under a directory whose name starts with "_"
// or ".", the same directories `./...` skips. That is the answer to this
// file's older worry — a vendored go.mod three directories down reporting
// CANNOT RUN forever on a repository with nothing to check.
func GoModuleDirs(files []string) []string {
	var dirs []string
	root := false
	for _, f := range files {
		dir, name := path.Split(f)
		dir = strings.TrimSuffix(dir, "/")
		if name != "go.mod" {
			continue
		}
		if dir == "" {
			root = true
			continue
		}
		if !goBuildsUnder(dir) {
			continue
		}
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	if root {
		dirs = append([]string{"."}, dirs...)
	}
	return dirs
}

// GoModulesWithSource are the modules of dirs (GoModuleDirs' answer) that have
// a Go package to load: the root, always (a repository declares the go lane by
// it), and every nested module that owns a tracked .go file. A .go file belongs
// to the nearest module above it, and one under vendor/, testdata/ or a hidden
// directory belongs to none.
//
// DEF-B6-037. A go.mod and go.sum that only pin tools (base-images'
// bases/layer-go/tools) hold no package, and `go vet ./...`, `go test ./...`
// and govulncheck exit non-zero on a module with nothing to load: a verdict
// about the tree's shape filed as a finding. files must be the whole
// population; a listing narrowed to go.mod would drop every nested module.
func GoModulesWithSource(dirs, files []string) []string {
	isMod := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		isMod[d] = true
	}
	owns := map[string]bool{}
	for _, f := range files {
		if !strings.HasSuffix(f, ".go") || vendoredOrHidden(path.Dir(f)) {
			continue
		}
		for d := path.Dir(f); ; d = path.Dir(d) {
			if isMod[d] {
				owns[d] = true
				break
			}
			if d == "." || d == "/" {
				break
			}
		}
	}
	var out []string
	for _, d := range dirs {
		if d == "." || owns[d] {
			out = append(out, d)
		}
	}
	return out
}

// goBuildsUnder reports whether the go command would build a package in dir.
func goBuildsUnder(dir string) bool { return !vendoredOrHidden(dir) }

// vendoredOrHidden reports whether dir is one NO lane counts as the
// repository's own source: vendor/, testdata/, and any segment starting with
// "_" or ".".
//
// It is the set `go ./...` skips, which is why it was written here first — and
// it is the same set a .py file under it is not the repository's python, which
// is why PythonFiles reads it too rather than keeping a second copy that could
// drift. Naming it apart from the go lane is the whole point: the predicate is
// about the TREE, and only goBuildsUnder's sentence is about go.
func vendoredOrHidden(dir string) bool {
	for _, seg := range strings.Split(dir, "/") {
		if seg == "vendor" || seg == "testdata" || strings.HasPrefix(seg, "_") || strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// ModuleVerdict is one Go module's answer to one atom.
type ModuleVerdict struct {
	Dir     string
	Verdict Verdict
}

// FoldModules is one atom's verdict over every module it ran in.
//
// FINDINGS OUTRANK CANNOT RUN. A finding in one module is a red the pull
// carries whatever another module would have said, and filing the fold as
// could-not-run would send the sweep to re-ask a question whose answer is
// already no. With no finding, a module that could not run makes the atom
// could-not-run, because a module that was not checked is not a module that
// passed.
//
// Every module's own reason is kept under its directory, passes included: the
// test-race and mutation atoms put their scope line on a pass, and a fold that
// dropped it would hide which modules ran against which databases.
func FoldModules(a AtomDef, mods []ModuleVerdict) Verdict {
	state := StatePass
	var dirs []string
	for _, m := range mods {
		s := State(m.Verdict.State)
		if s == StateFindings || (s != StatePass && state == StatePass) {
			state = s
		}
	}
	for _, m := range mods {
		if State(m.Verdict.State) == state {
			dirs = append(dirs, m.Dir)
		}
	}
	var b strings.Builder
	switch state {
	case StatePass:
		fmt.Fprintf(&b, "%s: PASS in %s (%s)", a.ID, goModules(len(mods)), strings.Join(dirs, ", "))
	case StateFindings:
		fmt.Fprintf(&b, "%s: FINDINGS in %d of %s (%s)", a.ID, len(dirs), goModules(len(mods)), strings.Join(dirs, ", "))
	default:
		fmt.Fprintf(&b, "%s: CANNOT RUN in %d of %s (%s) — a module that was not checked is not a module that passed.", a.ID, len(dirs), goModules(len(mods)), strings.Join(dirs, ", "))
	}
	// Each module's gradings are its own units', repo-relative, so the fold is
	// their concatenation.
	var gradings []Grading
	var audits []string
	for _, m := range mods {
		fmt.Fprintf(&b, "\n── module %s ──\n%s", m.Dir, m.Verdict.Reason)
		gradings = append(gradings, m.Verdict.Gradings...)
		audits = append(audits, m.Verdict.Audit)
	}
	return Verdict{
		Atom:     a.ID,
		Stage:    a.Stage,
		Lane:     string(a.Lane),
		State:    int(state),
		Result:   state.String(),
		Reason:   b.String(),
		Gradings: gradings,
		Audit:    FoldAudits(audits),
	}
}

// goModules counts modules in words: "1 Go module", "3 Go modules".
func goModules(n int) string {
	if n == 1 {
		return "1 Go module"
	}
	return fmt.Sprintf("%d Go modules", n)
}

// ArgvChunks splits a file list into runs that each fit the argv budget, in
// order, so a tool is exec'd once per run with the files as arguments. A single
// path over the budget is a run of its own. It is the in-process form of what
// NeedsArgFile's xargs fallback did: one program, fixed flags, the list
// appended in as many invocations as the budget takes, and no shell.
func ArgvChunks(files []string) [][]string {
	var chunks [][]string
	var run []string
	n := 0
	for _, f := range files {
		if len(run) > 0 && n+len(f)+1 > argvBudget {
			chunks = append(chunks, run)
			run, n = nil, 0
		}
		run = append(run, f)
		n += len(f) + 1
	}
	if len(run) > 0 {
		chunks = append(chunks, run)
	}
	return chunks
}
