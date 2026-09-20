package checks

import (
	"errors"
	"sort"
	"strings"
	"testing"
)

// THE CONTROL IS THE POINT, as it was in the script's golden test
// (foundry-stocks ci/lib/stop_justifications.test.sh). A scanner that reports
// nothing looks identical whether the tree is clean or the matcher is broken,
// so every planted directive has a clean control beside it: if the matcher
// stopped matching, the planted half goes red.
//
// Every directive below is assembled from fragments. Written whole, a tool
// that reads this file for its own directives would obey them.
const (
	sjNoqa            = "no" + "qa"
	sjTypeIg          = "type: ig" + "nore"
	sjNoCover         = "pragma: no co" + "ver"
	sjAllow           = "all" + "ow"
	sjIgnore          = "ig" + "nore"
	sjNolint          = "no" + "lint"
	sjSkip            = "Sk" + "ip"
	sjHado            = "hado" + "lint"
	sjAllowlistPragma = "pragma: allowlist sec" + "ret"
)

// sjTree scans an in-memory repository. tracked is the map's keys in git's
// order, which is byte order.
func sjTree(repo string, files map[string]string) (int, string) {
	tracked := make([]string, 0, len(files))
	for p := range files {
		tracked = append(tracked, p)
	}
	sort.Strings(tracked)
	return StopJustifications(SJInput{
		Tracked: tracked,
		Read:    func(rel string) (string, error) { return files[rel], nil },
		Repo:    repo,
		Today:   "2026-09-15",
	})
}

func wantSJ(t *testing.T, label string, want int, repo string, files map[string]string) string {
	t.Helper()
	got, out := sjTree(repo, files)
	if got != want {
		t.Errorf("%s: exit %d, want %d\n%s", label, got, want, out)
	}
	return out
}

func TestStopJustificationsCleanTreePassesAndCountsWhatItScanned(t *testing.T) {
	out := wantSJ(t, "clean tree", 0, "x", map[string]string{
		"a.py":      "def f():\n    return 1\n",
		"a.go":      "package main\nfunc main() {}\n",
		"README.md": "# " + sjNoqa + "\n",
	})
	if !strings.Contains(out, "stop-justifications: 2 file(s) scanned, no undocumented suppressions.") {
		t.Errorf("the clean line must count the two source files and not the README:\n%s", out)
	}
}

// One planted directive per language and tool, each of which MUST be caught.
func TestStopJustificationsCatchesAPlantedDirectiveInEveryLanguage(t *testing.T) {
	for _, tc := range []struct{ label, path, body, form string }{
		{"python ruff", "a.py", "x = 1  # " + sjNoqa + ": BLE001\n", "ruff · noqa"},
		{"python mypy", "a.py", "x = f()  # " + sjTypeIg + "\n", "mypy · type: ignore"},
		{"python coverage", "a.py", "def f():  # " + sjNoCover + "\n    pass\n", "coverage · pragma: no cover"},
		{"python pytest marker", "a.py", "@pytest.mark.sk" + "ip\ndef test_x(): pass\n", "pytest · skip/skipif/xfail marker"},
		{"python nosemgrep", "a.py", "p = Path(x)  # nosem" + "grep: rules.sast.x\n", "opengrep · nosemgrep"},
		{"python fmt", "a.py", "x = 1  # fmt: o" + "ff\n", "ruff-format · fmt off/on/skip"},
		{"python mypy file", "a.py", "# mypy: ig" + "nore-errors\n", "mypy · mypy: ignore-errors"},
		{"python pyright", "a.py", "x = 1  # pyright: ig" + "nore\n", "pyright · pyright: ignore"},
		{"python detect-secrets", "a.py", "x = 1  # " + sjAllowlistPragma + "\n", "detect-secrets · pragma: allowlist secret"},
		{"python bandit", "a.py", "x = 1  # no" + "sec\n", "bandit (legacy) · nosec"},
		{"python isort", "a.py", "import x  # isort: sk" + "ip\n", "isort · isort: skip"},
		{"python pylint", "a.py", "x = 1  # pylint: dis" + "able=x\n", "pylint · pylint: disable"},
		{"go nolint", "a.go", "package m\n//" + sjNolint + ":gosec\nfunc f() {}\n", "golangci-lint / staticcheck · nolint"},
		{"go lint ignore", "a.go", "package m\n//lint:ig" + "nore SA1 x\nfunc f() {}\n", "golangci-lint / staticcheck · lint:ignore"},
		{"go t.Skip", "a.go", "package m\nfunc TestX(t *testing.T) { t." + sjSkip + "(\"x\") }\n", "go test · t.Skip()"},
		{"go gosec", "a.go", "package m\nvar x = 1 // #no" + "sec G101\n", "gosec · nosec"},
		{"go nosemgrep", "a.go", "package m\nvar x = 1 // nosem" + "grep\n", "opengrep · nosemgrep"},
		{"rust allow", "a.rs", "#[" + sjAllow + "(dead_code)]\nfn f() {}\n", "rustc / clippy · allow(...)"},
		{"rust expect", "a.rs", "#![exp" + "ect(dead_code)]\n", "rustc / clippy · expect(...)"},
		{"rust ignore", "a.rs", "#[" + sjIgnore + "]\nfn t() {}\n", "cargo test · #[ignore]"},
		{"rust nosemgrep", "a.rs", "let x = 1; // nosem" + "grep\n", "opengrep · nosemgrep"},
		{"ts eslint", "a.ts", "// eslint-dis" + "able-next-line\nconst x = 1;\n", "eslint · eslint-disable"},
		{"ts ts-ignore", "a.ts", "// @ts-ig" + "nore\nconst x = 1;\n", "tsc · ts-ignore/expect-error/nocheck"},
		{"ts prettier", "a.ts", "// prettier-ig" + "nore\nconst x = 1;\n", "prettier · prettier-ignore"},
		{"ts vitest", "a.ts", "it.sk" + "ip(\"x\", () => {});\n", "vitest · skip/only/todo"},
		{"ts nosemgrep", "a.mjs", "const x = 1; // nosem" + "grep\n", "opengrep · nosemgrep"},
		{"ci continue-on-error", "w.yml", "jobs:\n  a:\n    continue-on-er" + "ror: true\n", "forgejo actions · continue-on-error"},
		{"ci if false", "w.yaml", "jobs:\n  a:\n    if: fal" + "se\n", "forgejo actions · if: false"},
		{"ci or true", "w.sh", "ruff check . |" + "| true\n", "shell · || true / || exit 0"},
		{"ci exit zero", "w.sh", "ruff check --exit-ze" + "ro .\n", "shell · --exit-zero"},
		{"dockerfile ignore", "Dockerfile", "FROM alpine:3\n# " + sjHado + " ignore=DL3018\nRUN apk add curl\n", "hadolint · hadolint ignore"},
		{"dockerfile global", "Dockerfile", "# " + sjHado + " global ignore=DL3003\nFROM alpine:3\n", "hadolint · hadolint ignore"},
		{"containerfile", "Containerfile", "FROM alpine:3\n# " + sjHado + " ignore=DL3018\n", "hadolint · hadolint ignore"},
		{"dockerfile variant", "bases/x/Dockerfile.prod", "FROM alpine:3\n# " + sjHado + " ignore=DL3018\n", "hadolint · hadolint ignore"},
		{"named dockerfile", "api.Dockerfile", "FROM alpine:3\n# " + sjHado + " ignore=DL3018\n", "hadolint · hadolint ignore"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			out := wantSJ(t, tc.label, 1, "x", map[string]string{tc.path: tc.body})
			if !strings.Contains(out, tc.form) {
				t.Errorf("the finding must name %q:\n%s", tc.form, out)
			}
		})
	}
}

