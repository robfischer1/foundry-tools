package checks

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// fleet:stop-justifications — refuse per-instance suppressions of the fleet's
// gates. Carried from foundry-stocks ci/lib/stop_justifications.py (1,738
// lines of python, state mode) so the atom runs no script. Every rule, table,
// wording and exit below is that script's; where Go had to answer a question
// python answered for free (its tokenizer, str.splitlines, str.strip) the
// answer lives in stopjust_lex.go and says what it matches.
//
// A suppression is a claim that the tool is wrong. This check makes the claim
// expensive to file and impossible to file silently: by default a repository
// may not carry one, in any of the languages, for any tool the gates run.
//
// WHY, IN ONE INCIDENT (the script's header, 2026-08-16). A fleet-wide sweep
// found 822 noqa directives, 533 suppressing one rule. 368 of those were EIGHT
// decisions replicated into 46 repos by a scaffold pour, byte identical and
// reviewed nowhere. 518 of the 533 carried a justification comment, which read
// as diligence and was a template.
//
// THE ESCAPE, AND ITS SHAPE. Tools genuinely conflict. When they do, the line
// says so in prose after `tool-conflict:`, naming both sides — and since
// 2026-08-19 that marker is an ESCALATION, not a judgement its author makes
// alone: "No supressions without my personal signoff" (Rob, session 1ca5cebf,
// turn 840). A marker excuses only when a RATIFIED row names it. Measured on
// first run: 35 markers across 7 repos, 2 ratified, 33 naming fewer than two
// tools. An unratified marker is therefore not an excuse, and the suppression
// it guards is an ordinary finding.
//
// WHAT IS NOT PORTED, and why: the script's `--since <ref>` delta mode. No
// caller in the fleet passes it — measured 2026-09-15 across every repository's
// main — so it stays behind in the script rather than arriving here unexercised.
//
// WHAT NO REGEX CATCHES: obfuscation. A flagged literal replaced by a computed
// equivalent has the same behaviour and matches nothing here. That needs a
// reader.

// ---- the escalation tables ------------------------------------------------

// ConflictMarker is the escape hatch: prose, not a code, because a code is
// copyable without thought and that is the failure this exists to stop.
var ConflictMarker = sjPy(`tool-conflict:\s*\S`)

// ToolWords is every tool the gates run, plus the spellings people reach for.
// A marker naming fewer than two of them is MALFORMED — one tool disagreeing
// with a fact is a finding, not a conflict.
var ToolWords = map[string]string{
	"ruff": "ruff", "mypy": "mypy", "pyright": "pyright", "pytest": "pytest",
	"coverage": "coverage", "coverage.py": "coverage",
	"detect-secrets": "detect-secrets", "semgrep": "opengrep",
	// ops:kube-linter gates rob/infra's flux/ tree, and the conflicts it
	// enters are with the API server rather than another linter — a check
	// demanding a migration to an apiVersion the CRD does not serve. So
	// kubectl and kubeconform are here too: without a second recognised
	// name, a marker about kube-linter is MALFORMED by construction and the
	// escape hatch it needs cannot be spelled.
	"kube-linter": "kube-linter", "kubelinter": "kube-linter",
	"kubectl": "kubectl", "kubeconform": "kubeconform",
	"opengrep": "opengrep", "bandit": "bandit", "isort": "isort",
	"pylint": "pylint", "black": "black", "clippy": "clippy", "rustc": "rustc",
	"cargo": "cargo", "golangci-lint": "golangci-lint",
	"staticcheck": "staticcheck", "gosec": "gosec", "go vet": "go vet",
	"eslint": "eslint", "tsc": "tsc", "typescript": "tsc",
	"prettier": "prettier", "vitest": "vitest", "pydantic": "pydantic",
	"hadolint": "hadolint", "shellcheck": "shellcheck",
}

// toolWordOrder is the order the script's alternation tried them in: longest
// ESCAPED spelling first, ties in declaration order. Only a shared prefix at
// one position can tell two orders apart (coverage.py before coverage).
var toolWordOrder = []string{
	"detect-secrets", "golangci-lint", "coverage.py", "staticcheck", "typescript",
	"shellcheck", "coverage", "opengrep", "prettier", "pydantic", "hadolint",
	"pyright", "semgrep", "go vet", "pytest", "bandit", "pylint", "clippy",
	"eslint", "vitest", "isort", "black", "rustc", "cargo", "gosec", "ruff",
	"mypy", "tsc",
}

// RatifiedRow is a marker Rob signed. Keyed by path and a rule fragment, never
// by line number, so an edit above the suppression does not un-ratify it.
type RatifiedRow struct {
	Path, Rule, Approved, Provenance string
}

// Ratified is the machine-readable twin of the script header's table. Nothing
// reaches it without Rob saying so, in words the header records:
//
//	grace — "Go ahead and merge it, I'm OK with the grace supression for the
//	Windows API." (session bfef42da, turn 1583, 2026-08-18T00:56:17Z)
var Ratified = []RatifiedRow{
	{
		Path:       "apps/desktop/shell-verb/src/lib.rs",
		Rule:       "non_snake_case",
		Approved:   "2026-08-18T00:56:17Z",
		Provenance: "session bfef42da, turn 1583",
	},
}

// ExemptRow excuses a RULE in a DIRECTORY of ONE REPOSITORY, on the ground
// that the code there is not the code the gate protects. Scoped three ways
// because an unscoped row would exempt a directory of that name fleet-wide:
// the repository (by origin, not checkout directory), a path prefix, and the
// exact rule the suppression names. Expires, when set, is the last ISO day it
// excuses anything.
type ExemptRow struct {
	Repo, Prefix, Rule, Approved, Expires, Provenance, Reason string
}

