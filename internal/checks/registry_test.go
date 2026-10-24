package checks

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

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
	var lits string
	if raw, err := os.ReadFile("../../atoms/go/staticcheck.toml"); err != nil {
		t.Fatal(err)
	} else {
		lits += "\x00" + string(raw)
	}

	// rust:cargo-clippy is a file now: its source carries the set.
	if raw, err := os.ReadFile("../../atoms/rust/cargo-clippy/cargo-clippy.go"); err != nil {
		t.Fatal(err)
	} else {
		lits += "\x00" + string(raw)
	}

	const staticcheckSet = "all,-ST1000,-ST1003,-ST1016,-ST1020,-ST1021,-ST1022,-ST1023"
	if !strings.Contains(lits, staticcheckSet) {
		t.Errorf("no atom file names staticcheck's check set %q — without -checks on the command line, "+
			"a staticcheck.conf in the tree decides what the gate looks for", staticcheckSet)
	}
	for _, arg := range []string{"clippy::all", "-D", "warnings"} {
		if !strings.Contains(lits, arg) {
			t.Errorf("no atom file names clippy's %q, so the manifest's [lints] table would decide the lint set", arg)
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
func TestEveryRehomedCadenceAtomCanRefuseAndAnnounceAbsence(t *testing.T) {
	root := moduleRoot(t)

	// These two came home from the deleted sweep (CA F18). Each still finds a
	// surface most repos do not have - flux/ and ci-matrix.toml - so each must
	// still be able to say ABSENT rather than pass by accident. They live in
	// their own files now (atoms/<ns>/<name>/<name>.go), and the file is read.
	for id, file := range map[string]string{
		"ops:kube-linter":        "atoms/ops/kube-linter/kube-linter.go",
		"template:render-matrix": "atoms/template/render-matrix/render-matrix.go",
	} {
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, file), nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		lits := strings.Join(stringLiterals(f), "\x00")
		if !strings.Contains(lits, "CANNOT RUN") {
			t.Errorf("sweep atom %q (%s) names no CANNOT RUN of its own - an unattended check that "+
				"cannot refuse will report a clean fleet it never examined", id, file)
		}
		if !strings.Contains(lits, "ABSENT") {
			t.Errorf("sweep atom %q (%s) never reports ABSENT; on a repo with no surface it would "+
				"answer 0 in silence, which is indistinguishable from a scan that found nothing", id, file)
		}
	}
}
