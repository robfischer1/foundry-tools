package checks

import (
	"bufio"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/unitkey"
)

// WHAT A TEST IS, AND WHAT IT RAN, per language (testcovered.go says why). The
// predicates follow each runner's own conventions; the readers turn a coverage
// tool's report into ranges named from the directory the tool ran in.

// UnderAny says file is one of dirsOrFiles or lies under one of them.
func UnderAny(file string, dirsOrFiles []string) bool {
	for _, m := range dirsOrFiles {
		m = strings.TrimSuffix(path.Clean(m), "/")
		if file == m || m == "." || strings.HasPrefix(file, m+"/") {
			return true
		}
	}
	return false
}

// pathHas says one of p's directory components is dir.
func pathHas(p, dir string) bool {
	return slices.Contains(strings.Split(path.Dir(p), "/"), dir)
}

// PythonTestFile says p is a test by pytest's default conventions: a module
// named test_*.py or *_test.py, or any python file under a tests directory (a
// helper or a conftest there is the tests' own).
func PythonTestFile(p string) bool {
	if !strings.HasSuffix(p, ".py") {
		return false
	}
	base := path.Base(p)
	return strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") || pathHas(p, "tests")
}

// PythonTestModule says p is a module pytest collects by name; a test helper
// under tests/ is not one, and its directory is collected instead.
func PythonTestModule(p string) bool {
	base := path.Base(p)
	return (strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py")) || strings.HasSuffix(base, "_test.py")
}

// PythonCoveredRanges reads `coverage json` into the lines that ran, named from
// tree, the directory coverage ran in. coverage names a file under its working
// directory relatively and any other absolutely; an absolute name outside tree
// is not the tree's and is dropped.
func PythonCoveredRanges(report, tree string) (map[string][]unitkey.Range, error) {
	var r struct {
		Files map[string]struct {
			Executed []int `json:"executed_lines"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(report), &r); err != nil {
		return nil, fmt.Errorf("the coverage report did not parse: %w", err)
	}
	out := map[string][]unitkey.Range{}
	for name, f := range r.Files {
		if rel, ok := treeRelative(name, tree); ok && len(f.Executed) > 0 {
			out[rel] = LineRanges(f.Executed)
		}
	}
	return out, nil
}

// treeRelative names p from tree: a relative p already is; an absolute one must
// lie under tree.
func treeRelative(p, tree string) (string, bool) {
	if !path.IsAbs(p) {
		p = path.Clean(p)
		return p, p != ".." && !strings.HasPrefix(p, "../")
	}
	rel, ok := strings.CutPrefix(path.Clean(p), path.Clean(tree)+"/")
	return rel, ok
}

// tsTestName is a vitest/jest/bun test module by name; tsScript is any script
// dialect the lane grades.
var (
	tsTestName = regexp.MustCompile(`\.(test|spec)\.[cm]?[jt]sx?$`)
	tsScript   = regexp.MustCompile(`\.[cm]?[jt]sx?$`)
)

// TSTestFile says p is a test by the runners' conventions: *.test.* or *.spec.*
// in a script dialect, or a script under a __tests__ directory.
func TSTestFile(p string) bool {
	return tsTestName.MatchString(p) || (pathHas(p, "__tests__") && tsScript.MatchString(p))
}

// TSUndeclaredSource is a file the undeclared ts:mutation scope takes
// (TSMutationSpecs' empty declaration): TypeScript outside the tests, the tool
// configs and node_modules.
func TSUndeclaredSource(p string) bool {
	return (strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".tsx")) && !TSTestFile(p) &&
		!strings.HasSuffix(p, ".config.ts") && !strings.HasSuffix(p, ".d.ts") && !pathHas(p, "node_modules")
}

// LCOVRanges reads an lcov report into the lines that ran (a DA hit count above
// zero), named from tree. dir is where the tool ran, relative to tree: a
// relative SF is named from it.
func LCOVRanges(lcov, tree, dir string) map[string][]unitkey.Range {
	lines := map[string][]int{}
	file := ""
	sc := bufio.NewScanner(strings.NewReader(lcov))
	sc.Buffer(make([]byte, 0, 1<<16), 1<<24)
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if sf, ok := strings.CutPrefix(ln, "SF:"); ok {
			if !path.IsAbs(sf) {
				sf = path.Join(dir, sf)
			}
			rel, inTree := treeRelative(sf, tree)
			file = ""
			if inTree {
				file = rel
			}
			continue
		}
		da, ok := strings.CutPrefix(ln, "DA:")
		if !ok || file == "" {
			continue
		}
		n, hits, _ := strings.Cut(da, ",")
		hits, _, _ = strings.Cut(hits, ",")
		l, err := strconv.Atoi(n)
		h, herr := strconv.Atoi(hits)
		if err == nil && herr == nil && h > 0 {
			lines[file] = append(lines[file], l)
		}
	}
	out := map[string][]unitkey.Range{}
	for f, ls := range lines {
		out[f] = LineRanges(ls)
	}
	return out
}

// StrykerOwner is the config directory nearest above file, "" for none
// (PlanStryker's rule).
func StrykerOwner(file string, configDirs map[string]bool) string {
	for d := path.Dir(file); ; d = path.Dir(d) {
		if configDirs[d] {
			return d
		}
		if d == "." || d == "/" {
			return ""
		}
	}
}

// RustTestFile says p is a crate's integration test or bench: a rust file
// under a tests or benches directory.
func RustTestFile(p string) bool {
	return strings.HasSuffix(p, ".rs") && (pathHas(p, "tests") || pathHas(p, "benches"))
}

// rustCfgTest opens a test-only item: `#[cfg(test)]` alone on its line.
var rustCfgTest = regexp.MustCompile(`^\s*#\[cfg\(test\)\]\s*$`)

// RustTestModSpans answers the lines of every `#[cfg(test)]` item in src, from
// the attribute to the brace that closes the item. Braces are counted outside
// line comments, string literals and char literals; that is a scanner, not a
// parser, and a raw string holding an unbalanced brace ends a span early, which
// only narrows what reads as a test change.
func RustTestModSpans(src string) []unitkey.Range {
	lines := strings.Split(src, "\n")
	var out []unitkey.Range
	for i := 0; i < len(lines); i++ {
		if !rustCfgTest.MatchString(lines[i]) {
			continue
		}
		depth, opened := 0, false
		j := i + 1
		for ; j < len(lines); j++ {
			d, semi := rustBraces(lines[j])
			if d != 0 || strings.Contains(lines[j], "{") {
				opened = true
			}
			depth += d
			if (opened && depth <= 0) || (!opened && semi) {
				break
			}
		}
		end := min(j, len(lines)-1)
		out = append(out, unitkey.Range{Start: i + 1, End: end + 1})
		i = end
	}
	return out
}

// rustBraces is a line's brace balance outside comments and literals, and
// whether it ends an item with `;` before any brace (`mod tests;`).
func rustBraces(ln string) (int, bool) {
	depth, inStr := 0, false
	semi := false
	for k := 0; k < len(ln); k++ {
		c := ln[k]
		switch {
		case inStr && c == '\\':
			k++
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '/' && k+1 < len(ln) && ln[k+1] == '/':
			return depth, semi
		case c == '\'' && k+2 < len(ln) && ln[k+2] == '\'':
			k += 2
		case c == '{':
			depth++
		case c == '}':
			depth--
		case c == ';' && depth == 0:
			semi = true
		}
	}
	return depth, semi
}
