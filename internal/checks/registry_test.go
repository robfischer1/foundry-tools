package checks

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// THE TWO-SURFACE GUARD, READ OFF THE SOURCE.
//
// The catalogue (Atoms, this package) says WHAT EXISTS; the registry
// (register() in each atoms_<lane>.go, package main) says HOW EACH ONE RUNS.
// register() panics at load for an id the catalogue does not carry and for an
// id registered twice — but nothing at load can see the other direction, a
// catalogue row that no atoms_*.go ever registered, because a registry is not
// asked about an id nobody calls it with. That row is an atom `dagger check -l`
// lists and `Verdicts` errors on, which is the two-surface drift this module
// exists to delete arriving from the one side the runtime cannot police.
//
// Package main is not importable from a test (`dag` panics without an engine,
// and a module's own package main is not a library), so the assertion is made
// over the SOURCE: go/parser reads every atoms_*.go at the module root and the
// register() calls are collected as data. That is what the `sh -n` tests over
// AtomDef.Script were standing in for, and it holds the same line — nothing in
// the module answers a verdict that this package has not catalogued, and
// nothing catalogued answers no verdict at all.

// laneSource is every atoms_<lane>.go at the module root, parsed.
type laneSource struct {
	fset  *token.FileSet
	files map[string]*ast.File
}

// moduleRoot is two directories up from this file — internal/checks/ — found
// off the compiler's own record of where this source lives rather than off the
// working directory, so `go test ./...` from anywhere finds the same tree.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate this test's own source file, so the module root is unknown")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(self)))
}

func parseLaneFiles(t *testing.T) laneSource {
	t.Helper()
	root := moduleRoot(t)
	paths, err := filepath.Glob(filepath.Join(root, "atoms_*.go"))
	if err != nil {
		t.Fatalf("could not enumerate %s: %v", root, err)
	}
	if len(paths) == 0 {
		t.Fatalf("no atoms_*.go under %s — the registry has no source to read, "+
			"which is either a moved tree or every lane deleted", root)
	}
	src := laneSource{fset: token.NewFileSet(), files: map[string]*ast.File{}}
	for _, p := range paths {
		f, err := parser.ParseFile(src.fset, p, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("%s does not parse: %v", p, err)
		}
		src.files[p] = f
	}
	return src
}

// registrations answers every `register("<id>", …)` the lane files make, as
// id -> the files it was registered from. A second file for one id is the
// duplicate register() panics on at load; here it is a failing test instead of
// a gate that dies on its first call.
func (s laneSource) registrations(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for path, f := range s.files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || ident.Name != "register" || len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Errorf("%s: register() is called with something other than a string literal, "+
					"so the catalogue id cannot be read from the source", s.fset.Position(call.Pos()))
				return true
			}
			id, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Errorf("%s: register()'s id %s does not unquote: %v", s.fset.Position(lit.Pos()), lit.Value, err)
				return true
			}
			out[id] = append(out[id], filepath.Base(path))
			return true
		})
	}
	return out
}

// funcBodies maps a top-level function's name to its declaration, for the
// assertions that are about ONE atom's runner rather than about a file.
func (s laneSource) funcBodies() map[string]*ast.FuncDecl {
	out := map[string]*ast.FuncDecl{}
	for _, f := range s.files {
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil {
				out[fn.Name.Name] = fn
			}
		}
	}
	return out
}

// runnerNames answers the function each id was registered with, where the
// second argument is a plain identifier. A registration built from a call —
// forgeTestkitLint("fake-placement", "*.py") — has no single function of its
// own and is skipped by the callers that need a body.
func (s laneSource) runnerNames() map[string]string {
	out := map[string]string{}
	for _, f := range s.files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || ident.Name != "register" || len(call.Args) != 2 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				return true
			}
			id, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if fn, ok := call.Args[1].(*ast.Ident); ok {
				out[id] = fn.Name
			}
			return true
		})
	}
	return out
}

// stringLiterals is every string constant inside a node, unquoted.
func stringLiterals(n ast.Node) []string {
	var out []string
	ast.Inspect(n, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if s, err := strconv.Unquote(lit.Value); err == nil {
			out = append(out, s)
		}
		return true
	})
	return out
}

func (s laneSource) allLiterals() []string {
	var out []string
	for _, f := range s.files {
		out = append(out, stringLiterals(f)...)
	}
	return out
}

