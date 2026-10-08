package main

import (
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// coveredBy says whether the include list reaches dir: by its own entry, or by a
// directory entry above it.
func coveredBy(dir string) bool {
	for _, p := range atomsSourceInclude {
		root, ok := strings.CutSuffix(p, "/**")
		if ok && (dir == root || strings.HasPrefix(dir, root+"/")) {
			return true
		}
	}
	return false
}

// THE FILTERED SOURCE IS ONLY AS GOOD AS ITS LIST. The atoms binary is built in
// the engine from atomsSourceInclude and nothing else, so a package it imports
// that the list omits is a `go build` failure no unit test can see: the shadow
// would report "the binary did not answer" on every run, forever, and look like
// a finding about the binary. This walks the import closure of ./atoms through
// the module's own packages and holds the list to it in both directions - a
// missing directory fails the build, a stale one makes every unrelated edit
// rebuild the binary.
func TestAtomsSourceCoversImports(t *testing.T) {
	const prefix = "dagger/foundry-tools/"
	reached := map[string]bool{}
	queue := []string{"atoms"}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		if reached[dir] {
			continue
		}
		reached[dir] = true
		if !coveredBy(dir) {
			t.Errorf("%s is imported by the atoms binary but atomsSourceInclude does not include it", dir)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				if rel, ok := strings.CutPrefix(path, prefix); ok {
					queue = append(queue, rel)
				}
			}
		}
	}
	for _, p := range atomsSourceInclude {
		root, ok := strings.CutSuffix(p, "/**")
		if !ok {
			if p != "go.mod" && p != "go.sum" {
				t.Errorf("%q is neither a package directory (/**) nor a module file", p)
			}
			continue
		}
		if !reached[root] {
			t.Errorf("%s is in atomsSourceInclude but the atoms binary does not import it", root)
		}
	}
	if !reached["internal/atoms"] || !reached["internal/checks"] {
		t.Errorf("the walk never reached the packages it exists for: %v", reached)
	}
}

func TestRenderShadow(t *testing.T) {
	yaml := checks.AtomByID("fleet:check-yaml")
	today := []checks.Verdict{checks.VerdictOf(yaml, 0, "")}
	agree, _ := json.Marshal(today)
	differ, _ := json.Marshal([]checks.Verdict{checks.VerdictOf(yaml, 2, "binary could not")})
	for _, tc := range []struct {
		name     string
		today    []checks.Verdict
		todayErr error
		raw      string
		rawErr   error
		want     string
	}{
		{"the chains did not answer", nil, errors.New("engine gone"), string(agree), nil, "not compared - the chains did not answer: engine gone"},
		{"the binary did not answer", today, nil, "", errors.New("exit 1"), "not compared - the binary did not answer: exit 1"},
		{"the binary printed something that is not a vector", today, nil, "panic: oops", nil, "not compared - the module's output is not a verdict vector"},
		{"agreement", today, nil, string(agree), nil, "shadow atoms: 1 compared, 1 identical, 0 same state, 0 state differs"},
		{"disagreement", today, nil, string(differ), nil, "fleet:check-yaml: STATE DIFFERS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderShadow(tc.today, tc.todayErr, tc.raw, tc.rawErr); !strings.Contains(got, tc.want) {
				t.Errorf("report %q lacks %q", got, tc.want)
			}
		})
	}
}

// The chains' error wins when both sides failed: it is the one whose absence
// makes the comparison meaningless, and the report names one cause.
func TestRenderShadowNamesTheChainsFirst(t *testing.T) {
	got := renderShadow(nil, errors.New("a"), "", errors.New("b"))
	if !strings.Contains(got, "the chains did not answer: a") || strings.Contains(got, "binary") {
		t.Errorf("report %q", got)
	}
}