// DirectoryExempt carries Rob's grants, each in the words the script's header
// records:
//
//	cerberus probe/ and probes/, S603 — "Directory level exemption for probes/
//	and S603 in cerberus only. Local, dev-only tooling that is required to test
//	functionality of a 3P harness." (session Alder52, 2026-08-19)
//
//	foundry-stocks bases/blade-* and bases/chairman/, DL3002 — "Please, and give
//	the blade docker files a temporary exemption, comment in the code that my
//	exemption expires 9/20/26" (session b4bbf354, 2026-09-13); chairman
//	confirmed "chairmen exemption is fine." (session c128c3c8). Extended to
//	2026-10-20 on 2026-09-16, when Rob chose "Extend the grant" over the
//	two-container split and a setuid launcher (session e1dba511, Curie18); the
//	root-then-runuser redesign stays open as foundry-stocks work.
//
//	wrecksys docker/ and src/wrecksys_one/, DL3026 — "give wrecksys an approved
//	exemption to that rule. If it needs a quote - Wrecksys is my own personal,
//	human-authored project and a live web service hosted on AWS with no access
//	to our internal services or proxy." (session 9b7fcbb4, 2026-09-15). A
//	repository that cannot reach the fleet's proxy cannot FROM through it; the
//	grant covers DL3026 only.
var DirectoryExempt = []ExemptRow{
	{
		Repo: "cerberus", Prefix: "probe/", Rule: "S603", Approved: "2026-08-19",
		Provenance: "session Alder52, in-session",
		Reason:     "local, dev-only tooling that tests a 3P harness",
	},
	{
		Repo: "cerberus", Prefix: "probes/", Rule: "S603", Approved: "2026-08-19",
		Provenance: "session Alder52, in-session",
		Reason:     "local, dev-only tooling that tests a 3P harness",
	},
	{
		Repo: "foundry-stocks", Prefix: "bases/blade-", Rule: "DL3002", Approved: "2026-09-13",
		Expires:    "2026-10-20",
		Provenance: "session b4bbf354 (Zuse5), in-session; extended in session e1dba511 (Curie18)",
		Reason:     "the entrypoint runusers the gate and the work; non-root cannot",
	},
	{
		Repo: "foundry-stocks", Prefix: "bases/chairman/", Rule: "DL3002", Approved: "2026-09-13",
		Expires:    "2026-10-20",
		Provenance: "session b4bbf354 (Zuse5); confirmed in session c128c3c8; extended in session e1dba511 (Curie18)",
		Reason:     "the same runuser seam as the blade bases",
	},
	{
		Repo: "wrecksys", Prefix: "docker/", Rule: "DL3026", Approved: "2026-09-15",
		Provenance: "session 9b7fcbb4 (Shannon19), in-session",
		Reason:     "Rob's own human-authored project, live on AWS with no access to the fleet's proxy",
	},
	{
		Repo: "wrecksys", Prefix: "src/wrecksys_one/", Rule: "DL3026", Approved: "2026-09-15",
		Provenance: "session 9b7fcbb4 (Shannon19), in-session",
		Reason:     "Rob's own human-authored project, live on AWS with no access to the fleet's proxy",
	},
}

// ---- the inventory ----------------------------------------------------------

// sjForm is one per-instance suppression a tool honours. Code forms (a skip
// call, an allow attribute) count only in CODE; every other form is a
// comment-directive and counts only in COMMENT.
type sjForm struct {
	tool, form string
	re         *regexp.Regexp
	code, skip bool
}

// sjPy compiles a python-flavoured pattern: `\s` there is unicode whitespace,
// which RE2's `\s` is not.
func sjPy(pattern string) *regexp.Regexp {
	ws := `\t-\r \x{1c}-\x{1f}\x{85}\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`
	return regexp.MustCompile(strings.NewReplacer(`\s`, "["+ws+"]", `\S`, "[^"+ws+"]").Replace(pattern))
}

// sjPatterns is the inventory by language, in the script's order. Every entry
// is a tool one of the gates actually runs.
var sjPatterns = map[string][]sjForm{
	"python": {
		{tool: "ruff", form: "noqa", re: sjPy(`#\s*(ruff:\s*)?noqa\b`)},
		{tool: "ruff-format", form: "fmt off/on/skip", re: sjPy(`#\s*fmt:\s*(off|on|skip)\b`)},
		{tool: "mypy", form: "type: ignore", re: sjPy(`#\s*type:\s*ignore\b`)},
		{tool: "mypy", form: "mypy: ignore-errors", re: sjPy(`#\s*mypy:\s*ignore`)},
		{tool: "pyright", form: "pyright: ignore", re: sjPy(`#\s*pyright:\s*ignore\b`)},
		{tool: "pytest", form: "skip/skipif/xfail marker", re: sjPy(`@pytest\.mark\.(skip|skipif|xfail)\b`), code: true, skip: true},
		{tool: "pytest", form: "pytest.skip()", re: sjPy(`\bpytest\.skip\(`), code: true, skip: true},
		{tool: "coverage", form: "pragma: no cover", re: sjPy(`#\s*pragma:\s*no cover\b`)},
		{tool: "detect-secrets", form: "pragma: allowlist secret", re: sjPy(`#\s*pragma:\s*allowlist secret\b`)},
		{tool: "opengrep", form: "nosemgrep", re: sjPy(`#\s*nosemgrep\b`)},
		{tool: "bandit (legacy)", form: "nosec", re: sjPy(`#\s*nosec\b`)},
		{tool: "isort", form: "isort: skip", re: sjPy(`#\s*isort:\s*skip\b`)},
		{tool: "pylint", form: "pylint: disable", re: sjPy(`#\s*pylint:\s*disable\b`)},
	},
	"go": {
		{tool: "golangci-lint / staticcheck", form: "nolint", re: sjPy(`//\s*nolint\b`)},
		{tool: "golangci-lint / staticcheck", form: "lint:ignore", re: sjPy(`//\s*lint:ignore\b`)},
		{tool: "go test", form: "t.Skip()", re: sjPy(`\bt\.Skip(Now|f)?\(`), code: true, skip: true},
		{tool: "gosec", form: "nosec", re: sjPy(`#\s*nosec\b`)},
		{tool: "opengrep", form: "nosemgrep", re: sjPy(`//\s*nosemgrep\b`)},
		// A code form that begins `//` sits in a comment by construction, so it
		// can never count. The script carries the same row with the same result.
		{tool: "go build", form: "go:build ignore", re: sjPy(`//go:build\s+ignore\b`), code: true},
	},
	"rust": {
		{tool: "rustc / clippy", form: "allow(...)", re: sjPy(`#!?\[allow\(`), code: true},
		{tool: "rustc / clippy", form: "expect(...)", re: sjPy(`#!?\[expect\(`), code: true},
		{tool: "cargo test", form: "#[ignore]", re: sjPy(`#\[ignore\b`), code: true, skip: true},
		{tool: "opengrep", form: "nosemgrep", re: sjPy(`//\s*nosemgrep\b`)},
	},
	"typescript": {
		{tool: "eslint", form: "eslint-disable", re: sjPy(`(//|/\*)\s*eslint-disable`)},
		{tool: "tsc", form: "ts-ignore/expect-error/nocheck", re: sjPy(`@ts-(ignore|expect-error|nocheck)\b`)},
		{tool: "prettier", form: "prettier-ignore", re: sjPy(`prettier-ignore\b`)},
		{tool: "vitest", form: "skip/only/todo", re: sjPy(`\b(it|test|describe)\.(skip|only|todo)\b`), code: true, skip: true},
		{tool: "opengrep", form: "nosemgrep", re: sjPy(`//\s*nosemgrep\b`)},
	},
	"ci": {
		{tool: "forgejo actions", form: "continue-on-error", re: sjPy(`continue-on-error:\s*true\b`), code: true},
		{tool: "forgejo actions", form: "if: false", re: sjPy(`if:\s*false\b`), code: true},
		{tool: "shell", form: "|| true / || exit 0", re: sjPy(`\|\|\s*(true|exit 0)\b`), code: true},
		{tool: "shell", form: "--exit-zero", re: sjPy(`--exit-zero\b`), code: true},
	},
	"dockerfile": {
		{tool: "hadolint", form: "hadolint ignore", re: sjPy(`#\s*hadolint\s+(global\s+)?ignore\s*=`)},
	},
}

