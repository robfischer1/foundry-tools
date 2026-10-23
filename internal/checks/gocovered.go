package checks

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/unitkey"
)

// A TEST-ONLY CHANGE IS MUTATION-TESTED BY WHAT IT RUNS (Rob, 2026-10-23: "test-only
// commits do run mutation testing without mutating the tests"). A change that
// edits only tests adds no mutable line, so a diff-scoped run would grade nothing
// and miss the one thing such a change can do: kill a survivor. So the changed
// tests are run alone under coverage, and the SOURCE lines they execute join the
// scope as if they had changed. The test files themselves are never mutated —
// gomutants discovers mutants in non-test files only — and the grading keys keep
// it cheap: a unit whose closure did not move reuses its stored grading.

// goTestFunc names a top-level function the go tool runs as a test.
var goTestFunc = regexp.MustCompile(`^(Test|Fuzz|Example)([A-Z0-9_]|$)`)

// GoChangedTests answers the test functions a change to a _test.go file touched:
// those whose declaration overlaps a changed line. A changed line outside every
// test function (a helper, a fixture, an import) may change any test of the file,
// so then every test function the file declares is answered. A file that does not
// parse answers none, with ok false.
func GoChangedTests(src []byte, changed []unitkey.Range) (names []string, ok bool) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x_test.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, false
	}
	type span struct {
		name       string
		start, end int
	}
	var tests []span
	for _, d := range f.Decls {
		fn, isFn := d.(*ast.FuncDecl)
		if !isFn || fn.Recv != nil || !goTestFunc.MatchString(fn.Name.Name) {
			continue
		}
		tests = append(tests, span{fn.Name.Name, fset.Position(fn.Pos()).Line, fset.Position(fn.End()).Line})
	}
	hit := map[string]bool{}
	outside := false
	for _, r := range changed {
		for l := r.Start; l <= r.End; l++ {
			in := false
			for _, t := range tests {
				if t.start <= l && l <= t.end {
					hit[t.name], in = true, true
				}
			}
			outside = outside || !in
		}
	}
	if outside {
		for _, t := range tests {
			hit[t.name] = true
		}
	}
	return slices.Sorted(maps.Keys(hit)), true
}

// GoTestRunPattern is `go test -run`'s anchored alternation of names.
func GoTestRunPattern(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = regexp.QuoteMeta(n)
	}
	return "^(" + strings.Join(quoted, "|") + ")$"
}

// GoCoveredRanges reads a coverage profile into the lines of every block that ran,
// named from the module root (the profile names `<module path>/<rel>`). Test files
// are never in a profile's rows; a row outside the module is dropped.
func GoCoveredRanges(profile, module string) map[string][]unitkey.Range {
	out := map[string][]unitkey.Range{}
	for _, ln := range strings.Split(profile, "\n") {
		m := goProfileBlock.FindStringSubmatch(strings.TrimSpace(ln))
		if m == nil || m[4] == "0" {
			continue
		}
		rel, inModule := strings.CutPrefix(m[1], module+"/")
		if !inModule || strings.HasSuffix(rel, "_test.go") {
			continue
		}
		start, _ := strconv.Atoi(m[2]) // the pattern matched digits
		end, _ := strconv.Atoi(m[3])
		out[rel] = append(out[rel], unitkey.Range{Start: start, End: end})
	}
	for f, rs := range out {
		out[f] = mergeRanges(rs)
	}
	return out
}

// goProfileBlock is file:startLine.col,endLine.col statements count.
var goProfileBlock = regexp.MustCompile(`^(.+):(\d+)\.\d+,(\d+)\.\d+ \d+ (\d+)$`)

// CapMutationRanges keeps at most limit lines, whole files in sorted order, and
// answers what it cut: the lines and the files. A limit of 0 or less keeps all.
func CapMutationRanges(r map[string][]unitkey.Range, limit int) (kept map[string][]unitkey.Range, cutLines int, cutFiles []string) {
	kept = map[string][]unitkey.Range{}
	total := 0
	for _, f := range slices.Sorted(maps.Keys(r)) {
		n := 0
		for _, x := range r[f] {
			n += x.End - x.Start + 1
		}
		if limit > 0 && total+n > limit {
			cutLines += n
			cutFiles = append(cutFiles, f)
			continue
		}
		total += n
		kept[f] = r[f]
	}
	return kept, cutLines, cutFiles
}
