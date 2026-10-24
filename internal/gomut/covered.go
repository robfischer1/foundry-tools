package gomut

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/unitkey"
)

// THE SOURCE A CHANGED TEST RUNS joins the scope (checks/gocovered.go says why).
// Each package whose _test.go files the change touched runs ONLY the touched
// tests, under -coverpkg=./... so a test's reach into another package counts, and
// every source line that ran is mutated as though it had changed. A change with no
// test file in it runs nothing here.

// EnvCoveredCap bounds the lines a run's changed tests may add to its scope; the
// knob is every mutation atom's (checks.EnvMutationCoveredCap).
const EnvCoveredCap = checks.EnvMutationCoveredCap

// testCovered answers the source lines the change's tests execute, named from the
// repository root, and a line for the verdict saying what ran and what was cut.
func (m *module) testCovered(ctx context.Context, since, changed string, tags []string) (map[string][]unitkey.Range, string) {
	var tests []string
	for _, f := range strings.Fields(changed) {
		if strings.HasSuffix(f, "_test.go") {
			tests = append(tests, f)
		}
	}
	if len(tests) == 0 {
		return nil, ""
	}
	diff, code := m.run(ctx, atoms.Cmd{Dir: m.cwd, Name: "git", Args: append([]string{"diff", "--relative", "--unified=0", "--no-color", "--no-ext-diff", since, "HEAD", "--"}, tests...)})
	if code != 0 {
		return nil, id + ": changed tests: git could not diff them, so their coverage joined no scope: " + atoms.LastLine(diff)
	}
	gomod, _ := os.ReadFile(filepath.Join(m.cwd, "go.mod")) // the module's own; unread names no module and keeps nothing
	module := checks.GoModulePath(string(gomod))
	ranges := unitkey.ParseRanges(diff)
	byDir := map[string][]string{}
	for _, f := range tests {
		src, err := os.ReadFile(filepath.Join(m.cwd, f))
		if err != nil {
			continue // deleted: nothing of it runs
		}
		if names, ok := checks.GoChangedTests(src, ranges[f]); ok {
			byDir[path.Dir(f)] = append(byDir[path.Dir(f)], names...)
		}
	}
	covered := map[string][]unitkey.Range{}
	var notes []string
	for _, dir := range slices.Sorted(maps.Keys(byDir)) {
		if len(byDir[dir]) == 0 {
			continue
		}
		got, note := m.coverOf(ctx, dir, byDir[dir], module, tags)
		covered = checks.UnionMutationRanges(covered, got)
		if note != "" {
			notes = append(notes, note)
		}
	}
	kept, say := checks.CoveredScope(id, "they run", covered)
	root := map[string][]unitkey.Range{}
	for f, rs := range kept {
		root[path.Join(m.dir, f)] = rs
	}
	return root, strings.Join(append([]string{say}, notes...), "\n")
}

// coverOf runs one package's changed tests alone under coverage and answers the
// lines that ran, named from the module.
func (m *module) coverOf(ctx context.Context, dir string, names []string, module string, tags []string) (map[string][]unitkey.Range, string) {
	profile := filepath.Join(m.work, "testcov", slug(dir)+".out")
	if err := os.MkdirAll(filepath.Dir(profile), 0o755); err != nil {
		return nil, id + ": changed tests in " + dir + ": " + err.Error()
	}
	args := append([]string{"test", "-count=1", "-run", checks.GoTestRunPattern(names), "-coverpkg=./...", "-coverprofile", profile}, tags...)
	out, code := m.run(ctx, atoms.Cmd{Dir: m.cwd, Name: "go", Args: append(args, "./"+dir)})
	raw, err := os.ReadFile(profile)
	if err != nil {
		return nil, fmt.Sprintf("%s: changed tests in %s wrote no coverage (exit %d), so they added nothing: %s", id, dir, code, atoms.LastLine(out))
	}
	note := ""
	if code != 0 {
		note = fmt.Sprintf("%s: changed tests in %s exited %d; the coverage they wrote was read", id, dir, code)
	}
	return checks.GoCoveredRanges(string(raw), module), note
}

// plus is x with more ranges named from the root; a nil x is made.
func (x *extraScope) plus(root map[string][]unitkey.Range, dir string) *extraScope {
	if len(root) == 0 {
		return x
	}
	if x == nil {
		x = &extraScope{}
	}
	x.root = checks.UnionMutationRanges(x.root, root)
	x.mod = checks.ModuleMutationRanges(x.root, dir)
	return x
}