// sjSuffixes maps a file suffix to its language, python's Path.suffix.
var sjSuffixes = map[string]string{
	".py": "python", ".go": "go", ".rs": "rust",
	".ts": "typescript", ".tsx": "typescript", ".js": "typescript", ".mjs": "typescript",
	".yml": "ci", ".yaml": "ci", ".sh": "ci",
}

// A Dockerfile has no suffix, so it is named: Dockerfile, Dockerfile.<variant>,
// <name>.Dockerfile, Containerfile. A template's Dockerfile.jinja matches too,
// which is right — a pragma poured from a template is poured everywhere.
var sjDockerfileName = regexp.MustCompile(`^(Dockerfile|Containerfile)(\..+)?$|\.[Dd]ockerfile$`)

// SJSelf is the script and its test: both name every directive they forbid,
// so they match themselves. Excluded by name, and the summary says so — an
// exclusion the reader cannot see is the defect this check exists to catch.
var SJSelf = []string{"ci/lib/stop_justifications.py", "ci/lib/stop_justifications.test.sh"}

// pathBase and pathSuffix are python's Path.name and Path.suffix.
func pathBase(rel string) string {
	return rel[strings.LastIndexByte(rel, '/')+1:]
}

func pathSuffix(rel string) string {
	name := pathBase(rel)
	i := strings.LastIndexByte(name, '.')
	if i <= 0 || i == len(name)-1 {
		return ""
	}
	return name[i:]
}

// LanguageOf answers the language bucket for a path, or "" when it is not
// scanned.
func LanguageOf(rel string) string {
	if sjDockerfileName.MatchString(pathBase(rel)) {
		return "dockerfile"
	}
	return sjSuffixes[pathSuffix(rel)]
}

// SJHit is one forbidden directive on one line.
type SJHit struct {
	Tool, Form string
	skip       bool
}

// ScanLine answers every forbidden directive on line, judged by the region of
// the FIRST match of each form — the script searched once per form. mask is
// the line's region mask; "" classifies the line alone.
func ScanLine(lang, line, mask string) []SJHit {
	if mask == "" {
		mask = RegionMask(lang, []string{line})[0]
	}
	var hits []SJHit
	for _, f := range sjPatterns[lang] {
		loc := f.re.FindStringIndex(line)
		if loc == nil {
			continue
		}
		want := byte(RegionComment)
		if f.code {
			want = RegionCode
		}
		if RegionAt(mask, loc[0]) == want {
			hits = append(hits, SJHit{Tool: f.tool, Form: f.form, skip: f.skip})
		}
	}
	return hits
}

// ---- the excuses ------------------------------------------------------------

var sjSuppressedCodes = regexp.MustCompile(`(?:(?:noqa|nolint|allow|expect)\s*[:(]|hadolint\s+(?:global\s+)?ignore\s*=)\s*([A-Za-z0-9_, ]+)`)