// The report is the script's, byte for byte, because the gate's reader and
// nereus key on it.
func TestStopJustificationsPrintsFindingsGroupedByLanguageInTupleOrder(t *testing.T) {
	_, out := sjTree("x", map[string]string{
		"b.py": "x = 1  # " + sjNoqa + ": E501\ny = 2  # " + sjTypeIg + "\n",
		"a.go": "package m\nfunc TestX(t *testing.T) { t." + sjSkip + "() }\n",
	})
	want := "\nstop-justifications: 3 suppression(s) with no stated conflict.\n\n" +
		"  ── go " + strings.Repeat("─", 58) + "\n" +
		"    a.go:2\n        go test · t.Skip()\n        func TestX(t *testing.T) { t." + sjSkip + "() }\n" +
		"  ── python " + strings.Repeat("─", 54) + "\n" +
		"    b.py:1\n        ruff · noqa\n        x = 1  # " + sjNoqa + ": E501\n" +
		"    b.py:2\n        mypy · type: ignore\n        y = 2  # " + sjTypeIg + "\n" +
		"\n  A suppression is a claim the tool is wrong. Fix the finding, or —\n" +
		"  when two tools genuinely disagree — say so on the line and let the\n" +
		"  more permissive one yield:\n\n" +
		"      <code>  <directive>  # tool-conflict: <what disagrees, in prose>\n\n" +
		"  Prose, not a code: a code is copyable without thought, which is the\n" +
		"  failure this exists to stop.\n\n"
	if out != want {
		t.Errorf("report:\n%q\nwant:\n%q", out, want)
	}
}

// Findings on one line sort by tool, then form; a long line is cut at 110
// code points, not bytes.
func TestStopJustificationsSortsTiesAndCutsTheLineByCodePoint(t *testing.T) {
	long := "x = 'é" + strings.Repeat("a", 120) + "'  # " + sjTypeIg + "  # " + sjNoqa
	_, out := sjTree("x", map[string]string{"a.py": long + "\n"})
	i := strings.Index(out, "mypy · type: ignore")
	j := strings.Index(out, "ruff · noqa")
	if i < 0 || j < 0 || i > j {
		t.Fatalf("two findings on one line sort by tool:\n%s", out)
	}
	if !strings.Contains(out, "        "+runeCap(long, 110)+"\n") || strings.Contains(out, runeCap(long, 111)) {
		t.Errorf("the text is the first 110 code points:\n%s", out)
	}
}

func TestStopJustificationsReadsTheRegionNotTheSubstring(t *testing.T) {
	for _, tc := range []struct {
		label, path, body string
		want              int
	}{
		{"a directive quoted in a docstring is prose", "a.py", "\"\"\"Docs.\n\nWrite ``# " + sjNoqa + ": E501`` on the line.\n\"\"\"\n", 0},
		{"a directive in a string literal is data", "a.py", "PATTERN = \"# " + sjNoqa + "\"\nOTHER = \"# " + sjTypeIg + "\"\n", 0},
		{"a code form inside a shell string is not one", "c.sh", "#!/bin/sh\necho \"the old form was: grep x |" + "| true\"\n", 0},
		{"a real pytest skip is still caught", "a.py", "import pytest\npytest.sk" + "ip(\"dodge\")\n", 1},
		{"a real noqa is still caught", "a.py", "x = 1  # " + sjNoqa + ": E501\n", 1},
		{"a real or-true is still caught", "c.yml", "steps:\n  - run: make test |" + "| true\n", 1},
		{"a skip after a URL in a string is NOT excused", "e_test.go", "package p\n\nfunc TestA(t *testing.T) {\n\tif os.Getenv(\"D\") == \"http://x\" { t." + sjSkip + "(\"dodge\") }\n}\n", 1},
		{"a skip inside a real line comment is prose", "e.go", "package p\n\n// if x { t." + sjSkip + "(\"dodge\") }\nfunc A() {}\n", 0},
		{"a skip inside a block comment is prose", "e.go", "package p\n\n/*\nt." + sjSkip + "(\"explained, not filed\")\n*/\nfunc A() {}\n", 0},
		{"a URL fragment does not open a comment", "c.yml", "steps:\n  - run: curl http://x#frag && make t |" + "| true\n", 1},
		{"a code form named in a comment is prose", "x_test.go", "package main\n\n// moved behind the build tag, not a runtime t." + sjSkip + "()\nfunc TestX() {}\n", 0},
		{"a pragma inside a RUN string is data", "Dockerfile", "FROM alpine:3\nRUN echo \"# " + sjHado + " ignore=DL3018\"\n", 0},
		{"prose naming the linter is not a pragma", "Dockerfile", "FROM alpine:3\n# hadolint is the linter a pragma here would silence\nRUN echo hi\n", 0},
		{"a go build-ignore line can never count", "a.go", "//go:build " + sjIgnore + "\n\npackage m\n", 0},
		{"a t-string's literal is code to the script", "a.py", "x = t\"pytest.sk" + "ip(\"\n", 1},
		{"a python file tokenize refuses is read by the scanner", "a.py", "x = (\n  \"# " + sjNoqa + "\"\n", 0},
	} {
		t.Run(tc.label, func(t *testing.T) {
			wantSJ(t, tc.label, tc.want, "x", map[string]string{tc.path: tc.body})
		})
	}
}

