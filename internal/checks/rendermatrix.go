package checks

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// sweep:template-render-matrix — render every case a template declares and
// grade what comes out. Carried from foundry-stocks
// ci/lib/template_render_matrix.py, whose logic this is, message for message.
//
// WHY A TEMPLATE IS UNUSUALLY WORTH GATING: a template bug does not break the
// template. It propagates into every repo stamped afterward and surfaces
// later, in someone else's repo, where the cause is expensive to trace. The
// tauri variant (frontend-repo-template PR #42) widened three path
// conditionals vite also depends on; a fat-finger would have silently dropped
// index.html from every web stamp with nothing erroring until somebody stamped
// a web app.
//
// ONE GATE, FIVE TEMPLATES, AND THE VARIANCE IS DATA: each template owns a
// ci-matrix.toml declaring its own cases and expectations. Adding a variant is
// a data edit, never a gate edit.
//
// THREE COPIER FLAGS, ALL LOAD-BEARING (verified 2026-07-25 against copier
// 9.17.0 — re-verify before changing any of them):
//
//  1. --trust is MANDATORY. Without it copier REFUSES a template declaring
//     _tasks and writes NOTHING; it does not skip the tasks and carry on.
//  2. --skip-tasks renders real content and runs no task, which is what makes
//     this gate cheap and side-effect-free: no git init, no bun install, no
//     network beyond copier itself.
//  3. --vcs-ref=HEAD is not tidiness. Copier resolves a git template to its
//     NEWEST TAG by default, so without it the gate renders the last release
//     and reports green having never touched the change under check.
//
// WHAT IS NOT PORTED: the script's --matrix, --case and --keep flags, and its
// COPIER_VERSION env override. The atom is the only caller and passes none of
// them (the reusable workflow that did retired with the act-runner).
// renovate: datasource=pypi depName=copier
const CopierVersion = "9.17.0"

// MatrixCase is one declared render: the answers copier is given, and what the
// rendered tree must and must not contain.
type MatrixCase struct {
	Name    string
	Answers map[string]any
	Present []string
	Absent  []string
}

// Matrix is a template's ci-matrix.toml.
type Matrix struct {
	// Parse are globs whose matches must parse as their suffix declares.
	Parse []string
	Cases []MatrixCase
}

type matrixFile struct {
	Parse []string `toml:"parse"`
	Case  []struct {
		Name    string         `toml:"name"`
		Answers map[string]any `toml:"answers"`
		Present []string       `toml:"present"`
		Absent  []string       `toml:"absent"`
	} `toml:"case"`
}

// ParseMatrix reads a ci-matrix.toml. A matrix that will not parse, or that
// declares no case, is a usage error rather than a verdict about the template.
func ParseMatrix(text string) (Matrix, error) {
	var f matrixFile
	if _, err := toml.Decode(text, &f); err != nil {
		return Matrix{}, fmt.Errorf("ci-matrix.toml does not parse: %w", err)
	}
	if len(f.Case) == 0 {
		return Matrix{}, fmt.Errorf("ci-matrix.toml declares no [[case]]")
	}
	m := Matrix{Parse: f.Parse}
	for _, c := range f.Case {
		if c.Name == "" {
			return Matrix{}, fmt.Errorf("case is missing `name`")
		}
		m.Cases = append(m.Cases, MatrixCase{Name: c.Name, Answers: c.Answers, Present: c.Present, Absent: c.Absent})
	}
	return m, nil
}

// CopierArgv is the render command for one case. Answers are passed in key
// order so the same case always builds the same argv, and copier casts each
// value back through its question's declared type — `mcp=false` reaches a
// `type: bool` question as False (verified).
func CopierArgv(template, dest string, answers map[string]any) ([]string, error) {
	argv := []string{
		"uvx", "--from", "copier==" + CopierVersion, "copier", "copy",
		"--trust",        // (1) — without it copier refuses and writes nothing
		"--skip-tasks",   // (2) — content, no side effects
		"--vcs-ref=HEAD", // (3) — the tree under check, not the newest tag
		"--defaults",
		"--quiet",
	}
	for _, key := range sortedAnswerKeys(answers) {
		value, err := copierScalar(answers[key])
		if err != nil {
			return nil, err
		}
		argv = append(argv, "--data", key+"="+value)
	}
	return append(argv, template, dest), nil
}

