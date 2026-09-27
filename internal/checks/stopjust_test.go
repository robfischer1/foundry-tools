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
	sjNoqa    = "no" + "qa"
	sjTypeIg  = "type: ig" + "nore"
	sjNoCover = "pragma: no co" + "ver"
	sjAllow   = "all" + "ow"
	sjIgnore  = "ig" + "nore"
	sjNolint  = "no" + "lint"
	sjSkip    = "Sk" + "ip"
	sjHado    = "hado" + "lint"
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

// THE EDGES OF THE QUOTED FORM, each one a mutant the gate raised against the
// parser's boundaries (9 survivors, 2026-09-20). A quoting bug does not fail
// loudly — it renames a file the scan then does not read — so every boundary
// gets its own case rather than a happy path and a shrug.
func TestSplitGitPathsHoldsItsEdges(t *testing.T) {
	// NOT a quoted path, and each for a different reason: too short to carry
	// two quotes, no opening quote, no closing quote. All three are returned
	// verbatim, because an unquoted listing line IS the path.
	for _, line := range []string{`"`, `a`, `plain.py`, `"unterminated.py`, `ends-with".py`} {
		got, err := SplitGitPaths(line + "\n")
		if err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		if len(got) != 1 || got[0] != line {
			t.Errorf("%q came back as %q, want it verbatim", line, got)
		}
	}

	// The empty quoted path: two quotes and nothing between them.
	if got, err := SplitGitPaths("\"\"\n"); err != nil || len(got) != 1 || got[0] != "" {
		t.Errorf(`"" = %q, %v`, got, err)
	}

	// EVERY NAMED ESCAPE git EMITS, decoded to the byte it names.
	got, err := SplitGitPaths("\"\\a\\b\\t\\n\\v\\f\\r\\\"\\\\.py\"\n")
	if err != nil {
		t.Fatalf("named escapes: %v", err)
	}
	if want := "\a\b\t\n\v\f\r\"\\.py"; len(got) != 1 || got[0] != want {
		t.Errorf("named escapes = %q, want %q", got, want)
	}

	// THE OCTAL BOUNDARIES, WHICH ARE THE BYTE'S. \000 and \377 are the ends of
	// the range git emits and must both decode — \377 in particular, because
	// '3' is the largest leading digit a byte can have and the parser's range
	// stops exactly there.
	if got, err := SplitGitPaths("\"\\000\\377.py\"\n"); err != nil ||
		len(got) != 1 || got[0] != "\x00\xff.py" {
		t.Errorf(`\000\377 = %q, %v`, got, err)
	}

	// A LEADING DIGIT ABOVE '3' IS NOT A BYTE: \400 is 256 and \777 is 511.
	for _, over := range []string{"\"\\400.py\"\n", "\"\\777.py\"\n"} {
		if _, err := SplitGitPaths(over); err == nil {
			t.Errorf("%q is above a byte and must not parse", over)
		}
	}

	// A DIGIT OUTSIDE THE RANGE, on either side: '/' is one below '0', and '8'
	// is not octal at all. Both are escapes this parser does not know.
	for _, bad := range []string{"\"\\/00.py\"\n", "\"\\8.py\"\n", "\"\\38x.py\"\n", "\"\\39.py\"\n"} {
		if _, err := SplitGitPaths(bad); err == nil {
			t.Errorf("%q must not parse", bad)
		}
	}

	// AN OCTAL ESCAPE CUT SHORT by the end of the path — two digits, not three.
	if _, err := SplitGitPaths("\"\\30\"\n"); err == nil {
		t.Error("a two-digit octal escape must not parse")
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

func TestKubeLinterIgnoresPairsEachAnnotationWithItsWorkload(t *testing.T) {
	doc := []string{
		"kind: SandboxTemplate",
		"metadata:",
		"  name: chairman",
		"  namespace: chairmen",
		"  annotations:",
		`    ignore-check.kube-linter.io/no-extensions-v1beta: "why"`,
		"---",
		"kind: DaemonSet",
		"metadata:",
		"  name: dagger-engine",
		"  annotations:",
		`    ignore-check.kube-linter.io/privileged-container: "why"`,
	}
	got := KubeLinterIgnores("flux/a.yaml", doc)
	want := []KLIgnore{
		{Workload: "SandboxTemplate/chairman", Check: "no-extensions-v1beta"},
		{Workload: "DaemonSet/dagger-engine", Check: "privileged-container"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %+v, want %+v", i, got[i], want[i])
		}
	}
	// The config documents the annotation form; counting its own example would
	// make the record disagree with the tree forever.
	if got := KubeLinterIgnores(".kube-linter.yaml", doc); got != nil {
		t.Errorf("the config must not count its own example: %v", got)
	}
	if got := KubeLinterIgnores("a.py", doc); got != nil {
		t.Errorf("not YAML: %v", got)
	}
}

func TestKubeLinterExcludesReadsBothSpellings(t *testing.T) {
	if exc := KubeLinterExcludes(".kube-linter.yaml", []string{"checks:", "  exclude: []"}); len(exc) != 0 {
		t.Errorf("an empty exclude silences nothing, got %v", exc)
	}
	exc := KubeLinterExcludes(".kube-linter.yaml", []string{"checks:", "  exclude:", `    - "no-extensions-v1beta"`})
	if len(exc) != 1 || exc[0] != "no-extensions-v1beta" {
		t.Errorf("block form: got %v", exc)
	}
	if exc := KubeLinterExcludes(".kube-linter.yaml", []string{"checks:", `  exclude: ["a", b]`}); len(exc) != 2 {
		t.Errorf("inline form: got %v", exc)
	}
	if exc := KubeLinterExcludes("flux/a.yaml", []string{"checks:", "  exclude:", "    - x"}); exc != nil {
		t.Errorf("only the config declares excludes: %v", exc)
	}
}

func TestKLJustificationMissingNamesEveryRequiredField(t *testing.T) {
	full := KLJustification{
		Timestamp: "t", SessionUUID: "s", RobQuote: "q",
		TurnNumber: 1, Workload: "DaemonSet/x", ExemptFrom: "privileged-container",
	}
	if miss := full.Missing(); len(miss) != 0 {
		t.Errorf("a complete row is missing nothing, got %v", miss)
	}
	if miss := (KLJustification{}).Missing(); len(miss) != 6 {
		t.Errorf("an empty row is missing all six, got %v", miss)
	}
	// turn-number is required, and zero is absent rather than a turn.
	partial := full
	partial.TurnNumber = 0
	if miss := partial.Missing(); len(miss) != 1 || miss[0] != "turn-number" {
		t.Errorf("turn 0 is no turn: %v", miss)
	}
}

func TestKubeJustificationsRefusesAFileItCannotParse(t *testing.T) {
	if _, err := KubeJustifications(".kube-justifications.json", "{not json"); err == nil {
		t.Error("an unparseable record must error, not read as empty")
	}
	rows, err := KubeJustifications(".kube-justifications.json", `{"justifications":[{"workload":"DaemonSet/x","exempt-from":"y"}]}`)
	if err != nil || len(rows) != 1 || rows[0].Workload != "DaemonSet/x" {
		t.Errorf("rows=%v err=%v", rows, err)
	}
	if rows, _ := KubeJustifications("flux/a.yaml", "{}"); rows != nil {
		t.Errorf("only the record parses as the record: %v", rows)
	}
}

func TestKubeJustificationsIsTheOnlyRecordThatExcuses(t *testing.T) {
	ann := strings.Join([]string{
		"kind: DaemonSet", "metadata:", "  name: dagger-engine", "  annotations:",
		`    ignore-check.kube-linter.io/privileged-container: "why"`,
	}, "\n")
	row := `{"justifications":[{"timestamp":"t","session-uuid":"s","rob-quote":"q","turn-number":26322,` +
		`"workload":"DaemonSet/dagger-engine","exempt-from":"privileged-container"}]}`
	run := func(record string) (int, string) {
		m := map[string]string{"flux/a.yaml": ann, ".kube-linter.yaml": "checks:\n  exclude: []"}
		tracked := []string{".kube-linter.yaml", "flux/a.yaml"}
		if record != "" {
			m[".kube-justifications.json"] = record
			tracked = append(tracked, ".kube-justifications.json")
		}
		return StopJustifications(SJInput{
			Tracked: tracked,
			Read:    func(rel string) (string, error) { return m[rel], nil },
			Repo:    "infra", Today: "2026-09-24",
		})
	}
	code, out := run(row)
	if code != 0 {
		t.Errorf("a named annotation must pass, got %d:\n%s", code, out)
	}
	// The clean case must print NONE of the refusal headers. Asserting the
	// absence is what makes each `len(...) != 0` falsifiable.
	for _, absent := range []string{"no justification row names", "naming no annotation", "REPO-WIDE", "is missing"} {
		if strings.Contains(out, absent) {
			t.Errorf("clean run printed %q:\n%s", absent, out)
		}
	}
	code, out = run("")
	if code == 0 || !strings.Contains(out, "NO .kube-justifications.json") {
		t.Errorf("no record at all must fail, got %d:\n%s", code, out)
	}
	// A row that cannot quote anyone is a session excusing itself.
	code, out = run(`{"justifications":[{"workload":"DaemonSet/dagger-engine","exempt-from":"privileged-container"}]}`)
	if code == 0 || !strings.Contains(out, "missing") {
		t.Errorf("an unsigned row must fail, got %d:\n%s", code, out)
	}
	// A row for a workload nothing annotates is an excuse outliving its cause.
	stale := `{"justifications":[{"timestamp":"t","session-uuid":"s","rob-quote":"q","turn-number":1,` +
		`"workload":"DaemonSet/gone","exempt-from":"privileged-container"}]}`
	code, out = run(stale)
	if code == 0 || !strings.Contains(out, "naming no annotation") {
		t.Errorf("a stale row must fail, got %d:\n%s", code, out)
	}
}

// Rob can authorise a repo-wide exclude, but only by name: the row carries
// workload "*" because no single object can carry the reason for one.
func TestARepoWideExcludeNeedsASignedRow(t *testing.T) {
	cfg := "checks:\n  exclude:\n    - dangling-service"
	run := func(record string) (int, string) {
		m := map[string]string{".kube-linter.yaml": cfg, ".kube-justifications.json": record}
		return StopJustifications(SJInput{
			Tracked: []string{".kube-linter.yaml", ".kube-justifications.json"},
			Read:    func(rel string) (string, error) { return m[rel], nil },
			Repo:    "infra", Today: "2026-09-24",
		})
	}
	code, out := run(`{"justifications":[]}`)
	if code == 0 || !strings.Contains(out, "no signed row") {
		t.Errorf("an unsigned exclude must fail, got %d:\n%s", code, out)
	}
	signed := `{"justifications":[{"timestamp":"t","session-uuid":"s","rob-quote":"I'm authorizing it",` +
		`"turn-number":1,"workload":"*","exempt-from":"dangling-service"}]}`
	code, out = run(signed)
	if code != 0 {
		t.Errorf("a signed exclude must pass, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "each on a signed row: dangling-service") {
		t.Errorf("a signed exclude must be NAMED in the report:\n%s", out)
	}
	// Nothing is annotated here, so the per-annotation summary must not print.
	if strings.Contains(out, "each named by a row") {
		t.Errorf("no annotations means no annotation summary:\n%s", out)
	}
	if strings.Contains(out, "no signed row") {
		t.Errorf("a signed exclude must not also read as unsigned:\n%s", out)
	}
	// And the repo-wide row must not then read as stale for naming no object.
	if _, out := run(signed); strings.Contains(out, "naming no annotation") {
		t.Errorf("a repo-wide row excuses an exclude, not an annotation:\n%s", out)
	}
}

// Two stale rows, so the sort that orders them actually runs. Without a second
// row the comparator is never executed and the output's determinism is a claim
// rather than a fact — the mutation gate reported it NOT COVERED.
func TestStaleJustificationRowsAreReportedInAStableOrder(t *testing.T) {
	// Two rows SHARING a workload, differing only by check: ordering by
	// workload alone is not a total order and sort.Slice is not stable, so
	// this pair is exactly what came out in either order.
	record := `{"justifications":[` +
		`{"timestamp":"t","session-uuid":"s","rob-quote":"q","turn-number":1,"workload":"DaemonSet/zulu","exempt-from":"host-pid"},` +
		`{"timestamp":"t","session-uuid":"s","rob-quote":"q","turn-number":1,"workload":"DaemonSet/alpha","exempt-from":"privileged-container"},` +
		`{"timestamp":"t","session-uuid":"s","rob-quote":"q","turn-number":1,"workload":"DaemonSet/alpha","exempt-from":"host-network"}]}`
	m := map[string]string{".kube-justifications.json": record}
	code, out := StopJustifications(SJInput{
		Tracked: []string{".kube-justifications.json"},
		Read:    func(rel string) (string, error) { return m[rel], nil },
		Repo:    "infra", Today: "2026-09-24",
	})
	if code == 0 {
		t.Fatalf("two stale rows must fail:\n%s", out)
	}
	a := strings.Index(out, "DaemonSet/alpha")
	z := strings.Index(out, "DaemonSet/zulu")
	if a < 0 || z < 0 {
		t.Fatalf("both stale rows must be named:\n%s", out)
	}
	if a > z {
		t.Errorf("stale rows must sort by workload, got zulu before alpha:\n%s", out)
	}
	// Same workload, different checks: host-network must precede
	// privileged-container, which only holds if the whole line sorts.
	hn := strings.Index(out, "DaemonSet/alpha · host-network")
	pc := strings.Index(out, "DaemonSet/alpha · privileged-container")
	if hn < 0 || pc < 0 {
		t.Fatalf("both same-workload rows must be named:\n%s", out)
	}
	if hn > pc {
		t.Errorf("rows sharing a workload must order by check:\n%s", out)
	}
}

// SJReads and the scan must never disagree: the lane loads what SJReads names
// and the scan opens what file() accepts, and when those were two copies of
// one condition they drifted — .kube-justifications.json reached the scan as
// an empty string and the whole atom answered CANNOT RUN.
func TestSJReadsNamesEveryFileTheScanOpens(t *testing.T) {
	tracked := []string{
		".kube-justifications.json", ".kube-linter.yaml", "flux/a.yaml",
		"pyproject.toml", "ruff.toml", "main.go", "README.md", "notes.txt",
	}
	got := map[string]bool{}
	for _, r := range SJReads(tracked) {
		got[r] = true
	}
	for _, rel := range tracked {
		if SJScans(rel) != got[rel] {
			t.Errorf("%s: SJScans=%v but SJReads includes=%v", rel, SJScans(rel), got[rel])
		}
	}
	if !SJScans(".kube-justifications.json") {
		t.Error("the canonical record must be scanned")
	}
	if SJScans("README.md") {
		t.Error("prose is not scanned")
	}
}

// The record reaching the scan as an empty string is what the lane actually
// did, so the refusal it produces is worth pinning by name.
func TestAnEmptyRecordIsACannotRunNotAnEmptyScan(t *testing.T) {
	code, out := StopJustifications(SJInput{
		Tracked: []string{".kube-justifications.json"},
		Read:    func(string) (string, error) { return "", nil },
		Repo:    "infra", Today: "2026-09-24",
	})
	if code != 2 {
		t.Errorf("an unreadable record is CANNOT RUN (2), got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "does not parse") {
		t.Errorf("the refusal must say why:\n%s", out)
	}
}