func TestStopJustificationsMarkerAudit(t *testing.T) {
	two := "x = f()  # " + sjTypeIg + "  # tool-conflict: ruff TC006 wants the cast, mypy calls it redundant\n"
	out := wantSJ(t, "an unratified marker naming two tools does not excuse", 1, "x", map[string]string{"a.py": two})
	if !strings.Contains(out, "[UNRATIFIED: names mypy, ruff; no signoff on record] x = f()") {
		t.Errorf("the finding carries the verdict:\n%s", out)
	}
	if !strings.Contains(out, "stop-justifications: 1 tool-conflict marker(s) — 0 ratified, 1 NOT (counted as findings).") {
		t.Errorf("the audit line:\n%s", out)
	}

	prev := "# tool-conflict: ruff wants the narrow except, pyright cannot prove it exhaustive\nx = f()  # " + sjTypeIg + "\n"
	wantSJ(t, "a marker on the line above does not excuse either", 1, "x", map[string]string{"a.py": prev})

	bare := "x = f()  # " + sjTypeIg + "  # tool-conflict:\n"
	if out := wantSJ(t, "a bare marker is no marker", 1, "x", map[string]string{"a.py": bare}); strings.Contains(out, "marker(s)") {
		t.Errorf("tool-conflict: with no prose is not a marker at all:\n%s", out)
	}

	one := "p = f(u)  # " + sjNoqa + ": S310  # tool-conflict: ruff S310 fires and no code shape clears it\n"
	if out := wantSJ(t, "a one-tool marker", 1, "x", map[string]string{"a.py": one}); !strings.Contains(out, "[MALFORMED: names 1 tool(s): ruff — a conflict needs two]") {
		t.Errorf("one named tool is MALFORMED:\n%s", out)
	}

	df := "FROM alpine:3\n# " + sjHado + " ignore=DL3018  # tool-conflict: hadolint wants a pin\n"
	wantSJ(t, "a one-tool marker does not excuse a pragma", 1, "x", map[string]string{"Dockerfile": df})

	rat := "#[" + sjAllow + "(non_snake_case)]  // tool-conflict: rustc wants snake case, the COM trait names are fixed\nfn f() {}\n"
	if out := wantSJ(t, "the ratified grace row excuses", 0, "x", map[string]string{"apps/desktop/shell-verb/src/lib.rs": rat}); !strings.Contains(out, "1 ratified, 0 NOT") {
		t.Errorf("a ratified marker is counted as ratified:\n%s", out)
	}
	wantSJ(t, "the same line at another path is not ratified", 1, "x", map[string]string{"apps/other/lib.rs": rat})

	doc := "\"\"\"\nx = 1  # " + sjNoqa + "  # tool-conflict: ruff and mypy\n\"\"\"\n"
	if out := wantSJ(t, "a marker in a docstring", 0, "x", map[string]string{"a.py": doc}); !strings.Contains(out, "1 tool-conflict marker(s) — 0 ratified, 1 NOT") {
		t.Errorf("the audit reads each line alone, so a docstring marker is still counted:\n%s", out)
	}
}

func TestMarkerVerdictNamesTheToolsTheProseNames(t *testing.T) {
	for _, tc := range []struct{ prose, verdict, detail string }{
		{"tool-conflict: ruff wants it, mypy does not", "unratified", "names mypy, ruff; no signoff on record"},
		{"tool-conflict: Coverage.py and COVERAGE", "malformed", "names 1 tool(s): coverage — a conflict needs two"},
		{"tool-conflict: semgrep and opengrep are one tool", "malformed", "names 1 tool(s): opengrep — a conflict needs two"},
		{"tool-conflict: ruff-mypy is one hyphenated word", "malformed", "names 0 tool(s): none — a conflict needs two"},
		{"tool-conflict: pre-ruff and mypy_x", "malformed", "names 0 tool(s): none — a conflict needs two"},
		{"tool-conflict: go vet, then golangci-lint", "unratified", "names go vet, golangci-lint; no signoff on record"},
		{"tool-conflict: typescript versus tsc versus eslint", "unratified", "names eslint, tsc; no signoff on record"},
		{"tool-conflict: éruff and mypy", "malformed", "names 1 tool(s): mypy — a conflict needs two"},
		{"tool-conflict: ruff mypy", "unratified", "names mypy, ruff; no signoff on record"},
	} {
		v, d := MarkerVerdict("a.py", "x = 1  # "+tc.prose, "")
		if v != tc.verdict || d != tc.detail {
			t.Errorf("%q: (%s, %s), want (%s, %s)", tc.prose, v, d, tc.verdict, tc.detail)
		}
	}
	// The marker on the line above is read when the line carries none, and only
	// the prose AFTER the marker names tools.
	if v, d := MarkerVerdict("a.py", "x = 1  # mypy ruff", "# tool-conflict: pyright wants it, ruff does not"); v != "unratified" || d != "names pyright, ruff; no signoff on record" {
		t.Errorf("the previous line's marker: (%s, %s)", v, d)
	}
	// Ratification reads the MARKER's text: the rule must be named where the
	// marker is, not merely on the line it guards.
	if v, _ := MarkerVerdict("apps/desktop/shell-verb/src/lib.rs", "#["+sjAllow+"(x)]", "// tool-conflict: non_snake_case, nothing named"); v != "ratified" {
		t.Errorf("the ratified rule on the marker line above: %s", v)
	}
	if v, _ := MarkerVerdict("apps/desktop/shell-verb/src/lib.rs", "#["+sjAllow+"(non_snake_case)]", "// tool-conflict: nothing named"); v != "malformed" {
		t.Errorf("the rule on the guarded line does not ratify a marker above it: %s", v)
	}
}

// Rob's cerberus grant, scoped three ways. Each case removes exactly one axis
// and expects the finding back.
func TestStopJustificationsDirectoryExemption(t *testing.T) {
	drive := "import subprocess\np = subprocess.Popen([x])  # " + sjNoqa + ": S603\n"
	out := wantSJ(t, "cerberus probes/ S603 is excused", 0, "cerberus", map[string]string{"probes/seam/drive.py": drive})
	if !strings.Contains(out, "\nstop-justifications: 1 suppression(s) excused by a DIRECTORY exemption in cerberus.\n  probes/seam/drive.py:2 · S603 — local, dev-only tooling that tests a 3P harness\n") {
		t.Errorf("an excuse is printed, never silent:\n%s", out)
	}
	wantSJ(t, "probe/ singular", 0, "cerberus", map[string]string{"probe/f4/drive.py": drive})
	wantSJ(t, "another repository", 1, "notcerberus", map[string]string{"probes/seam/drive.py": drive})
	wantSJ(t, "no repository known", 1, "", map[string]string{"probes/seam/drive.py": drive})
	wantSJ(t, "outside the prefix", 1, "cerberus", map[string]string{"src/app.py": drive})
	wantSJ(t, "a second rule rides in", 1, "cerberus", map[string]string{"probes/drive.py": "p = Popen([x])  # " + sjNoqa + ": S603, E501\n"})
	wantSJ(t, "a bare suppression names nothing", 1, "cerberus", map[string]string{"probes/drive.py": "p = Popen([x])  # " + sjNoqa + "\n"})
	wantSJ(t, "a different rule", 1, "cerberus", map[string]string{"probes/other.py": "x = 1  # " + sjNoqa + ": BLE001\n"})
	blade := "FROM x\n# " + sjHado + " ignore=DL3002\n"
	if out := wantSJ(t, "the temporary grant prints its expiry", 0, "foundry-stocks", map[string]string{"bases/blade-go/Dockerfile": blade}); !strings.Contains(out, "DL3002 — the entrypoint runusers the gate and the work; non-root cannot — EXPIRES 2026-10-20") {
		t.Errorf("the expiry rides on the excuse:\n%s", out)
	}
}