func sortedAnswerKeys(answers map[string]any) []string {
	keys := make([]string, 0, len(answers))
	for k := range answers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// copierScalar renders a TOML scalar as copier expects it on --data.
//
// If-chains rather than a type switch: go cover does not mark a switch case's
// expression, so the mutation lane reads every one of them as NOT COVERED
// (foundry-tools#35).
func copierScalar(value any) (string, error) {
	if v, ok := value.(bool); ok && v {
		return "true", nil
	}
	if v, ok := value.(bool); ok && !v {
		return "false", nil
	}
	if v, ok := value.(string); ok {
		return v, nil
	}
	if v, ok := value.(int64); ok {
		return strconv.FormatInt(v, 10), nil
	}
	if v, ok := value.(float64); ok {
		return strconv.FormatFloat(v, 'g', -1, 64), nil
	}
	return "", fmt.Errorf("answer values must be scalars, got %T: %#v", value, value)
}

// badPathMarkers are the jinja delimiters a RENDERED PATH may never contain. A
// surviving one means a conditional path did not resolve.
//
// PATHS ONLY, DELIBERATELY. The same scan over file CONTENT was tried and
// rejected: it has real false positives (a .gitkeep documenting itself with a
// literal `{{ app_name }}`, and every rendered workflow full of `${{ … }}`).
// Paths have none, and a broken conditional shows up in the path first.
var badPathMarkers = []string{"{{", "{%", "}}", "%}"}

// RenderedPathProblems grades the rendered file list: a tree that is empty,
// a path carrying jinja, a suffix copier did not strip.
func RenderedPathProblems(rendered []string) []string {
	var problems []string
	if len(rendered) == 0 {
		problems = append(problems, "rendered nothing — copier reported success but the tree is empty")
	}
	for _, rel := range rendered {
		if marked(rel) {
			problems = append(problems, "unresolved jinja in rendered PATH: "+rel)
		} else if strings.HasSuffix(rel, ".jinja") {
			problems = append(problems, "unstripped .jinja suffix: "+rel)
		}
	}
	return problems
}

func marked(rel string) bool {
	for _, marker := range badPathMarkers {
		if strings.Contains(rel, marker) {
			return true
		}
	}
	return false
}

// PresentProblem is the complaint for a `present` glob that matched nothing.
func PresentProblem(pattern string) string {
	return "expected PRESENT but missing: " + pattern
}

// AbsentProblem is the complaint for an `absent` glob that matched, naming the
// first five hits.
func AbsentProblem(pattern string, hits []string) string {
	sort.Strings(hits)
	return fmt.Sprintf("expected ABSENT but rendered: %s -> %s", pattern, strings.Join(hits[:min(len(hits), 5)], ", "))
}

// matrixParsers are the suffixes a `parse` glob may match. An unknown suffix
// is an ERROR, not a silent skip: a rendered file nobody can parse is a file
// the gate did not check.
var matrixParsers = []string{".json", ".toml"}

// ParseRendered answers the complaint for one rendered file a `parse` glob
// matched, or "" when it parses. It catches born-red stamps: a long
// description or an unquoted answer that renders syntactically invalid
// JSON or TOML.
func ParseRendered(rel, body string) string {
	suffix := pathSuffix(rel)
	if suffix == ".json" {
		var into any
		if err := json.Unmarshal([]byte(body), &into); err != nil {
			return fmt.Sprintf("%s does not parse as json: %v", rel, err)
		}
		return ""
	}
	if suffix == ".toml" {
		var into any
		if _, err := toml.Decode(body, &into); err != nil {
			return fmt.Sprintf("%s does not parse as toml: %v", rel, err)
		}
		return ""
	}
	return fmt.Sprintf("no parser for `parse` match %s (known: %s)", rel, strings.Join(matrixParsers, ", "))
}

// SuppressionProblem is the complaint for a suppression in the pour surface.
//
// THE GAP THIS CLOSES. A template repo excludes template/ from its own
// pre-commit, so nothing in the pour surface is scanned there, and this matrix
// checked that the render PARSES rather than what it contains. A suppression
// written into a template therefore reached every repo poured from it, ungated,
// and first surfaced as a finding in somebody else's repo weeks later. That is
// not hypothetical: the 2026-08-16 sweep found 533 noqa on one rule, 368 of
// them eight decisions replicated into 46 repos by a scaffold pour.
//
// The RENDERED tree is the right place to look rather than template/: copier
// has already stripped .jinja and resolved the conditionals, so the files carry
// their real suffixes and the scan derives the real language.
func SuppressionProblem(f SJTreeFinding) string {
	return fmt.Sprintf("%s:%d %s · %s — a suppression in the POUR SURFACE reaches every repo born from this template: %s",
		f.Rel, f.Line, f.Tool, f.Form, f.Text)
}

// MatrixReport is the script's own report: a group per case, an ::error:: line
// per problem, and the roll-up. state is 0 when every case is clean.
func MatrixReport(cases []string, problems map[string][]string) (int, string) {
	var out strings.Builder
	fmt.Fprintf(&out, "render matrix: ci-matrix.toml  (%d case(s), copier %s)\n", len(cases), CopierVersion)
	var failed []string
	for _, name := range cases {
		fmt.Fprintf(&out, "::group::render %s\n", name)
		if found := problems[name]; len(found) > 0 {
			failed = append(failed, name)
			for _, problem := range found {
				fmt.Fprintf(&out, "::error::[%s] %s\n", name, problem)
			}
		} else {
			fmt.Fprintf(&out, "  OK — %s\n", name)
		}
		out.WriteString("::endgroup::\n")
	}
	if len(failed) > 0 {
		fmt.Fprintf(&out, "::error::render matrix FAILED for: %s\n", strings.Join(failed, ", "))
		return 1, out.String()
	}
	fmt.Fprintf(&out, "render matrix OK — %d case(s) clean\n", len(cases))
	return 0, out.String()
}
