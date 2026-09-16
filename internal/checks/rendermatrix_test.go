package checks

import (
	"strings"
	"testing"
)

func TestParseMatrixReadsTheCasesATemplateDeclares(t *testing.T) {
	m, err := ParseMatrix(`
parse = ["**/*.json", "**/*.toml"]

# a comment between the tables
[[case]]
name = "star"
answers = { project_name = "Gate Go", variant = "star", mcp = true }
present = [
  "go.mod",     # trailing comments are the matrix's own idiom
  "Dockerfile",
]
absent = [".forgejo"]

[[case]]
name = "library"
answers = { variant = "library", mcp = false, retries = 3 }
present = ["go.mod"]
`)
	if err != nil {
		t.Fatalf("ParseMatrix: %v", err)
	}
	if strings.Join(m.Parse, ",") != "**/*.json,**/*.toml" {
		t.Errorf("parse globs: %v", m.Parse)
	}
	if len(m.Cases) != 2 || m.Cases[0].Name != "star" || m.Cases[1].Name != "library" {
		t.Fatalf("cases: %+v", m.Cases)
	}
	if strings.Join(m.Cases[0].Present, ",") != "go.mod,Dockerfile" || strings.Join(m.Cases[0].Absent, ",") != ".forgejo" {
		t.Errorf("expectations: %+v", m.Cases[0])
	}
	if m.Cases[0].Answers["mcp"] != true || m.Cases[1].Answers["retries"] != int64(3) {
		t.Errorf("answers: %+v", m.Cases)
	}
	if m.Cases[1].Absent != nil {
		t.Errorf("a case declaring no absent has none: %+v", m.Cases[1].Absent)
	}
}

// A matrix that cannot be read is a usage error, never a verdict about the
// template: the gate did not run.
func TestParseMatrixRefusesAMatrixItCannotUse(t *testing.T) {
	for label, text := range map[string]string{
		"no case at all":      "parse = []\n",
		"a case with no name": "[[case]]\nanswers = { a = 1 }\n",
		"not toml":            "[[case]\nname =\n",
	} {
		if _, err := ParseMatrix(text); err == nil {
			t.Errorf("%s: parsed anyway", label)
		}
	}
}

func TestCopierArgvCarriesTheThreeLoadBearingFlags(t *testing.T) {
	argv, err := CopierArgv("/src", "/out/star", map[string]any{"variant": "star", "mcp": false, "retries": int64(2), "ratio": 1.5})
	if err != nil {
		t.Fatalf("CopierArgv: %v", err)
	}
	got := strings.Join(argv, " ")
	for _, want := range []string{
		"uvx --from copier==" + CopierVersion + " copier copy",
		"--trust", "--skip-tasks", "--vcs-ref=HEAD", "--defaults", "--quiet",
		// Answers in key order, each scalar as copier's own cast reads it.
		"--data mcp=false --data ratio=1.5 --data retries=2 --data variant=star",
		"/src /out/star",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("argv missing %q:\n%s", want, got)
		}
	}
	if argv[len(argv)-2] != "/src" || argv[len(argv)-1] != "/out/star" {
		t.Errorf("the template and the destination come last: %v", argv[len(argv)-2:])
	}
	if _, err := CopierArgv("/src", "/out/x", map[string]any{"a": []any{1}}); err == nil {
		t.Error("a non-scalar answer is refused: copier takes --data key=value")
	}
	if _, err := CopierArgv("/src", "/out/x", map[string]any{"a": true}); err != nil {
		t.Errorf("a bool answer is a scalar: %v", err)
	}
}

func TestRenderedPathProblemsReadsThePathsNotTheContent(t *testing.T) {
	if got := RenderedPathProblems(nil); len(got) != 1 || !strings.Contains(got[0], "rendered nothing") {
		t.Errorf("an empty tree is the first thing said: %v", got)
	}
	got := RenderedPathProblems([]string{
		"always.txt",
		"{% if flavor == 'a' %}only-a{% endif %}/marker.txt",
		"data.json.jinja",
		"src/{{ project }}.py",
		"README.md",
	})
	want := []string{
		"unresolved jinja in rendered PATH: {% if flavor == 'a' %}only-a{% endif %}/marker.txt",
		"unstripped .jinja suffix: data.json.jinja",
		"unresolved jinja in rendered PATH: src/{{ project }}.py",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v\nwant %v", got, want)
	}
	// A workflow's ${{ … }} is CONTENT, and content is deliberately not read:
	// every rendered workflow is full of it.
	if got := RenderedPathProblems([]string{".forgejo/workflows/ci.yml"}); len(got) != 0 {
		t.Errorf("a plain path is no problem: %v", got)
	}
}