// The grant's DATE is the rule, so it is tested with the date pinned — never
// through the real clock, which would red this suite the day after the grant ends.
func TestDirectoryExemptionHonoursTheExpiryAndNothingWider(t *testing.T) {
	pragma := "# " + sjHado + " ignore=DL3002"
	for _, tc := range []struct {
		label, repo, rel, line, today string
		excused                       bool
	}{
		{"a blade base on its last day", "foundry-stocks", "bases/blade-go/Dockerfile", pragma, "2026-10-20", true},
		{"and nothing the day after", "foundry-stocks", "bases/blade-go/Dockerfile", pragma, "2026-10-21", false},
		{"chairman carries the same row", "foundry-stocks", "bases/chairman/Dockerfile", pragma, "2026-09-13", true},
		{"a second rule", "foundry-stocks", "bases/blade-go/Dockerfile", pragma + ",DL3008", "2026-09-13", false},
		{"the same rule twice is one rule", "foundry-stocks", "bases/blade-go/Dockerfile", pragma + ", DL3002", "2026-09-13", true},
		{"a different rule", "foundry-stocks", "bases/blade-go/Dockerfile", "# " + sjHado + " ignore=DL3008", "2026-09-13", false},
		{"another repository", "frontend-ci-image", "bases/blade-go/Dockerfile", pragma, "2026-09-13", false},
		{"a base outside the grant", "foundry-stocks", "bases/frontend-next/Dockerfile", pragma, "2026-09-13", false},
		{"an undated row never expires", "cerberus", "probes/drive.py", "p = Popen([x])  # " + sjNoqa + ": S603", "9999-12-31", true},
		// Rob's wrecksys DL3026 grant, 2026-09-15.
		{"wrecksys: DL3026 under docker/", "wrecksys", "docker/jupyter/Dockerfile", "# " + sjHado + " ignore=DL3026", "2026-09-15", true},
		{"wrecksys: under src/wrecksys_one/, and it does not expire", "wrecksys", "src/wrecksys_one/Dockerfile", "# " + sjHado + " ignore=DL3026", "9999-12-31", true},
		{"wrecksys: outside the two directories", "wrecksys", "graveyard/Dockerfile", "# " + sjHado + " ignore=DL3026", "2026-09-15", false},
		{"wrecksys: a second rule may not ride in", "wrecksys", "docker/jupyter/Dockerfile", "# " + sjHado + " ignore=DL3026,DL3007", "2026-09-15", false},
		{"wrecksys: the other findings are not excused", "wrecksys", "docker/jupyter/Dockerfile", "# " + sjHado + " ignore=DL3013", "2026-09-15", false},
		{"wrecksys: not a fleet-wide DL3026 grant", "theia", "docker/Dockerfile", "# " + sjHado + " ignore=DL3026", "2026-09-15", false},
	} {
		if got := DirectoryExemption(tc.repo, tc.rel, tc.line, tc.today) != nil; got != tc.excused {
			t.Errorf("%s: excused=%v, want %v", tc.label, got, tc.excused)
		}
	}
}

func TestSuppressedCodesReadsTheFirstSuppressionOnTheLine(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []string
	}{
		{"x  # " + sjNoqa + ": S603, E501", []string{"S603", "E501"}},
		{"x  # " + sjNoqa, nil},
		{"//" + sjNolint + "(gosec)", []string{"gosec"}},
		{"#[" + sjAllow + "(dead_code)]", []string{"dead_code"}},
		{"#[exp" + "ect( a , b )]", []string{"a", "b"}},
		{"# " + sjHado + " global ignore=DL3003,SC2016", []string{"DL3003", "SC2016"}},
		{"# " + sjNoqa + ": ,", nil},
		{"plain text", nil},
	} {
		got := SuppressedCodes(tc.line)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%q: %v, want %v", tc.line, got, tc.want)
		}
	}
}

// THE SKIP-VS-FAIL CONTRACT: an executable branch excuses a skip; prose does not.
func TestStopJustificationsSkipVsFailContract(t *testing.T) {
	armed := "import os\nREQUIRED_ENV = \"APP_DB_REQUIRED\"\nif os.environ.get(REQUIRED_ENV):\n    raise SystemExit(1)\npytest.sk" + "ip(\"no db\")\n"
	out := wantSJ(t, "an armed python skip is a contract", 0, "x", map[string]string{"a.py": armed})
	if !strings.Contains(out, "\nstop-justifications: 1 skip-vs-fail contract(s) — excused, armed by 1 required-mode variable(s).\n  APP_DB_REQUIRED\n    a.py:5 · pytest.skip()\n") {
		t.Errorf("the contract is printed with its variable:\n%s", out)
	}
	wantSJ(t, "a required-mode name in prose arms nothing", 1, "x", map[string]string{"a.py": "# arm it with APP_DB_REQUIRED\npytest.sk" + "ip(\"no db\")\n"})
	wantSJ(t, "a required-mode variable cannot excuse a non-skip", 1, "x", map[string]string{"a.py": "X_REQUIRED = \"1\"\np = 1  # " + sjNoqa + ": BLE001\n"})
	wantSJ(t, "a sibling file in the same package arms the skip", 0, "x", map[string]string{
		"pkg/env.go":    "package m\n\nconst RequiredEnv = \"APP_GOLDENS_REQUIRED\"\n",
		"pkg/x_test.go": "package m\nfunc TestX(t *testing.T) { t." + sjSkip + "(\"no goldens\") }\n",
	})
	wantSJ(t, "another package does not arm it", 1, "x", map[string]string{
		"other/env.go": "package other\n\nconst RequiredEnv = \"APP_GOLDENS_REQUIRED\"\n",
		"x_test.go":    "package m\nfunc TestX(t *testing.T) { t." + sjSkip + "(\"no goldens\") }\n",
	})
	wantSJ(t, "a deeper package does not arm it", 1, "x", map[string]string{
		"pkg/sub/env.go": "package sub\n\nconst RequiredEnv = \"APP_GOLDENS_REQUIRED\"\n",
		"pkg/x_test.go":  "package m\nfunc TestX(t *testing.T) { t." + sjSkip + "(\"no goldens\") }\n",
	})
	wantSJ(t, "a non-go sibling does not arm a go skip", 1, "x", map[string]string{
		"pkg/env.py":    "X = \"APP_GOLDENS_REQUIRED\"\n",
		"pkg/x_test.go": "package m\nfunc TestX(t *testing.T) { t." + sjSkip + "(\"no goldens\") }\n",
	})
	wantSJ(t, "python does not widen to siblings", 1, "x", map[string]string{
		"pkg/env.py":    "X = \"APP_DB_REQUIRED\"\n",
		"pkg/test_x.py": "pytest.sk" + "ip(\"no db\")\n",
	})
	out = wantSJ(t, "two variables, the smallest names the contract", 0, "x", map[string]string{
		"a.go": "package m\nconst A = \"ZED_REQUIRED\"\nconst B = \"ALPHA_REQUIRED\"\nfunc TestX(t *testing.T) { t." + sjSkip + "() }\nfunc TestY(t *testing.T) { t." + sjSkip + "() }\n",
		"b.go": "package m\nconst C = \"BETA_REQUIRED\"\nfunc TestZ(t *testing.T) { t." + sjSkip + "() }\n",
	})
	if !strings.Contains(out, "3 skip-vs-fail contract(s) — excused, armed by 2 required-mode variable(s).\n  ALPHA_REQUIRED\n    a.go:4 · t.Skip()\n    a.go:5 · t.Skip()\n  BETA_REQUIRED\n    b.go:3 · t.Skip()\n") {
		t.Errorf("contracts group by variable, sorted:\n%s", out)
	}
}