// SuppressedCodes answers the rule codes a suppression on line names. A bare
// form names none, which is why it can never be exempt.
func SuppressedCodes(line string) []string {
	m := sjSuppressedCodes.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range strings.Split(m[1], ",") {
		c = pyStrip(c)
		if c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// DirectoryExemption answers the row excusing this suppression, or nil. All
// three must match — repository, path prefix, and the rule the line names AND
// NOTHING ELSE — and a row past its expiry excuses nothing. today is an ISO
// date, which compares correctly as text.
func DirectoryExemption(repo, rel, line, today string) *ExemptRow {
	codes := SuppressedCodes(line)
	if len(codes) != 1 {
		return nil
	}
	for i := range DirectoryExempt {
		row := &DirectoryExempt[i]
		if repo != row.Repo || !strings.HasPrefix(rel, row.Prefix) || codes[0] != row.Rule {
			continue
		}
		if row.Expires != "" && today > row.Expires {
			continue
		}
		return row
	}
	return nil
}

// toolsNamed answers the canonical tools prose names, the script's _TOOL_RE:
// case-insensitive, never inside a longer word or hyphenated name.
//
// NEITHER EDGE NEEDS A GUARD, and the two that used to be here were worse than
// redundant. DecodeLastRuneInString("") and DecodeRuneInString("") both answer
// RuneError, which is not a word character — so the first position already has
// nothing behind it and the last already has nothing ahead. An `i > 0` and an
// `end == len(prose)` only restated that, which made them unfalsifiable: the
// mutation lane flipped both and no test could move, because the decoders give
// the same answer either way.
func toolsNamed(prose string) map[string]bool {
	named := map[string]bool{}
	for rest, done := prose, 0; rest != ""; {
		width := 0
		if !toolWordChar(lastRune(prose[:done])) {
			for _, w := range toolWordOrder {
				if len(rest) >= len(w) && strings.EqualFold(rest[:len(w)], w) &&
					!toolWordChar(firstRune(rest[len(w):])) {
					named[ToolWords[w]] = true
					width = len(w)
					break
				}
			}
		}
		step := max(width, 1)
		done += step
		rest = rest[step:]
	}
	return named
}

func toolWordChar(r rune) bool {
	return r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func lastRune(s string) rune {
	r, _ := utf8.DecodeLastRuneInString(s)
	return r
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

// MarkerVerdict classifies the tool-conflict marker guarding line: "ratified",
// "unratified" or "malformed". Only ratified excuses. There is no flag — an
// excuse that does not meet the rule is not a softer excuse, it is not one.
func MarkerVerdict(rel, line, previous string) (verdict, detail string) {
	text := previous
	if ConflictMarker.MatchString(line) {
		text = line
	}
	prose := text
	if _, after, found := strings.Cut(text, "tool-conflict:"); found {
		prose = after
	}
	for _, row := range Ratified {
		if rel == row.Path && strings.Contains(text, row.Rule) {
			return "ratified", fmt.Sprintf("approved %s (%s)", row.Approved, row.Provenance)
		}
	}
	named := sortedKeys(toolsNamed(prose))
	if len(named) < 2 {
		got := "none"
		if len(named) == 1 {
			got = named[0]
		}
		return "malformed", fmt.Sprintf("names %d tool(s): %s — a conflict needs two", len(named), got)
	}
	return "unratified", fmt.Sprintf("names %s; no signoff on record", strings.Join(named, ", "))
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// THE SKIP-VS-FAIL CONTRACT. A skip when infrastructure is absent is right on
// a runner that has none and a trap on a lane that should — "a broken DinD
// lane would skip green forever". <SCOPE>_<RESOURCE>_REQUIRED turns the same
// condition into a failure, and unlike a marker it needs no signoff: it is an
// executable branch, so it cannot be written falsely without writing a working
// failure path. Matched outside comments only — a comment naming one is
// somebody explaining a contract, not filing one.
var sjRequiredMode = regexp.MustCompile(`\b[A-Z][A-Z0-9_]*_REQUIRED\b`)

// RequiredModeVars answers the required-mode variables lines name outside a
// comment. A string counts: `os.getenv("FOO_REQUIRED")` is how one is armed.
func RequiredModeVars(lang string, lines []string) map[string]bool {
	found := map[string]bool{}
	masks := RegionMask(lang, lines)
	for i, line := range lines {
		for _, loc := range sjRequiredMode.FindAllStringIndex(line, -1) {
			if RegionAt(masks[i], loc[0]) != RegionComment {
				found[line[loc[0]:loc[1]]] = true
			}
		}
	}
	return found
}

// ---- the yaml advisory ------------------------------------------------------

// detect-secrets reads YAML and shell too. Its pragma there is COUNTED, not
// enforced: enforcing lit 142 lines across 53 repos, the fingerprint of a
// scaffold pour. One idiom is ruled — `secrets: inherit`, 239 instances and
// one distinct line, load-bearing (strip it and detect-secrets-hook reds).
var (
	sjYAMLSecretPragma = sjPy(`#\s*pragma:\s*allowlist secret\b`)
	sjYAMLIdiom        = sjPy(`^\s*#?\s*secrets:\s*inherit\s+#\s*pragma:\s*allowlist secret\b`)
)

// YAMLPragmas counts detect-secrets pragmas in a .yml/.yaml/.sh file as
// (known idiom, other).
func YAMLPragmas(rel string, lines []string) (known, other int) {
	s := pathSuffix(rel)
	if s != ".yml" && s != ".yaml" && s != ".sh" {
		return 0, 0
	}
	for _, line := range lines {
		if !sjYAMLSecretPragma.MatchString(line) {
			continue
		}
		if sjYAMLIdiom.MatchString(line) {
			known++
		} else {
			other++
		}
	}
	return known, other
}

// ---- kube-linter ignore annotations ----------------------------------------

// kube-linter's per-object escape hatch is an annotation, not a config key:
//
//	metadata:
//	  annotations:
//	    ignore-check.kube-linter.io/privileged-container: "why, in prose"
//
// That is the RIGHT shape — the reason sits on the object it excuses, and the
// check stays live for every other object. A repo-wide checks.exclude was
// measured to be the wrong one: excluding no-extensions-v1beta in rob/infra
// also hid a real extensions/v1beta1 Ingress planted to test it.
//
// BUT AN ANNOTATION IS STILL A SUPPRESSION OF A FLEET GATE, and this check
// exists so none is filed silently. rob/infra declares every one in the
// comment header of its .kube-linter.yaml, per workload (Rob, this session:
// "Every single annotation that we're adding to a pod is identified in the
// comment header of .kube-linter.yaml, per-workload"). This reader is what
// makes that a rule rather than a habit: the tree and the header must agree.
//
// ONE REPOSITORY CAN FAIL THIS, measured 2026-09-24: infra is the only repo in
// the fleet with a flux/ tree, so ops:kube-linter gates it alone and nothing
// else can trip over this.
var (
	sjKLAnnotation = sjPy(`ignore-check\.kube-linter\.io/([a-z0-9-]+)\s*:`)
	sjKLInventory  = sjPy(`^#\s+[A-Za-z]+/[A-Za-z0-9.-]+\s+([a-z0-9-]+)\s*$`)
	sjKLExclude    = sjPy(`^\s*-\s*"?([a-z0-9-]+)"?\s*$`)
)

// IsKubeLinterConfig answers whether a path is the kube-linter config, in
// either spelling kube-linter itself accepts (pkg/config/config.go:42).
func IsKubeLinterConfig(rel string) bool {
	b := pathBase(rel)
	return b == ".kube-linter.yaml" || b == ".kube-linter.yml"
}

// KubeLinterIgnores answers the checks excused by ignore-check annotations in
// one .yml/.yaml file, in the order they appear.
func KubeLinterIgnores(rel string, lines []string) []string {
	if suf := pathSuffix(rel); suf != ".yml" && suf != ".yaml" {
		return nil
	}
	if IsKubeLinterConfig(rel) {
		return nil
	}
	var out []string
	for _, line := range lines {
		if m := sjKLAnnotation.FindStringSubmatch(line); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

// KubeLinterDeclared answers what a .kube-linter.yaml declares: the checks its
// comment inventory names per workload, and the checks its checks.exclude
// silences repo-wide. Anything else is not this function's business.
func KubeLinterDeclared(rel string, lines []string) (inventory, excluded []string) {
	if !IsKubeLinterConfig(rel) {
		return nil, nil
	}
	inExclude := false
	for _, line := range lines {
		if m := sjKLInventory.FindStringSubmatch(line); m != nil {
			inventory = append(inventory, m[1])
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "exclude:") {
			inExclude = true
			// `exclude: []` and `exclude: [a, b]` both end the block here.
			if rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "exclude:")); rest != "" {
				inExclude = false
				rest = strings.Trim(rest, "[]")
				for _, r := range strings.Split(rest, ",") {
					if r = strings.Trim(strings.TrimSpace(r), `"'`); r != "" {
						excluded = append(excluded, r)
					}
				}
			}
			continue
		}
		if !inExclude {
			continue
		}
		if m := sjKLExclude.FindStringSubmatch(line); m != nil {
			excluded = append(excluded, m[1])
			continue
		}
		inExclude = false
	}
	return inventory, excluded
}

// ---- config-level silencing -------------------------------------------------

// A config key silences at repo scale what a directive silences on a line. On
// 2026-08-20 one poured "tests/**" entry hid 2,795 ruff findings across 26
// repos. Counted: per-file-ignores entries, and mypy overrides that set
// ignore_errors or disable_error_code. Measured and rejected: ruff lint.ignore
// (one poured 15-rule baseline) and coverage omit (zero fleet-wide).
//
// EXEMPT: an entry whose every rule is exempt for its glob. INP001 anywhere (a
// directory is not a package — a fact about layout). On a test glob, also
// S101, ANN, D, E402 and PLW1510 — S101 because pytest IS assert, ratified by
// Rob 2026-08-21 in words the script records: "Please note S101, scoped only
// to tests/** as a known idiom, in the canonical stop-justifications hook,
// this turn is the citation. You and the subagents will, however, verify the
// logic in every single one of those assertions under the assumption that
// they're tautologies and the logic is absolute garbage." PLR2004, SLF001 and
// the rest of S stay findings: silencing magic-value comparison in every
// suite is silencing the evidence.
var (
	sjNotAPackage    = map[string]bool{"INP001": true}
	sjTestErgonomics = map[string]bool{"INP001": true, "S101": true, "ANN": true, "D": true, "E402": true, "PLW1510": true}
	sjTestGlob       = regexp.MustCompile(`(^|/)(tests?|conftest)([./_*]|$)`)
	sjPFISection     = sjPy(`^\[(?:tool\.ruff\.lint\.|lint\.)?per-file-ignores\]\s*$`)
	sjPFIEntry       = sjPy(`^\s*"([^"]+)"\s*=\s*\[([^\]]*)\]`)
	sjMypyOverride   = sjPy(`^\[\[tool\.mypy\.overrides\]\]\s*$`)
	sjMypySilencer   = sjPy(`^\s*(ignore_errors|disable_error_code)\s*=\s*(.+)$`)
)

// SJConfigFinding is one config-level suppression.
type SJConfigFinding struct {
	Line        int
	Key, Detail string
}

// ConfigSilencing answers the config-level suppressions in a pyproject.toml or
// ruff.toml, and how many entries were the ruled idiom.
func ConfigSilencing(rel string, lines []string) (found []SJConfigFinding, idioms int) {
	name := pathBase(rel)
	if name != "pyproject.toml" && name != "ruff.toml" {
		return nil, 0
	}
	inPFI, inMypy := false, false
	for i, line := range lines {
		if strings.HasPrefix(line, "[") {
			inPFI = sjPFISection.MatchString(line)
			inMypy = sjMypyOverride.MatchString(line)
			continue
		}
		if inPFI {
			m := sjPFIEntry.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			rules := entryRules(m[2])
			if len(rules) > 0 && allExempt(rules, m[1]) {
				idioms++
				continue
			}
			found = append(found, SJConfigFinding{Line: i + 1, Key: "ruff · per-file-ignores", Detail: fmt.Sprintf(`"%s" silences %s`, m[1], strings.Join(rules, ", "))})
			continue
		}
		if m := sjMypySilencer.FindStringSubmatch(line); inMypy && m != nil {
			found = append(found, SJConfigFinding{Line: i + 1, Key: "mypy · " + m[1], Detail: runeCap(pyStrip(line), 90)})
		}
	}
	return found, idioms
}

func entryRules(list string) []string {
	var rules []string
	for _, r := range strings.Split(list, ",") {
		if pyStrip(r) != "" {
			rules = append(rules, strings.Trim(pyStrip(r), `"`))
		}
	}
	return rules
}

func allExempt(rules []string, glob string) bool {
	exempt := sjNotAPackage
	if sjTestGlob.MatchString(glob) {
		exempt = sjTestErgonomics
	}
	for _, r := range rules {
		if !exempt[r] {
			return false
		}
	}
	return true
}

// ---- the state scan ---------------------------------------------------------

// SJInput is one repository as the scan sees it.
type SJInput struct {
	// Tracked is `git ls-files` in its own order.
	Tracked []string
	// Read answers a tracked file's contents.
	Read func(rel string) (string, error)
	// Repo is the repository's name; "" disables DirectoryExempt entirely.
	Repo string
	// Today is the UTC date, ISO-formatted.
	Today string
}

type sjFinding struct {
	rel        string
	line       int
	tool, form string
	text, lang string
}

type sjContract struct {
	rel  string
	line int
	form string
}

type sjExempted struct {
	rel          string
	line         int
	rule, reason string
}

type sjConfig struct {
	rel string
	SJConfigFinding
}

// sjKLIgnore is one ignore-check.kube-linter.io annotation, and where it is.
type sjKLIgnore struct {
	rel, check string
}

// sjScan is the state scan's accumulated record.
type sjScan struct {
	in        SJInput
	out       strings.Builder
	findings  []sjFinding
	markers   int
	ratified  int
	yamlKnown int
	yamlOther int
	// kube-linter's per-object escape hatch, and what .kube-linter.yaml says
	// about it. Compared in report(): the tree and the header must agree.
	klIgnores   []sjKLIgnore
	klInventory []string
	klExcluded  []string
	klConfig    string
	contracts   map[string][]sjContract
	// packageVars is each go directory's required-mode variables.
	packageVars map[string]map[string]bool
	exempted    []sjExempted
	config      []sjConfig
	idioms      int
	self        []string
	excluded    int
	scanned     int
}

// StopJustifications runs the state scan over one repository and answers the
// script's exit (0 clean, 1 findings, 2 could not run) and everything it
// printed, in the order it printed it.
func StopJustifications(in SJInput) (int, string) {
	s := &sjScan{in: in, contracts: map[string][]sjContract{}, packageVars: map[string]map[string]bool{}}
	for _, rel := range in.Tracked {
		if err := s.file(rel); err != nil {
			s.out.WriteString("stop-justifications: CANNOT RUN — " + err.Error() + "\n")
			return 2, s.out.String()
		}
	}
	return s.report(), s.out.String()
}

// read answers a file's lines, decoded the way the script read them.
func (s *sjScan) read(rel string) ([]string, error) {
	body, err := s.in.Read(rel)
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", rel, err)
	}
	return SplitLines(strings.ToValidUTF8(body, "�")), nil
}

// file scans one tracked path. What the fleet excludes (GateExclude) is counted
// and skipped: the script read each repository's own pre-commit `exclude:`,
// which is a repository deciding what the gate looks at, and since 2026-09-11
// a repository has no say in anything that runs.
func (s *sjScan) file(rel string) error {
	for _, self := range SJSelf {
		if rel == self {
			s.self = append(s.self, rel)
			return nil
		}
	}
	if gateExcludeRE.MatchString(rel) {
		s.excluded++
		return nil
	}
	name := pathBase(rel)
	lang := LanguageOf(rel)
	if lang == "" && name != "pyproject.toml" && name != "ruff.toml" {
		return nil
	}
	lines, err := s.read(rel)
	if err != nil {
		return err
	}
	found, idioms := ConfigSilencing(rel, lines)
	s.idioms += idioms
	for _, f := range found {
		s.config = append(s.config, sjConfig{rel: rel, SJConfigFinding: f})
	}
	if lang == "" {
		return nil
	}
	s.scanned++
	known, other := YAMLPragmas(rel, lines)
	s.yamlKnown += known
	s.yamlOther += other
	for _, c := range KubeLinterIgnores(rel, lines) {
		s.klIgnores = append(s.klIgnores, sjKLIgnore{rel: rel, check: c})
	}
	if IsKubeLinterConfig(rel) {
		inv, exc := KubeLinterDeclared(rel, lines)
		s.klConfig = rel
		s.klInventory = append(s.klInventory, inv...)
		s.klExcluded = append(s.klExcluded, exc...)
	}
	return s.scanFile(rel, lang, lines)
}

// armed answers the required-mode variables in scope for a file. Go splits a
// package across files — hephaestus arms its goldens gate from a const beside
// the test — so a Go file naming none inherits its directory's. No other
// language widens: python and typescript resolve names through imports.
//
// A sibling that will not read is the scan's refusal NOW rather than when its
// own turn comes: the script swallowed it here and refused on it moments later,
// with the same words, because every go sibling is itself scanned.
func (s *sjScan) armed(rel, lang string, lines []string) (map[string]bool, error) {
	found := RequiredModeVars(lang, lines)
	if len(found) > 0 || pathSuffix(rel) != ".go" {
		return found, nil
	}
	dir := rel[:strings.LastIndexByte(rel, '/')+1]
	// The file itself names none, so its package's set is its siblings' set,
	// and one union per directory answers every file in it.
	if vars, ok := s.packageVars[dir]; ok {
		return vars, nil
	}
	for _, sib := range s.in.Tracked {
		if !strings.HasPrefix(sib, dir) || strings.Contains(sib[len(dir):], "/") || pathSuffix(sib) != ".go" {
			continue
		}
		sibLines, err := s.read(sib)
		if err != nil {
			return nil, err
		}
		for v := range RequiredModeVars("go", sibLines) {
			found[v] = true
		}
	}
	s.packageVars[dir] = found
	return found, nil
}

func (s *sjScan) scanFile(rel, lang string, lines []string) error {
	armed, err := s.armed(rel, lang, lines)
	if err != nil {
		return err
	}
	masks := RegionMask(lang, lines)
	for i, line := range lines {
		previous := ""
		if i > 0 {
			previous = lines[i-1]
		}
		marked := ConflictMarker.MatchString(line) || ConflictMarker.MatchString(previous)
		// THE MARKER AUDIT reads each line ALONE, as the script's scan_markers
		// did: a marker beside a directive the file-wide reader calls prose (a
		// docstring) is still a marker somebody filed.
		if marked && len(ScanLine(lang, line, "")) > 0 {
			s.markers++
			if v, _ := MarkerVerdict(rel, line, previous); v == "ratified" {
				s.ratified++
			}
		}
		hits := ScanLine(lang, line, masks[i])
		if len(hits) == 0 {
			continue
		}
		if len(armed) > 0 && allSkips(hits) {
			v := sortedKeys(armed)[0]
			for _, h := range hits {
				s.contracts[v] = append(s.contracts[v], sjContract{rel: rel, line: i + 1, form: h.Form})
			}
			continue
		}
		if s.in.Repo != "" {
			if row := DirectoryExemption(s.in.Repo, rel, line, s.in.Today); row != nil {
				reason := row.Reason
				if row.Expires != "" {
					reason += " — EXPIRES " + row.Expires
				}
				s.exempted = append(s.exempted, sjExempted{rel: rel, line: i + 1, rule: row.Rule, reason: reason})
				continue
			}
		}
		text := runeCap(pyStrip(line), 110)
		if marked {
			verdict, detail := MarkerVerdict(rel, line, previous)
			if verdict == "ratified" {
				continue
			}
			text = fmt.Sprintf("[%s: %s] %s", strings.ToUpper(verdict), detail, runeCap(pyStrip(line), 70))
		}
		for _, h := range hits {
			s.findings = append(s.findings, sjFinding{rel: rel, line: i + 1, tool: h.Tool, form: h.Form, text: text, lang: lang})
		}
	}
	return nil
}

func allSkips(hits []SJHit) bool {
	for _, h := range hits {
		if !h.skip {
			return false
		}
	}
	return true
}

// report prints the script's summary and answers its exit.
func (s *sjScan) report() int {
	w := &s.out
	if s.excluded > 0 {
		fmt.Fprintf(w, "stop-justifications: skipped %d file(s) the fleet excludes (vendored and tool trees) — they cannot fail anything.\n", s.excluded)
	}
	if len(s.self) > 0 {
		fmt.Fprintf(w, "stop-justifications: skipped its own definition (%s) — it names every directive it forbids.\n", strings.Join(s.self, ", "))
	}
	if s.yamlKnown > 0 {
		fmt.Fprintf(w, "\nstop-justifications: %d detect-secrets pragma(s) in YAML/shell are a KNOWN IDIOM — ruled, exempt, nothing to do.\n", s.yamlKnown)
		w.WriteString("  `secrets: inherit` passes the caller's secrets to a reusable\n" +
			"  workflow and names no credential; detect-secrets' Secret Keyword\n" +
			"  detector fires on the word itself. Measured 2026-08-20: 239 such\n" +
			"  pragmas across 53 repos and exactly ONE distinct line under them.\n" +
			"  Load-bearing — strip one and that repo's pre-commit reds.\n\n")
	}
	if s.yamlOther > 0 {
		fmt.Fprintf(w, "\nstop-justifications: %d detect-secrets pragma(s) in YAML/shell — ADVISORY, not counted as findings.\n", s.yamlOther)
		w.WriteString("  These are NOT the known `secrets: inherit` idiom, so each is a\n" +
			"  decision nobody has ruled on. Reported rather than enforced\n" +
			"  because enforcing the whole class at once lit 142 lines across\n" +
			"  53 repos — a scaffold pour, not 53 decisions.\n\n")
	}
	klBad := s.reportKubeLinter(w)
	if len(s.exempted) > 0 {
		fmt.Fprintf(w, "\nstop-justifications: %d suppression(s) excused by a DIRECTORY exemption in %s.\n", len(s.exempted), s.in.Repo)
		for _, e := range s.exempted {
			fmt.Fprintf(w, "  %s:%d · %s — %s\n", e.rel, e.line, e.rule, e.reason)
		}
		w.WriteString("  Rob approved these by directory; the header table records the words.\n" +
			"  Printed rather than passed over in silence: an excuse nobody can see\n" +
			"  is how 33 self-authored tool-conflict markers accumulated unread.\n\n")
	}
	if len(s.contracts) > 0 {
		total := 0
		for _, rows := range s.contracts {
			total += len(rows)
		}
		fmt.Fprintf(w, "\nstop-justifications: %d skip-vs-fail contract(s) — excused, armed by %d required-mode variable(s).\n", total, len(s.contracts))
		for _, v := range sortedContractVars(s.contracts) {
			fmt.Fprintf(w, "  %s\n", v)
			for _, c := range s.contracts[v] {
				fmt.Fprintf(w, "    %s:%d · %s\n", c.rel, c.line, c.form)
			}
		}
		w.WriteString("  A skip is a CONTRACT when a required-mode variable turns the same\n" +
			"  condition into a failure — the lane that has the infrastructure\n" +
			"  arms it and a broken lane goes red instead of skipping green.\n" +
			"  Unlike a tool-conflict marker this needs no signoff: it is an\n" +
			"  executable branch, not prose, so it cannot be written falsely\n" +
			"  without writing a working failure path.\n\n")
	}
	if s.idioms > 0 {
		fmt.Fprintf(w, "\nstop-justifications: %d per-file-ignores entry(s) are a KNOWN IDIOM — ruled, exempt, nothing to do.\n", s.idioms)
		w.WriteString("  An entry whose ONLY rule is INP001 declares that a directory is\n" +
			"  not an importable package (scripts/, probe/, specs/, crates/).\n" +
			"  That is a fact about the layout, not a claim that a tool is\n" +
			"  wrong. Measured 2026-08-20: 156 of the fleet's 193 entries, in\n" +
			"  20 repos across 12 globs — one poured decision, not 156. Any\n" +
			"  other rule in the same entry takes it out of the idiom.\n\n")
	}
	if s.markers > 0 {
		fmt.Fprintf(w, "\nstop-justifications: %d tool-conflict marker(s) — %d ratified, %d NOT (counted as findings).\n", s.markers, s.ratified, s.markers-s.ratified)
	}
	if len(s.config) > 0 {
		slices.SortFunc(s.config, configCmp)
		fmt.Fprintf(w, "\nstop-justifications: %d CONFIG-level suppression(s).\n\n", len(s.config))
		for _, c := range s.config {
			fmt.Fprintf(w, "    %s:%d\n        %s\n        %s\n", c.rel, c.Line, c.Key, c.Detail)
		}
		w.WriteString("\n  A config key silences at repo scale what a directive silences on\n" +
			"  one line, and it does it in a file nobody reads twice. Measured\n" +
			"  2026-08-20: one poured `\"tests/**\"` entry hid 2,795 ruff findings\n" +
			"  across 26 repos, and half the per-file carve-outs in src/ were\n" +
			"  already DEAD — silencing nothing, detected by nothing, because\n" +
			"  RUF100 reads a `noqa` and has never read a config.\n" +
			"\n" +
			"  Narrow it to the lines that need it and answer them, or delete an\n" +
			"  entry that turns out to silence nothing. There is no marker for\n" +
			"  this: a config entry is not a line, so it cannot carry a\n" +
			"  tool-conflict on one.\n\n")
	}
	if len(s.findings) == 0 && len(s.config) == 0 && !klBad {
		fmt.Fprintf(w, "stop-justifications: %d file(s) scanned, no undocumented suppressions.\n", s.scanned)
		return 0
	}
	if len(s.findings) == 0 {
		return 1
	}
	s.printFindings()
	return 1
}

func (s *sjScan) printFindings() {
	w := &s.out
	slices.SortFunc(s.findings, findingCmp)
	byLang := map[string][]sjFinding{}
	for _, f := range s.findings {
		byLang[f.lang] = append(byLang[f.lang], f)
	}
	langs := make([]string, 0, len(byLang))
	for l := range byLang {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	fmt.Fprintf(w, "\nstop-justifications: %d suppression(s) with no stated conflict.\n\n", len(s.findings))
	for _, l := range langs {
		fmt.Fprintf(w, "  ── %s %s\n", l, strings.Repeat("─", 60-len(l)))
		for _, f := range byLang[l] {
			fmt.Fprintf(w, "    %s:%d\n        %s · %s\n        %s\n", f.rel, f.line, f.tool, f.form, f.text)
		}
	}
	w.WriteString("\n  A suppression is a claim the tool is wrong. Fix the finding, or —\n" +
		"  when two tools genuinely disagree — say so on the line and let the\n" +
		"  more permissive one yield:\n" +
		"\n" +
		"      <code>  <directive>  # tool-conflict: <what disagrees, in prose>\n" +
		"\n" +
		"  Prose, not a code: a code is copyable without thought, which is the\n" +
		"  failure this exists to stop.\n\n")
}

func sortedContractVars(m map[string][]sjContract) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// findingCmp is python's tuple order over (rel, line, tool, form, text).
//
// A TOTAL COMPARISON, not a chain of guarded <. The guarded form said
// `if a.rel != b.rel { return a.rel < b.rel }` five times over, and every one
// of those guards made the comparison beneath it unfalsifiable: a.rel == b.rel
// is exactly the case the guard excludes, so < and <= can never answer
// differently and no test can tell them apart. The mutation lane flipped all
// seven and all seven lived. cmp.Or short-circuits on the first non-zero, which
// is the same order with nothing left to flip.
func findingCmp(a, b sjFinding) int {
	return cmp.Or(
		cmp.Compare(a.rel, b.rel),
		cmp.Compare(a.line, b.line),
		cmp.Compare(a.tool, b.tool),
		cmp.Compare(a.form, b.form),
		cmp.Compare(a.text, b.text),
	)
}

// configCmp is python's tuple order over (rel, line, key, detail).
func configCmp(a, b sjConfig) int {
	return cmp.Or(
		cmp.Compare(a.rel, b.rel),
		cmp.Compare(a.Line, b.Line),
		cmp.Compare(a.Key, b.Key),
		cmp.Compare(a.Detail, b.Detail),
	)
}

// SJReads answers the tracked files the scan will read — every scanned
// language and every ruff config, less the script's own definition and what
// the gate excludes — so a caller can fetch them before the scan asks.
func SJReads(tracked []string) []string {
	var out []string
	for _, rel := range tracked {
		name := pathBase(rel)
		if LanguageOf(rel) == "" && name != "pyproject.toml" && name != "ruff.toml" {
			continue
		}
		if rel == SJSelf[0] || rel == SJSelf[1] || gateExcludeRE.MatchString(rel) {
			continue
		}
		out = append(out, rel)
	}
	return out
}

// RepoFromOrigin answers the repository an origin URL names — the basename,
// less `.git` — for both spellings the fleet uses: path-style
// `http://door/cerberus.git` and scp-style `git@host:rob/cerberus.git`.
//
// THE REMOTE IS THE ONLY COORDINATE A CHECKOUT CANNOT RENAME. The door's lane
// clones into a directory of its own choosing, and on 2026-09-04 eight S603
// suppressions Rob exempted under cerberus probes/ failed every gate because
// the checkout's directory was not called cerberus.
func RepoFromOrigin(url string) string {
	url = strings.TrimRight(pyStrip(url), "/")
	tail := url[strings.LastIndexAny(url, "/:")+1:]
	return strings.TrimSuffix(tail, ".git")
}

// SplitNul answers the fields of a NUL-separated listing (`git ls-files -z`),
// with no empty field for the trailing NUL.
func SplitNul(listing string) []string {
	var out []string
	for _, f := range strings.Split(listing, "\x00") {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// SplitGitPaths answers the paths of a `git ls-files` listing — one per line,
// C-quoted where git decided the path needed it.
//
// WHY NOT `-z`, WHICH WOULD NEED NO PARSER AT ALL. NUL-separated output is one
// line, so a repository's whole file list reached Loki as a single entry and
// was cut mid-path at 64KB without a word (infra #10719): 65,496 B on ourea,
// 65,534 B on mnemosyne. dagger echoes an exec's stdout into progress whatever
// is done with it — RedirectStdout was measured and still echoes — so the only
// fix that keeps the list off one line is to stop asking for one line.
//
// A NEWLINE IN A PATH IS STILL SAFE, which is the reason `-z` existed. git
// quotes any path holding a control character, a quote, a backslash or a
// non-ASCII byte (core.quotePath, default true), and the escape for a newline
// is `\n` INSIDE the quotes — so a quoted path never spans two lines and the
// line remains the record separator.
//
// THE ESCAPES DECODE TO BYTES, NOT RUNES, and that is the whole reason this is
// hand-written rather than strconv.Unquote. git emits a non-ASCII path as one
// octal escape PER BYTE of its UTF-8, so `café.py` arrives as
// "caf\303\251.py"; strconv.Unquote reads \303 as the rune U+00C3 and answers
// "cafÃ©.py". Decoding to bytes and converting once at the end round-trips the
// path git actually named.
func SplitGitPaths(listing string) ([]string, error) {
	var out []string
	for _, line := range strings.Split(listing, "\n") {
		if line == "" {
			continue
		}
		p, err := unquoteGitPath(line)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// unquoteGitPath decodes one listing line. An unquoted line is its own path.
func unquoteGitPath(line string) (string, error) {
	if len(line) < 2 || line[0] != '"' || line[len(line)-1] != '"' {
		return line, nil
	}
	body := line[1 : len(line)-1]
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' {
			out = append(out, body[i])
			continue
		}
		i++
		if i >= len(body) {
			return "", fmt.Errorf("git path %s ends in a backslash", line)
		}
		switch c := body[i]; c {
		case 'a':
			out = append(out, 0x07)
		case 'b':
			out = append(out, 0x08)
		case 't':
			out = append(out, '\t')
		case 'n':
			out = append(out, '\n')
		case 'v':
			out = append(out, 0x0b)
		case 'f':
			out = append(out, 0x0c)
		case 'r':
			out = append(out, '\r')
		case '"', '\\':
			out = append(out, c)
		default:
			// Three octal digits, one byte — git's form for everything else.
			//
			// THE LEADING DIGIT STOPS AT '3' BECAUSE A BYTE DOES. git escapes
			// bytes, so the range it emits is \000 to \377 and nothing above:
			// \400 is 256. Accepting '4'–'7' here and range-checking the total
			// afterwards would be a second gate on a value the first one has
			// already made impossible — and the mutation gate said so, by
			// surviving a `> '7'` → `>= '7'` mutant that no input could tell
			// apart (every \7xx is at least 448, rejected either way).
			if c < '0' || c > '3' || i+2 >= len(body) {
				return "", fmt.Errorf("git path %s carries an escape this parser does not know: \\%c", line, c)
			}
			var b int
			for _, d := range body[i : i+3] {
				if d < '0' || d > '7' {
					return "", fmt.Errorf("git path %s carries a malformed octal escape", line)
				}
				b = b*8 + int(d-'0')
			}
			out = append(out, byte(b))
			i += 2
		}
	}
	return string(out), nil
}

// SJTreeFinding is one suppression ScanTree found.
type SJTreeFinding struct {
	Rel        string
	Line       int
	Tool, Form string
	Text       string
}

// ScanTree runs the suppression scan over a tree that is not a repository —
// the render matrix's rendered output — and answers its findings in path
// order.
//
// ONE DEFINITION, NOT A SECOND COPY OF THE RULE: this is the scan the gate
// runs, over files instead of a checkout. It differs from the gate in the two
// ways a rendered tree demands: nothing is excluded (a rendered .claude/ is
// still poured into every repo born from the template), and no DirectoryExempt
// row can apply, because the tree belongs to no repository yet. The skip-vs-
// fail contract and the marker audit apply exactly as they do on a checkout.
func ScanTree(files map[string]string) ([]SJTreeFinding, error) {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	s := &sjScan{
		in:          SJInput{Tracked: paths, Read: func(rel string) (string, error) { return files[rel], nil }},
		contracts:   map[string][]sjContract{},
		packageVars: map[string]map[string]bool{},
	}
	for _, rel := range paths {
		lang := LanguageOf(rel)
		if lang == "" {
			continue
		}
		lines, err := s.read(rel)
		if err != nil {
			return nil, err
		}
		if err := s.scanFile(rel, lang, lines); err != nil {
			return nil, err
		}
	}
	out := make([]SJTreeFinding, 0, len(s.findings))
	for _, f := range s.findings {
		out = append(out, SJTreeFinding{Rel: f.rel, Line: f.line, Tool: f.tool, Form: f.form, Text: f.text})
	}
	return out, nil
}

// reportKubeLinter prints what the tree and .kube-linter.yaml say about
// kube-linter's per-object escape hatch, and answers how many of them are
// findings.
//
// THE RULE IS PARITY. Every ignore-check.kube-linter.io annotation is declared
// in .kube-linter.yaml's comment inventory, per workload. An annotation the
// header does not account for is a suppression filed silently, which is the
// one thing this check exists to make impossible — so it counts.
//
// AND checks.exclude IS ALWAYS A FINDING. It silences a check for the whole
// repository, so it cannot be argued per object; it needs Rob's signoff and a
// ratified row, exactly like any other repo-scale silencer. rob/infra ships it
// empty on purpose.
func (s *sjScan) reportKubeLinter(w *strings.Builder) bool {
	if len(s.klIgnores) == 0 && len(s.klExcluded) == 0 {
		return false
	}
	bad := false
	if len(s.klExcluded) > 0 {
		fmt.Fprintf(w, "\nstop-justifications: %d kube-linter check(s) silenced REPO-WIDE by %s.\n", len(s.klExcluded), s.klConfig)
		for _, c := range s.klExcluded {
			fmt.Fprintf(w, "  checks.exclude: %s\n", c)
		}
		w.WriteString("  A repo-wide exclude is not a reason about one object, it is the\n" +
			"  check turned off. Measured in rob/infra: excluding\n" +
			"  no-extensions-v1beta also hid a REAL extensions/v1beta1 Ingress\n" +
			"  planted to test it. Needs a signoff and a ratified row.\n\n")
		bad = true
	}
	if len(s.klIgnores) == 0 {
		return bad
	}
	if s.klConfig == "" {
		fmt.Fprintf(w, "\nstop-justifications: %d kube-linter ignore annotation(s) and NO .kube-linter.yaml to declare them.\n", len(s.klIgnores))
		for _, ig := range s.klIgnores {
			fmt.Fprintf(w, "  %s · %s\n", ig.rel, ig.check)
		}
		w.WriteString("  Each is a suppression of a fleet gate with nowhere that records it.\n\n")
		return true
	}
	declared := len(s.klInventory)
	fmt.Fprintf(w, "\nstop-justifications: %d kube-linter ignore annotation(s), %d declared in %s.\n", len(s.klIgnores), declared, s.klConfig)
	if declared == len(s.klIgnores) {
		w.WriteString("  The tree and the header agree — every annotation is accounted for.\n\n")
		return bad
	}
	fmt.Fprintf(w, "  MISMATCH. The header must name every one, per workload.\n")
	for _, ig := range s.klIgnores {
		fmt.Fprintf(w, "  %s · %s\n", ig.rel, ig.check)
	}
	w.WriteString("  An annotation the inventory does not account for is the silent\n" +
		"  filing this check exists to prevent. A row with no annotation left to\n" +
		"  name is just as wrong: it is an excuse outliving the thing excused.\n\n")
	// A BOOL, NOT A COUNT, and the mutation gate is why. The first cut
	// returned abs(annotations - declared); three mutants lived on arithmetic
	// nothing reads, because the only question asked of this value is whether
	// it is zero. A delta was the wrong number anyway — two added and one row
	// deleted nets to one, while the condition is "the header is not the
	// record", which has no magnitude.
	return true
}