// THE CATALOGUE AND THE REGISTRY NAME THE SAME SET, EXACTLY. Missing is an atom
// that lists and cannot run; extra cannot happen at runtime (register panics)
// but is asserted anyway, because this test reads the source and a rename of a
// catalogue row would otherwise surface as a panic on the next gate rather than
// here.
func TestEveryCatalogueIDIsRegisteredExactlyOnce(t *testing.T) {
	src := parseLaneFiles(t)
	registered := src.registrations(t)

	catalogued := map[string]bool{}
	for _, a := range Atoms {
		catalogued[a.ID] = true
	}

	var missing, extra []string
	for id := range catalogued {
		if len(registered[id]) == 0 {
			missing = append(missing, id)
		}
	}
	for id, files := range registered {
		if !catalogued[id] {
			extra = append(extra, id+" ("+strings.Join(files, ", ")+")")
		}
		if len(files) > 1 {
			t.Errorf("%q is registered %d times (%s) — register() panics at load for this, "+
				"so every gate in the fleet would die before answering a verdict",
				id, len(files), strings.Join(files, ", "))
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("catalogued with no runner registered: %v — `dagger check -l` lists each of these "+
			"and verdictFor errors on it, which is a listed check that cannot run", missing)
	}
	if len(extra) > 0 {
		t.Errorf("registered but not in the catalogue: %v — register() panics on this at load", extra)
	}
	if len(registered) != len(Atoms) {
		t.Errorf("%d ids registered, %d rows catalogued", len(registered), len(Atoms))
	}
}

// RULE 7, ENFORCED. No `sh -c` anywhere: the whole of this branch is that an
// atom is a typed chain of execs the engine can cache in parts and schedule,
// and one shell invocation hands it back an opaque step. A pipe is the sign the
// rest belongs in Go.
func TestNoLaneFileExecsAShell(t *testing.T) {
	src := parseLaneFiles(t)
	for path, f := range src.files {
		ast.Inspect(f, func(n ast.Node) bool {
			comp, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			var elems []string
			for _, e := range comp.Elts {
				lit, ok := e.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					elems = append(elems, "")
					continue
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					s = ""
				}
				elems = append(elems, s)
			}
			for i := 0; i+1 < len(elems); i++ {
				if (elems[i] == "sh" || elems[i] == "bash") && elems[i+1] == "-c" {
					t.Errorf("%s: %s builds `%s -c` — runtime.go rule 7 says no shell, "+
						"and a shell is one opaque step the engine can neither cache in parts nor schedule",
						filepath.Base(path), src.fset.Position(comp.Pos()), elems[i])
				}
			}
			return true
		})
	}
}

// announcedAbsence is the shape VerdictOf reads off an atom's output to tell
// "there was nothing to check" from "I checked and it was clean". The prefix is
// matched NON-GREEDILY and is not required to look like an id, because the
// measured defect was an announcement that did not: catching only well-formed
// ids would be the test agreeing with the bug.
//
// A literal built as `a.ID+": ABSENT - …"` begins at the colon and matches
// nothing here — it is right by construction, which is the point of writing it
// that way.
var announcedAbsence = regexp.MustCompile(`^(.{1,80}?): ABSENT`)

// EVERY ABSENCE AN ATOM ANNOUNCES MUST CARRY THAT ATOM'S OWN ID, because
// AnnouncedAbsence matches on the id as a prefix and reads anything else as a
// pass.
//
// MEASURED 2026-09-10, live in three atoms: the forge-testkit ports announced
// `forge-testkit assertion-free: ABSENT`, while the id is
// `python:forge-testkit-assertion-free`. No prefix, no match — so on every
// repository that never took the forge-testkit dependency, which is most of the
// fleet, three verdicts read `pass` over a scan that had not happened. That is
// the conflation VerdictOf was written to prevent, and its own comment names
// the run where 28 of 86 verdicts read `pass` with `absent=0` while nothing had
// been examined.
//
// Most announcements are built as `a.ID+": ABSENT - …"` and are right by
// construction; this catches the ones spelled out, which is where the defect
// was.
func TestEveryAnnouncedAbsenceCarriesItsOwnAtomID(t *testing.T) {
	src := parseLaneFiles(t)
	runners := src.runnerNames()
	bodies := src.funcBodies()

	for _, s := range src.allLiterals() {
		m := announcedAbsence.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		if !AtomExists(m[1]) {
			t.Errorf("an absence is announced as %q, and %q is not an atom id — "+
				"AnnouncedAbsence matches on the id as a prefix, so this renders as a PASS "+
				"over a scan that did not happen", s, m[1])
		}
	}

	for id, fnName := range runners {
		fn, ok := bodies[fnName]
		if !ok {
			continue
		}
		for _, s := range stringLiterals(fn) {
			m := announcedAbsence.FindStringSubmatch(s)
			if m == nil || m[1] == id {
				continue
			}
			t.Errorf("%s (%s) announces an absence as %q — it renders as a PASS, "+
				"because AnnouncedAbsence reads another atom's id as no announcement at all",
				id, fnName, s)
		}
	}
}