// A sibling that will not read refuses the scan with the words the script used
// for it — and no later: the refusal is not traded for a verdict on the file
// that asked.
func TestStopJustificationsRefusesOnAnUnreadableSibling(t *testing.T) {
	files := map[string]string{
		"pkg/a_test.go": "package m\nfunc TestX(t *testing.T) { t." + sjSkip + "() }\n",
		"pkg/b.go":      "package m\nconst R = \"APP_REQUIRED\"\n",
		"pkg/z.go":      "package m\n",
	}
	got, out := StopJustifications(SJInput{
		Tracked: []string{"pkg/a_test.go", "pkg/b.go", "pkg/z.go"},
		Read: func(rel string) (string, error) {
			if rel == "pkg/z.go" {
				return "", errors.New("gone")
			}
			return files[rel], nil
		},
	})
	if got != 2 || out != "stop-justifications: CANNOT RUN — could not read pkg/z.go: gone\n" {
		t.Errorf("exit %d\n%q", got, out)
	}
}

// THE FLEET'S EXCLUDE, NOT THE REPOSITORY'S (GateExclude, Rob 2026-09-11: a
// repository has no say in anything that runs). The script read each repo's
// own pre-commit `exclude:`; a repository that widens its own is still scanned.
func TestStopJustificationsHonoursTheFleetsExcludeAndNoRepositorys(t *testing.T) {
	out := wantSJ(t, "suppressions in the trees the fleet excludes", 0, "x", map[string]string{
		".specify/scripts/vendored.py": "x = 1  # " + sjNoqa + ": BLE001\n",
		"a/vendor/b.go":                "//" + sjNolint + "\n",
		"web/node_modules/c.ts":        "// @ts-ig" + "nore\n",
		"src/own.py":                   "def f():\n    return 1\n",
	})
	if !strings.Contains(out, "stop-justifications: skipped 3 file(s) the fleet excludes (vendored and tool trees) — they cannot fail anything.\n") {
		t.Errorf("the exclusion is reported:\n%s", out)
	}
	wantSJ(t, "the same suppression in owned code", 1, "x", map[string]string{"src/own.py": "y = 2  # " + sjNoqa + ": BLE001\n"})
	wantSJ(t, "a repository's own exclude narrows nothing", 1, "x", map[string]string{
		".pre-commit-config.yaml": "exclude: '^src/'\n",
		"src/own.py":              "y = 2  # " + sjNoqa + ": BLE001\n",
	})
}

// SJReads is what the atom fetches before the scan runs, so it must cover
// every read the scan makes — and nothing the scan skips.
func TestSJReadsCoversEveryReadTheScanMakes(t *testing.T) {
	files := map[string]string{
		"README.md":                     "x",
		"ci/lib/stop_justifications.py": "x",
		"vendor/a.py":                   "x",
		"a.py":                          "x = 1\n",
		"pkg/a_test.go":                 "package m\nfunc TestX(t *testing.T) { t." + sjSkip + "() }\n",
		"pkg/b.go":                      "package m\n",
		"pyproject.toml":                "[project]\n",
		"sub/ruff.toml":                 "",
		"Dockerfile":                    "FROM x\n",
	}
	tracked := make([]string, 0, len(files))
	for p := range files {
		tracked = append(tracked, p)
	}
	sort.Strings(tracked)
	want := SJReads(tracked)
	if strings.Join(want, ",") != "Dockerfile,a.py,pkg/a_test.go,pkg/b.go,pyproject.toml,sub/ruff.toml" {
		t.Errorf("SJReads = %v", want)
	}
	fetched := map[string]bool{}
	for _, p := range want {
		fetched[p] = true
	}
	StopJustifications(SJInput{Tracked: tracked, Read: func(rel string) (string, error) {
		if !fetched[rel] {
			t.Errorf("the scan read %s, which SJReads did not name", rel)
		}
		return files[rel], nil
	}})
}

func TestRepoFromOriginAndSplitNul(t *testing.T) {
	for url, want := range map[string]string{
		"http://ourea.default.svc.cluster.local:8215/cerberus.git\n": "cerberus",
		"git@forgejo.notusmi.com:rob/cerberus.git":                   "cerberus",
		"git@host:cerberus":                 "cerberus",
		"https://host/rob/iris/":            "iris",
		"/home/rob/Forge/Outputs/tongs.git": "tongs",
		"":                                  "",
	} {
		if got := RepoFromOrigin(url); got != want {
			t.Errorf("RepoFromOrigin(%q) = %q, want %q", url, got, want)
		}
	}
	if got := SplitNul("a.py\x00b c.go\x00\x00"); strings.Join(got, "|") != "a.py|b c.go" {
		t.Errorf("SplitNul = %q", got)
	}
	if got := SplitNul(""); got != nil {
		t.Errorf("SplitNul of nothing = %q", got)
	}
}

// The listing `git ls-files` answers when it is NOT asked for -z: one path per
// line, quoted only where the path needs it.
func TestSplitGitPathsReadsTheQuotedFormGitActuallyEmits(t *testing.T) {
	got, err := SplitGitPaths("a.py\nsrc/b c.go\n\n")
	if err != nil {
		t.Fatalf("plain listing: %v", err)
	}
	if strings.Join(got, "|") != "a.py|src/b c.go" {
		t.Errorf("plain listing = %q", got)
	}
	if got, err := SplitGitPaths(""); err != nil || got != nil {
		t.Errorf("empty listing = %q, %v", got, err)
	}

	// THE BYTE CASE, which is why this is not strconv.Unquote: git escapes a
	// non-ASCII path one octal per UTF-8 BYTE, so decoding per rune answers
	// mojibake. "caf\303\251.py" is café.py and nothing else.
	got, err = SplitGitPaths("\"caf\\303\\251.py\"\n")
	if err != nil {
		t.Fatalf("octal path: %v", err)
	}
	if len(got) != 1 || got[0] != "café.py" {
		t.Errorf("octal path = %q, want café.py", got)
	}

	// A NEWLINE IN A PATH IS THE REASON -z EXISTED, and the quoted form carries
	// it without spanning two lines — so the record separator still holds.
	got, err = SplitGitPaths("\"odd\\nname.py\"\nplain.py\n")
	if err != nil {
		t.Fatalf("newline path: %v", err)
	}
	if len(got) != 2 || got[0] != "odd\nname.py" || got[1] != "plain.py" {
		t.Errorf("newline path = %q", got)
	}

	// Quotes, backslashes and tabs round-trip.
	got, err = SplitGitPaths("\"say \\\"hi\\\"\\t\\\\x.py\"\n")
	if err != nil {
		t.Fatalf("escaped path: %v", err)
	}
	if len(got) != 1 || got[0] != "say \"hi\"\t\\x.py" {
		t.Errorf("escaped path = %q", got)
	}

	// A LISTING THIS PARSER CANNOT READ IS AN ERROR, never a silently short
	// file list: the atom turns it into a could-not-run rather than a clean scan.
	if _, err := SplitGitPaths("\"bad\\q.py\"\n"); err == nil {
		t.Error("an unknown escape must not parse")
	}
	if _, err := SplitGitPaths("\"trailing\\\"\n"); err == nil {
		t.Error("a path ending in a backslash must not parse")
	}
}