func TestExpectationProblemsNameTheGlobAndTheFirstFiveHits(t *testing.T) {
	if got := PresentProblem("go.mod"); got != "expected PRESENT but missing: go.mod" {
		t.Errorf("present: %q", got)
	}
	got := AbsentProblem(".forgejo/**", []string{"f/g", "f/b", "f/a", "f/e", "f/d", "f/c"})
	if got != "expected ABSENT but rendered: .forgejo/** -> f/a, f/b, f/c, f/d, f/e" {
		t.Errorf("absent: %q", got)
	}
	if got := AbsentProblem("x", []string{"x"}); got != "expected ABSENT but rendered: x -> x" {
		t.Errorf("one hit: %q", got)
	}
}

func TestParseRenderedCatchesABornRedStamp(t *testing.T) {
	for _, tc := range []struct{ label, rel, body, says string }{
		{"valid json", "package.json", `{"name": "x"}`, ""},
		{"valid toml", "pyproject.toml", "[project]\nname = \"x\"\n", ""},
		{"an unquoted answer breaks the json", "package.json", `{"name": x}`, "package.json does not parse as json"},
		{"a broken toml", "pyproject.toml", "[project\nname =\n", "pyproject.toml does not parse as toml"},
		{"an empty json file", "a.json", "", "does not parse as json"},
		{"a suffix nobody parses", "always.txt", "hello", "no parser for `parse` match always.txt (known: .json, .toml)"},
		{"a suffixless match", "Makefile", "all:\n", "no parser for `parse` match Makefile"},
	} {
		got := ParseRendered(tc.rel, tc.body)
		if tc.says == "" && got != "" {
			t.Errorf("%s: %q", tc.label, got)
		}
		if tc.says != "" && !strings.Contains(got, tc.says) {
			t.Errorf("%s: %q, want %q", tc.label, got, tc.says)
		}
	}
}

func TestMatrixReportIsTheScriptsOwnReport(t *testing.T) {
	state, out := MatrixReport([]string{"a", "b"}, map[string][]string{})
	if state != 0 {
		t.Errorf("every case clean is state 0, got %d", state)
	}
	want := "render matrix: ci-matrix.toml  (2 case(s), copier " + CopierVersion + ")\n" +
		"::group::render a\n  OK — a\n::endgroup::\n" +
		"::group::render b\n  OK — b\n::endgroup::\n" +
		"render matrix OK — 2 case(s) clean\n"
	if out != want {
		t.Errorf("clean report:\n%q\nwant\n%q", out, want)
	}

	state, out = MatrixReport([]string{"a", "b"}, map[string][]string{"a": {"one", "two"}})
	if state != 1 {
		t.Errorf("a failed case is state 1, got %d", state)
	}
	for _, line := range []string{
		"::error::[a] one\n::error::[a] two\n::endgroup::\n",
		"::group::render b\n  OK — b\n",
		"::error::render matrix FAILED for: a\n",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("report missing %q:\n%s", line, out)
		}
	}
}

// The rendered tree is scanned by the fleet's own suppression scan: one
// definition, not a second copy of the rule.
func TestScanTreeReadsTheRenderedTreeWithTheFleetsScan(t *testing.T) {
	found, err := ScanTree(map[string]string{
		"src/app.py":     "x = 1  # " + sjNoqa + ": BLE001\n",
		"README.md":      "# " + sjNoqa + "\n",
		".claude/x.py":   "y = 2  # " + sjNoqa + "\n",
		"pkg/env.go":     "package m\nconst R = \"APP_REQUIRED\"\n",
		"pkg/x_test.go":  "package m\nfunc TestX(t *testing.T) { t." + sjSkip + "() }\n",
		"probes/p.py":    "p = Popen([x])  # " + sjNoqa + ": S603\n",
		"docs/quoted.py": "PATTERN = \"# " + sjNoqa + "\"\n",
	})
	if err != nil {
		t.Fatalf("ScanTree: %v", err)
	}
	var rels []string
	for _, f := range found {
		rels = append(rels, f.Rel)
	}
	// .claude/ is scanned here though the gate excludes it in a checkout: the
	// pour surface reaches every repo born from the template. probes/ is a
	// finding because no DirectoryExempt row can apply to a tree that belongs
	// to no repository. The armed skip and the quoted directive are neither.
	if strings.Join(rels, ",") != ".claude/x.py,probes/p.py,src/app.py" {
		t.Errorf("findings: %v", rels)
	}
	if found[2].Line != 1 || found[2].Tool != "ruff" || found[2].Form != "noqa" {
		t.Errorf("the finding carries its coordinates: %+v", found[2])
	}
	if got := SuppressionProblem(found[2]); !strings.HasPrefix(got, "src/app.py:1 ruff · noqa — a suppression in the POUR SURFACE reaches every repo born from this template: x = 1  # ") {
		t.Errorf("the pour-surface complaint: %q", got)
	}
	if _, err := ScanTree(map[string]string{"a.py": "x = (\n"}); err != nil {
		t.Errorf("a file python cannot tokenize is read by the fallback scanner, not a refusal: %v", err)
	}
}