// THE RULESETS ARE THE FLEET'S, AND NO ATOM READS THE REPOSITORY'S. Rob,
// 2026-09-11: a repo has no say in anything that runs; the fleet decides the
// atoms AND their rulesets. Until then ruff read the repo's pyproject, mypy its
// [tool.mypy], eslint its eslint.config.mjs, staticcheck any staticcheck.conf,
// clippy the manifest's [lints], and the fleet atoms read .pre-commit-config.yaml
// for their population and their arguments — seven repos had drifted from the
// template's ruleset and three carried none.
func TestNoAtomReadsARepoAuthoredConfigSurface(t *testing.T) {
	src := parseLaneFiles(t)
	for _, s := range src.allLiterals() {
		for _, forbidden := range []string{".pre-commit-config.yaml", "hookmeta", "hookpopulation", "staticcheck.conf"} {
			if strings.Contains(s, forbidden) {
				t.Errorf("a lane file names %q in %q — a repo-authored surface deciding what the gate does", forbidden, s)
			}
		}
	}
}

// THE CHECK SETS ARE NAMED ON THE COMMAND LINE, so a config file a repository
// adds later changes nothing in the gate.
//
// staticcheck's is EIGHT EXCLUSIONS, NOT SEVEN: the first cut listed -ST1000 …
// -ST1022 and left out -ST1023 (redundant type in a declaration), which
// staticcheck's own default config also disables — so the first Go pull through
// it (ourea #127) went red on a pre-existing `var fs billy.Filesystem = …` in a
// test file. This is staticcheck's shipped default, verbatim; a stricter fleet
// set is a decision to make on purpose, not by omission.
//
// clippy's reaches rustc after `--`, where it wins over the manifest's
// [workspace.lints] table whatever a crate put there.
func TestTheNamedCheckSetsAreTheFleets(t *testing.T) {
	src := parseLaneFiles(t)
	lits := strings.Join(src.allLiterals(), "\x00")

	const staticcheckSet = "all,-ST1000,-ST1003,-ST1016,-ST1020,-ST1021,-ST1022,-ST1023"
	if !strings.Contains(lits, staticcheckSet) {
		t.Errorf("no lane file names staticcheck's check set %q — without -checks on the command line, "+
			"a staticcheck.conf in the tree decides what the gate looks for", staticcheckSet)
	}
	for _, arg := range []string{"clippy::all", "-D", "warnings"} {
		if !strings.Contains(lits, arg) {
			t.Errorf("no lane file names clippy's %q, so the manifest's [lints] table would decide the lint set", arg)
		}
	}
}

// A SWEEP ATOM RUNS UNATTENDED ON A CLOCK, which is precisely where a silent
// pass does the most damage: nobody is watching the run, so a body that answers
// 0 because its tool never arrived reads as a clean fleet for as long as it
// takes somebody to look. Each must be able to say CANNOT RUN in its own
// words — verdict() files an engine error as state 2 by construction, but the
// refusals a sweep atom reaches on purpose (an unreachable catalogue, a
// canonical script that did not mount) are its own to name.
//
// AND IT MUST ANNOUNCE AN ABSENCE, for the same reason one shelf up: a sweep
// runs against every repo in custody and most repos carry no surface for most
// of these, so an atom that exits 0 in silence is indistinguishable from a scan
// that found nothing.
func TestEverySweepAtomCanRefuseAndAnnounceAbsence(t *testing.T) {
	src := parseLaneFiles(t)
	runners := src.runnerNames()
	bodies := src.funcBodies()

	for _, a := range SweepAtoms() {
		fnName, ok := runners[a.ID]
		if !ok {
			t.Errorf("sweep atom %q has no registered runner to read", a.ID)
			continue
		}
		fn, ok := bodies[fnName]
		if !ok {
			t.Errorf("sweep atom %q registers %s, which is not a function in any atoms_*.go", a.ID, fnName)
			continue
		}
		lits := strings.Join(stringLiterals(fn), "\x00")
		if !strings.Contains(lits, "CANNOT RUN") {
			t.Errorf("sweep atom %q (%s) names no CANNOT RUN of its own — an unattended check that "+
				"cannot refuse will report a clean fleet it never examined", a.ID, fnName)
		}
		if !strings.Contains(lits, "ABSENT") {
			t.Errorf("sweep atom %q (%s) never reports ABSENT; on a repo with no surface it would "+
				"answer 0 in silence, which is indistinguishable from a scan that found nothing", a.ID, fnName)
		}
	}
}