func TestStopJustificationsSkipsItsOwnDefinitionAndSaysSo(t *testing.T) {
	out := wantSJ(t, "the script names every directive", 0, "foundry-stocks", map[string]string{
		"ci/lib/stop_justifications.py":      "x = 1  # " + sjNoqa + "\n",
		"ci/lib/stop_justifications.test.sh": "a |" + "| true\n",
	})
	if !strings.Contains(out, "stop-justifications: skipped its own definition (ci/lib/stop_justifications.py, ci/lib/stop_justifications.test.sh) — it names every directive it forbids.\n") {
		t.Errorf("the self-exclusion is reported:\n%s", out)
	}
}

// A tracked file that will not read is a refusal to report success.
func TestStopJustificationsCannotRunOnAnUnreadableFile(t *testing.T) {
	for _, path := range []string{"a.py", "pyproject.toml"} {
		got, out := StopJustifications(SJInput{
			Tracked: []string{path},
			Read:    func(string) (string, error) { return "", errors.New("blob missing") },
		})
		if got != 2 || out != "stop-justifications: CANNOT RUN — could not read "+path+": blob missing\n" {
			t.Errorf("%s: exit %d\n%q", path, got, out)
		}
	}
	got, _ := StopJustifications(SJInput{
		Tracked: []string{"README.md", "vendor/a.py", "ci/lib/stop_justifications.py"},
		Read:    func(string) (string, error) { return "", errors.New("never asked") },
	})
	if got != 0 {
		t.Errorf("a file the scan never reads cannot refuse it: exit %d", got)
	}
}

func TestStopJustificationsYAMLPragmas(t *testing.T) {
	idiom := "jobs:\n  b:\n    secrets: inherit  # " + sjAllowlistPragma + "\n"
	out := wantSJ(t, "the secrets-inherit idiom", 0, "x", map[string]string{"k.yml": idiom})
	if !strings.Contains(out, "\nstop-justifications: 1 detect-secrets pragma(s) in YAML/shell are a KNOWN IDIOM — ruled, exempt, nothing to do.\n  `secrets: inherit` passes") || strings.Contains(out, "ADVISORY") {
		t.Errorf("the idiom is known, not advisory:\n%s", out)
	}
	if out := wantSJ(t, "the commented idiom", 0, "x", map[string]string{"c.yaml": "jobs:\n  b:\n    # secrets: inherit  # " + sjAllowlistPragma + "\n"}); !strings.Contains(out, "KNOWN IDIOM") {
		t.Errorf("a commented idiom is the idiom:\n%s", out)
	}
	other := "jobs:\n  a:\n    env:\n      TOKEN: abc123  # " + sjAllowlistPragma + "\n"
	out = wantSJ(t, "a non-idiom pragma", 0, "x", map[string]string{"w.yml": other, "k.yml": idiom, "s.sh": "T=x  # " + sjAllowlistPragma + "\n", "Dockerfile": "# " + sjAllowlistPragma + "\n"})
	if !strings.Contains(out, "1 detect-secrets pragma(s) in YAML/shell are a KNOWN") || !strings.Contains(out, "\nstop-justifications: 2 detect-secrets pragma(s) in YAML/shell — ADVISORY, not counted as findings.\n  These are NOT") {
		t.Errorf("mixed: one known, two advisory, the Dockerfile not counted:\n%s", out)
	}
}

func TestStopJustificationsConfigSilencing(t *testing.T) {
	pfi := "[tool.ruff.lint.per-file-ignores]\n"
	for _, tc := range []struct {
		label, path, body string
		want              int
		says              string
	}{
		{"the poured tests entry", "pyproject.toml", pfi + "\"tests/**\" = [\"S\", \"PLR2004\", \"SLF001\"]\n", 1, "    pyproject.toml:2\n        ruff · per-file-ignores\n        \"tests/**\" silences S, PLR2004, SLF001\n"},
		{"a single-file carve-out", "ruff.toml", "[per-file-ignores]\n\"src/x/store.py\" = [\"S608\"]\n", 1, "\"src/x/store.py\" silences S608"},
		{"the lint section spelling", "ruff.toml", "[lint.per-file-ignores]\n\"src/x.py\" = [\"S608\"]\n", 1, "\"src/x.py\" silences S608"},
		{"INP001 alone is the idiom", "pyproject.toml", pfi + "\"scripts/**\" = [\"INP001\"]\n\"probe/**\" = [\"INP001\"]\n", 0, "\nstop-justifications: 2 per-file-ignores entry(s) are a KNOWN IDIOM — ruled, exempt, nothing to do.\n"},
		{"a rule riding in on INP001", "pyproject.toml", pfi + "\"tools/**\" = [\"INP001\", \"S608\"]\n", 1, "\"tools/**\" silences INP001, S608"},
		{"a project-wide lint.ignore", "pyproject.toml", "[tool.ruff.lint]\nignore = [\"COM\", \"ISC\", \"E501\"]\n", 0, "0 file(s) scanned"},
		{"mypy ignore_errors", "pyproject.toml", "[[tool.mypy.overrides]]\nmodule = \"x.y\"\nignore_errors = true\n", 1, "    pyproject.toml:3\n        mypy · ignore_errors\n        ignore_errors = true\n"},
		{"mypy disable_error_code", "pyproject.toml", "[[tool.mypy.overrides]]\n  disable_error_code = [\"x\"]\n", 1, "mypy · disable_error_code"},
		{"mypy outside an override", "pyproject.toml", "[tool.mypy]\nignore_errors = true\n", 0, ""},
		{"ignore_missing_imports", "pyproject.toml", "[[tool.mypy.overrides]]\nmodule = \"thirdparty.*\"\nignore_missing_imports = true\n", 0, ""},
		{"a clean pyproject", "pyproject.toml", "[project]\nname = \"x\"\n\n[tool.ruff.lint]\nselect = [\"ALL\"]\n", 0, ""},
		{"the ruled test set on a test glob", "pyproject.toml", pfi + "\"tests/**\" = [\"S101\", \"INP001\", \"ANN\", \"D\", \"E402\", \"PLW1510\"]\n\"conftest.py\" = [\"D\"]\n", 0, "2 per-file-ignores entry(s)"},
		{"the same rules on src", "pyproject.toml", pfi + "\"src/**\" = [\"ANN\", \"D\"]\n", 1, "\"src/**\" silences ANN, D"},
		{"a word containing test is not a test glob", "pyproject.toml", pfi + "\"contest/**\" = [\"D\"]\n", 1, "\"contest/**\" silences D"},
		{"blanket S on tests", "pyproject.toml", pfi + "\"tests/**\" = [\"S\"]\n", 1, ""},
		{"PLR2004 on a test glob", "pyproject.toml", pfi + "\"tests/**\" = [\"S101\", \"INP001\", \"PLR2004\"]\n", 1, ""},
		{"an empty rule list is not the idiom", "pyproject.toml", pfi + "\"tests/**\" = [ ]\n", 1, "\"tests/**\" silences \n"},
		{"a section change ends the table", "pyproject.toml", pfi + "[tool.other]\n\"tests/**\" = [\"S\"]\n", 0, ""},
		{"a non-entry line inside the table", "pyproject.toml", pfi + "# comment\n\"a\" = \"b\"\n", 0, ""},
		{"a nested config file is still read", "sub/ruff.toml", "[per-file-ignores]\n\"x.py\" = [\"E501\"]\n", 1, "sub/ruff.toml:2"},
		{"a single-quoted rule is not the idiom", "pyproject.toml", pfi + "\"scripts/**\" = ['INP001']\n", 1, "silences 'INP001'"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			out := wantSJ(t, tc.label, tc.want, "x", map[string]string{tc.path: tc.body})
			if !strings.Contains(out, tc.says) {
				t.Errorf("want %q in:\n%s", tc.says, out)
			}
		})
	}
	// Config findings alone exit 1 with no findings block after them.
	_, out := sjTree("x", map[string]string{"pyproject.toml": pfi + "\"a\" = [\"E501\"]\n", "b.py": "x = 1\n"})
	if strings.Contains(out, "no stated conflict") || strings.Contains(out, "file(s) scanned") {
		t.Errorf("a config-only failure prints no findings block and no clean line:\n%s", out)
	}
	if !strings.HasSuffix(out, "tool-conflict on one.\n\n") {
		t.Errorf("the config block ends the report:\n%s", out)
	}
}

func TestStopJustificationsConfigRowsSortAcrossFiles(t *testing.T) {
	_, out := sjTree("x", map[string]string{
		"b/pyproject.toml": "[per-file-ignores]\n\"z\" = [\"E1\"]\n\"a\" = [\"E2\"]\n",
		"a/ruff.toml":      "[[tool.mypy.overrides]]\nignore_errors = true\n",
	})
	i := strings.Index(out, "a/ruff.toml:2")
	j := strings.Index(out, "b/pyproject.toml:2")
	k := strings.Index(out, "b/pyproject.toml:3")
	if i < 0 || !(i < j && j < k) {
		t.Errorf("config rows sort by path then line:\n%s", out)
	}
}

func TestLanguageOfNamesSuffixesAndDockerfiles(t *testing.T) {
	for path, want := range map[string]string{
		"a.py": "python", "x/y.go": "go", "a.rs": "rust", "a.ts": "typescript", "a.tsx": "typescript",
		"a.js": "typescript", "a.mjs": "typescript", "w.yml": "ci", "w.yaml": "ci", "s.sh": "ci",
		"Dockerfile": "dockerfile", "Containerfile": "dockerfile", "d/Dockerfile.jinja": "dockerfile",
		"api.Dockerfile": "dockerfile", "api.dockerfile": "dockerfile",
		"README.md": "", ".yml": "", "x.": "", "Dockerfile2": "", "dockerfile": "", "pyproject.toml": "",
		"a.py/": "", "noext": "",
	} {
		if got := LanguageOf(path); got != want {
			t.Errorf("LanguageOf(%q) = %q, want %q", path, got, want)
		}
	}
}

// ---- the helpers the file-level assertions masked ----------------------------
//
// Every case below was added because a mutant of the named expression survived
// the fleet's mutation lane (foundry-tools #76, 48 survivors): the whole-file
// assertions above agree with the mutant, so the claim has to be made where it
// is decided.

func TestPathSuffixIsPythonsPathSuffix(t *testing.T) {
	for name, want := range map[string]string{
		"a.py":      ".py",
		"a.tar.gz":  ".gz",
		"x.":        "", // a trailing dot names no suffix
		".yml":      "", // a dotfile is all name
		"..":        "",
		"noext":     "",
		"":          "",
		"a.b/c.py":  ".py",
		"dir/.yaml": "",
	} {
		if got := pathSuffix(name); got != want {
			t.Errorf("pathSuffix(%q) = %q, want %q", name, got, want)
		}
	}
	if got := pathBase("a/b/c.py"); got != "c.py" {
		t.Errorf("pathBase = %q", got)
	}
	if got := pathBase("c.py"); got != "c.py" {
		t.Errorf("pathBase with no directory = %q", got)
	}
}

// The scan reads EVERY required-mode variable on a line, not the first: a file
// that arms two on one line arms both, and the contract names the smallest.
func TestRequiredModeVarsReadsEveryNameOnTheLine(t *testing.T) {
	got := RequiredModeVars("go", []string{`const a, b = "ZED_REQUIRED", "ALPHA_REQUIRED"`})
	if len(got) != 2 || !got["ZED_REQUIRED"] || !got["ALPHA_REQUIRED"] {
		t.Errorf("two variables on one line: %v", got)
	}
	if got := RequiredModeVars("go", []string{`// ZED_REQUIRED and ALPHA_REQUIRED are prose`}); len(got) != 0 {
		t.Errorf("a comment names none: %v", got)
	}
	if got := RequiredModeVars("python", []string{`x = "A_REQUIRED"  # B_REQUIRED is prose`}); len(got) != 1 || !got["A_REQUIRED"] {
		t.Errorf("code counts, the comment does not: %v", got)
	}
}

// The advisory counts .yml, .yaml AND .sh — each suffix on its own, so dropping
// any one of the three is visible.
func TestYAMLPragmasCountEachAdvisorySuffix(t *testing.T) {
	line := []string{"T = x  # " + sjAllowlistPragma}
	for _, rel := range []string{"w.yml", "w.yaml", "s.sh"} {
		known, other := YAMLPragmas(rel, line)
		if known != 0 || other != 1 {
			t.Errorf("%s: known=%d other=%d, want 0/1", rel, known, other)
		}
	}
	for _, rel := range []string{"a.py", "Dockerfile", "w.yml.jinja", "noext"} {
		if known, other := YAMLPragmas(rel, line); known != 0 || other != 0 {
			t.Errorf("%s is not an advisory suffix: known=%d other=%d", rel, known, other)
		}
	}
	known, other := YAMLPragmas("k.yml", []string{"    secrets: inherit  # " + sjAllowlistPragma})
	if known != 1 || other != 0 {
		t.Errorf("the ruled idiom: known=%d other=%d", known, other)
	}
}

// THE REPORT'S ORDER IS PYTHON'S TUPLE ORDER, and each field decides only when
// every field before it ties. Asserted in both directions: a comparison that is
// not antisymmetric is not an order, whatever it answers on one side.
func TestFindingCmpComparesEveryFieldInTupleOrder(t *testing.T) {
	base := sjFinding{rel: "b.py", line: 2, tool: "mypy", form: "type: ignore", text: "m"}
	for _, tc := range []struct {
		label string
		lo    sjFinding
	}{
		{"rel decides first", sjFinding{rel: "a.py", line: 9, tool: "zzz", form: "z", text: "z"}},
		{"line decides on an equal rel", sjFinding{rel: "b.py", line: 1, tool: "zzz", form: "z", text: "z"}},
		{"tool decides on an equal line", sjFinding{rel: "b.py", line: 2, tool: "coverage", form: "z", text: "z"}},
		{"form decides on an equal tool", sjFinding{rel: "b.py", line: 2, tool: "mypy", form: "a", text: "z"}},
		{"text decides last", sjFinding{rel: "b.py", line: 2, tool: "mypy", form: "type: ignore", text: "a"}},
	} {
		if got := findingCmp(tc.lo, base); got >= 0 {
			t.Errorf("%s: findingCmp(lo, base) = %d, want < 0", tc.label, got)
		}
		if got := findingCmp(base, tc.lo); got <= 0 {
			t.Errorf("%s: findingCmp(base, lo) = %d, want > 0", tc.label, got)
		}
	}
	if got := findingCmp(base, base); got != 0 {
		t.Errorf("every field equal is 0, got %d", got)
	}
	// Every earlier field ties and the last one decides, so no field is dead.
	near := base
	near.text = "z"
	if findingCmp(base, near) >= 0 || findingCmp(near, base) <= 0 {
		t.Error("text is the last tiebreak and must still decide")
	}
}

func TestConfigCmpComparesEveryFieldInTupleOrder(t *testing.T) {
	base := sjConfig{rel: "b.toml", SJConfigFinding: SJConfigFinding{Line: 2, Key: "mypy · ignore_errors", Detail: "m"}}
	for _, tc := range []struct {
		label string
		lo    sjConfig
	}{
		{"rel decides first", sjConfig{rel: "a.toml", SJConfigFinding: SJConfigFinding{Line: 9, Key: "z", Detail: "z"}}},
		{"line decides on an equal rel", sjConfig{rel: "b.toml", SJConfigFinding: SJConfigFinding{Line: 1, Key: "z", Detail: "z"}}},
		{"key decides on an equal line", sjConfig{rel: "b.toml", SJConfigFinding: SJConfigFinding{Line: 2, Key: "a", Detail: "z"}}},
		{"detail decides last", sjConfig{rel: "b.toml", SJConfigFinding: SJConfigFinding{Line: 2, Key: "mypy · ignore_errors", Detail: "a"}}},
	} {
		if got := configCmp(tc.lo, base); got >= 0 {
			t.Errorf("%s: configCmp(lo, base) = %d, want < 0", tc.label, got)
		}
		if got := configCmp(base, tc.lo); got <= 0 {
			t.Errorf("%s: configCmp(base, lo) = %d, want > 0", tc.label, got)
		}
	}
	if got := configCmp(base, base); got != 0 {
		t.Errorf("every field equal is 0, got %d", got)
	}
}

func TestToolsNamedNeverMatchesInsideALongerWord(t *testing.T) {
	for _, tc := range []struct {
		prose string
		want  []string
	}{
		{"mypy", []string{"mypy"}}, // the whole prose IS the name
		{"ruff and mypy disagree", []string{"mypy", "ruff"}},
		{"trailing name is still a name: ruff", []string{"ruff"}},
		{"", nil},
		{"mypyx", nil},  // a longer word is not the tool
		{"xmypy", nil},  // nor is a longer word ending in it
		{"a-mypy", nil}, // a hyphen joins, it does not separate
		{"mypy-ish", nil},
		{"mypy_2", nil},
		{"MyPy and BLACK", []string{"black", "mypy"}}, // case-insensitive
		{"(mypy) vs [black]", []string{"black", "mypy"}},
		{"mypy.black", []string{"black", "mypy"}}, // a dot does separate

		// Longest-first at a position: coverage.py wins over coverage where
		// both could match, and coverage still matches where it cannot.
		{"coverage.py", []string{"coverage"}},
		{"coverage.pyx", []string{"coverage"}},
		{"coverage", []string{"coverage"}},

		// The canonical name is what lands, not the spelling.
		{"semgrep", []string{"opengrep"}},
		{"typescript", []string{"tsc"}},
		{"go vet", []string{"go vet"}},
		{"golangci-lint", []string{"golangci-lint"}},

		// A non-ASCII letter is a word character, so it joins like any other.
		{"mypyé", nil},
		{"émypy", nil},
		{"é mypy", []string{"mypy"}},
	} {
		got := sortedKeys(toolsNamed(tc.prose))
		if len(got) != len(tc.want) {
			t.Errorf("toolsNamed(%q) = %v, want %v", tc.prose, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("toolsNamed(%q) = %v, want %v", tc.prose, got, tc.want)
				break
			}
		}
	}
}

// The marker's prose is what follows "tool-conflict:" — a tool named BEFORE it
// is somebody writing about the conflict, not a party to it.
func TestMarkerVerdictReadsOnlyTheProseAfterTheMarker(t *testing.T) {
	verdict, detail := MarkerVerdict("a/b.py", "x = 1  # mypy tool-conflict: black vs ruff", "")
	if verdict != "unratified" || !strings.Contains(detail, "names black, ruff;") {
		t.Errorf("a name before the marker is not a party: %s / %s", verdict, detail)
	}
	if strings.Contains(detail, "mypy") {
		t.Errorf("mypy sits before the marker and must not be counted: %s", detail)
	}

	// With the marker at the very start of the line, there is nothing before
	// it and the whole line is prose.
	verdict, detail = MarkerVerdict("a/b.py", "tool-conflict: black vs ruff", "")
	if verdict != "unratified" || !strings.Contains(detail, "names black, ruff;") {
		t.Errorf("a marker at column 0: %s / %s", verdict, detail)
	}

	// One tool is not a conflict, and no tool is not either.
	if v, _ := MarkerVerdict("a/b.py", "# tool-conflict: black", ""); v != "malformed" {
		t.Errorf("one tool named is malformed, got %s", v)
	}
	if v, d := MarkerVerdict("a/b.py", "# tool-conflict: because I said so", ""); v != "malformed" ||
		!strings.Contains(d, "names 0 tool(s): none") {
		t.Errorf("no tool named is malformed, got %s / %s", v, d)
	}

	// No marker on the line at all: the previous line carries it.
	if v, _ := MarkerVerdict("a/b.py", "x = 1  # type: ignore", "# tool-conflict: black vs ruff"); v != "unratified" {
		t.Errorf("the marker on the previous line still counts, got %s", v)
	}
	if v, d := MarkerVerdict("a/b.py", "x = 1  # type: ignore", ""); v != "malformed" ||
		!strings.Contains(d, "names 0 tool(s)") {
		t.Errorf("no marker anywhere is malformed, got %s / %s", v, d)
	}
}
