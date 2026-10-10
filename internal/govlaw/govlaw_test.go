// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package govlaw

import (
	"errors"
	"sort"
	"strings"
	"testing"
)

const ledger = `[retired.tethys_pnl]
star = "tethys"
successor = 'model_pnl(by="job")'
retired_by = "F12"

[retired.fleet_get_checkpoint]
star = "fleet"
successor = "session_latest_checkpoint(...)"
retired_by = "F3"
`

// stocks is a foundry-stocks-shaped tree: the kit library and the ledger, plus
// whatever the test adds. A value in files overrides the default for its path.
func stocks(files map[string]string) Tree {
	all := map[string]string{
		"kits.toml":                  "[kits.root]\nblocks = [\"alpha\"]\n",
		"retired-verbs.toml":         ledger,
		"governance/blocks/alpha.md": "alpha text\n",
	}
	for k, v := range files {
		all[k] = v
	}
	return mapTree(all, nil)
}

// mapTree reads from a map; fail names paths whose read errors.
func mapTree(files map[string]string, fail map[string]bool) Tree {
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return Tree{Paths: paths, Read: func(p string) (string, error) {
		if fail[p] {
			return "", errors.New("i/o error")
		}
		return files[p], nil
	}}
}

// without drops paths from a tree's listing but not its reader.
func without(t Tree, drop ...string) Tree {
	var keep []string
	for _, p := range t.Paths {
		skip := false
		for _, d := range drop {
			if p == d {
				skip = true
			}
		}
		if !skip {
			keep = append(keep, p)
		}
	}
	t.Paths = keep
	return t
}

func want(t *testing.T, r Result, state int, parts ...string) {
	t.Helper()
	if r.State != state {
		t.Errorf("state %d, want %d:\n%s", r.State, state, r.Report)
	}
	for _, p := range parts {
		if !strings.Contains(r.Report, p) {
			t.Errorf("report lacks %q:\n%s", p, r.Report)
		}
	}
}

func wantNot(t *testing.T, r Result, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(r.Report, p) {
			t.Errorf("report holds %q:\n%s", p, r.Report)
		}
	}
}

func TestIsStocksNeedsBothFiles(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"neither": {"README.md": "x"},
		"kits":    {"kits.toml": ""},
		"ledger":  {"retired-verbs.toml": ledger},
	} {
		if IsStocks(mapTree(files, nil)) {
			t.Errorf("%s alone is not foundry-stocks", name)
		}
	}
	if !IsStocks(stocks(nil)) {
		t.Error("kits.toml beside retired-verbs.toml is foundry-stocks")
	}
}

func TestEveryLaneIsAbsentOffFoundryStocks(t *testing.T) {
	other := mapTree(map[string]string{
		"governance/blocks/Bad_Name.md": "",
		"retired-verbs.toml":            ledger,
		"renders/c/claude/AGENTS.md":    "text",
	}, nil)
	for id, run := range map[string]func(Tree) Result{LintID: Lint, VerbsID: VerbLiveness, BudgetID: Budget} {
		r := run(other)
		want(t, r, StatePass, id+": ABSENT", "not foundry-stocks")
	}
}

func TestVerdictFoldsFindingsAndWarnings(t *testing.T) {
	r := verdict("x:y", "all well", []string{"b", "a"}, []string{"w2", "w1"})
	want(t, r, StateFindings, "x:y: FINDINGS - 2 problem(s)", "WARNINGS (2, not gating)")
	if strings.Index(r.Report, "  a\n") > strings.Index(r.Report, "  b\n") || strings.Index(r.Report, "w1") > strings.Index(r.Report, "w2") {
		t.Errorf("not sorted:\n%s", r.Report)
	}
	wantNot(t, r, "all well")
	if strings.HasSuffix(r.Report, "\n") {
		t.Errorf("the report ends in a newline: %q", r.Report)
	}
	r = verdict("x:y", "all well", nil, nil)
	if r.State != StatePass || r.Report != "x:y: all well" {
		t.Errorf("clean: %+v", r)
	}
	r = verdict("x:y", "all well", nil, []string{"w"})
	want(t, r, StatePass, "x:y: all well", "WARNINGS (1, not gating)", "  w")
	if strings.HasSuffix(r.Report, "\n") {
		t.Errorf("the report ends in a newline: %q", r.Report)
	}
}

// ---- law:lint ----

func TestLintPassesSoundLaw(t *testing.T) {
	r := Lint(stocks(map[string]string{
		"bundles.toml":                "[bundles.b]\nskills = [\"s\"]\n",
		"kits.toml":                   "[kits.a]\nblocks = [\"alpha\"]\nincludes = [\"b\", \"c\"]\n[kits.c]\nincludes = [\"b\"]\n",
		"governance/blocks/beta-2.md": "beta\n",
	}))
	want(t, r, StatePass, "2 block(s), 2 kit(s) and 0 span map(s) are sound")
	wantNot(t, r, "WARNINGS")
}

func TestLintRefusesALibraryWithNoBlocks(t *testing.T) {
	r := Lint(without(stocks(nil), "governance/blocks/alpha.md"))
	want(t, r, StateCannotRun, "CANNOT RUN", "the block library moved")
}

func TestLintBlocksAreNonEmptyAndStemsAreSlugs(t *testing.T) {
	r := Lint(stocks(map[string]string{
		"governance/blocks/blank.md":     " \n\t\n",
		"governance/blocks/Bad_Name.md":  "text",
		"governance/blocks/-lead.md":     "text",
		"governance/blocks/double--x.md": "text",
		"governance/blocks/nested/x.md":  "",
		"governance/blocks/notes.txt":    "",
	}))
	want(t, r, StateFindings,
		"governance/blocks/blank.md: the block is empty",
		`governance/blocks/Bad_Name.md: the stem "Bad_Name" is not a slug`,
		`the stem "-lead" is not a slug`,
		`the stem "double--x" is not a slug`)
	wantNot(t, r, "alpha.md", "nested", "notes.txt", "blank.md: the stem")
}

func TestLintRefusesABlockItCannotRead(t *testing.T) {
	tr := stocks(nil)
	base := tr.Read
	tr.Read = func(p string) (string, error) {
		if p == "governance/blocks/alpha.md" {
			return "", errors.New("i/o error")
		}
		return base(p)
	}
	want(t, Lint(tr), StateCannotRun, "governance/blocks/alpha.md would not read", "i/o error")
}

func TestLintKitsNameOnlyExistingBlocks(t *testing.T) {
	r := Lint(stocks(map[string]string{
		"kits.toml": "[kits.a]\nblocks = [\"alpha\", \"ghost\"]\n[kits.b]\nblocks = [\"phantom\"]\n",
	}))
	want(t, r, StateFindings,
		`kits.toml: kit "a" names block "ghost"`,
		`kits.toml: kit "b" names block "phantom"`)
	wantNot(t, r, `"alpha", which`, `block "alpha"`)
}

func TestLintUnknownIncludeWarnsAndKnownDoesNot(t *testing.T) {
	r := Lint(stocks(map[string]string{
		"bundles.toml": "[bundles.real]\nskills = []\n",
		"kits.toml":    "[kits.a]\nincludes = [\"real\", \"b\", \"gone\"]\n[kits.b]\n",
	}))
	want(t, r, StatePass, "WARNINGS (1, not gating)", `kit "a" includes "gone", which is neither a kit nor a bundle`)
	wantNot(t, r, `includes "real"`, `includes "b"`)
}

func TestLintUnknownIncludeWithNoBundleLibrary(t *testing.T) {
	r := Lint(stocks(map[string]string{"kits.toml": "[kits.a]\nincludes = [\"b\"]\n"}))
	want(t, r, StatePass, `includes "b"`)
}

func TestLintCyclesAreErrors(t *testing.T) {
	r := Lint(stocks(map[string]string{
		"kits.toml": "[kits.a]\nincludes = [\"b\"]\n[kits.b]\nincludes = [\"c\"]\n[kits.c]\nincludes = [\"a\"]\n[kits.s]\nincludes = [\"s\"]\n[kits.ok]\nincludes = [\"a\", \"x\"]\n",
	}))
	want(t, r, StateFindings,
		"kits include each other in a cycle: a -> b -> c -> a",
		"kits include each other in a cycle: s -> s")
	if n := strings.Count(r.Report, "in a cycle"); n != 2 {
		t.Errorf("%d cycle reports, want 2 (each once):\n%s", n, r.Report)
	}
}

func TestIncludeCyclesWalksSharedDescendantsOnce(t *testing.T) {
	// A diamond is not a cycle: d is reached twice and finished the first time.
	g := map[string][]string{"a": {"b", "c"}, "b": {"d"}, "c": {"d"}, "d": nil}
	if got := includeCycles(g); len(got) != 0 {
		t.Errorf("a diamond is not a cycle: %v", got)
	}
	// A cycle that does not pass through the root is reported from where it closes.
	g = map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"b"}}
	got := includeCycles(g)
	if len(got) != 1 || strings.Join(got[0], ",") != "b,c,b" {
		t.Errorf("got %v, want [b c b]", got)
	}
	// A bundle (not a key) is a leaf, never walked.
	if got := includeCycles(map[string][]string{"a": {"bundle"}}); len(got) != 0 {
		t.Errorf("a bundle include is a leaf: %v", got)
	}
}

func TestLintRefusesLibrariesItCannotUse(t *testing.T) {
	for _, c := range []struct {
		name  string
		files map[string]string
		fail  map[string]bool
		want  string
	}{
		{"kits not toml", map[string]string{"kits.toml": "[kits.a\n"}, nil, "kits.toml is not a kit library"},
		{"kits wrong shape", map[string]string{"kits.toml": "[kits.a]\nblocks = \"alpha\"\n"}, nil, "kits.toml is not a kit library"},
		{"kits unreadable", nil, map[string]bool{"kits.toml": true}, "kits.toml would not read"},
		{"bundles not toml", map[string]string{"bundles.toml": "[bundles.a\n"}, nil, "bundles.toml is not a bundle library"},
		{"bundles unreadable", map[string]string{"bundles.toml": ""}, map[string]bool{"bundles.toml": true}, "bundles.toml would not read"},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{
				"kits.toml": "[kits.root]\n", "retired-verbs.toml": ledger, "governance/blocks/alpha.md": "x",
			}
			for k, v := range c.files {
				files[k] = v
			}
			want(t, Lint(mapTree(files, c.fail)), StateCannotRun, "CANNOT RUN", c.want)
		})
	}
}

const goodDoc = "alpha text\n\nbeta text\n"

func renderTree(spans, doc string, extra map[string]string) Tree {
	files := map[string]string{
		"governance/blocks/beta.md":            "beta text\n",
		"renders/c/claude/AGENTS.md":           doc,
		"renders/c/claude/.furnace/spans.json": spans,
	}
	for k, v := range extra {
		files[k] = v
	}
	return stocks(files)
}

func TestLintAcceptsASpanMapThatCoversItsDocument(t *testing.T) {
	spans := `[{"end_byte":10,"position":0,"slug":"alpha","start_byte":0},{"end_byte":22,"position":0,"slug":"beta","start_byte":12}]` + "\n"
	r := Lint(renderTree(spans, goodDoc, nil))
	want(t, r, StatePass, "1 span map(s) are sound")
	// Spans may sit flush, and the last may end exactly at the document's end.
	flush := `[{"end_byte":10,"slug":"alpha","start_byte":0},{"end_byte":22,"slug":"beta","start_byte":10}]`
	want(t, Lint(renderTree(flush, goodDoc, nil)), StatePass)
	want(t, Lint(renderTree(flush, goodDoc[:21], nil)), StateFindings, "ends at byte 22, past the document's 21")
}

func TestLintGradesEachSpanRule(t *testing.T) {
	for _, c := range []struct{ name, spans, want string }{
		{"not json", `{`, "does not parse as a list of spans"},
		{"not a list", `{"slug":"alpha"}`, "does not parse as a list of spans"},
		{"empty", `[]`, "maps no span of a document that has text"},
		{"no slug", `[{"start_byte":0,"end_byte":4}]`, "span 1 lacks slug, start_byte or end_byte"},
		{"no start", `[{"slug":"alpha","end_byte":4}]`, "span 1 lacks slug, start_byte or end_byte"},
		{"no end", `[{"slug":"alpha","start_byte":0}]`, "span 1 lacks slug, start_byte or end_byte"},
		{"empty range", `[{"slug":"alpha","start_byte":3,"end_byte":3}]`, "(alpha) is not a range: 3..3"},
		{"backwards", `[{"slug":"alpha","start_byte":5,"end_byte":3}]`, "(alpha) is not a range: 5..3"},
		{"negative", `[{"slug":"alpha","start_byte":-1,"end_byte":3}]`, "(alpha) is not a range: -1..3"},
		{"past the end", `[{"slug":"alpha","start_byte":0,"end_byte":23}]`, "ends at byte 23, past the document's 22"},
		{"overlap", `[{"slug":"alpha","start_byte":0,"end_byte":10},{"slug":"beta","start_byte":9,"end_byte":22}]`, "span 2 (beta) starts at byte 9, inside the span before it (ends at 10)"},
		{"out of order", `[{"slug":"beta","start_byte":12,"end_byte":22},{"slug":"alpha","start_byte":0,"end_byte":10}]`, "span 2 (alpha) starts at byte 0, inside the span before it (ends at 22)"},
		{"unknown block", `[{"slug":"ghost","start_byte":0,"end_byte":10}]`, `span 1 names block "ghost"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			want(t, Lint(renderTree(c.spans, goodDoc, nil)), StateFindings, "renders/c/claude/.furnace/spans.json", c.want)
		})
	}
}

func TestLintSpanOverlapTracksTheFurthestEnd(t *testing.T) {
	// A short span inside a long one must not shrink the running end.
	spans := `[{"slug":"alpha","start_byte":0,"end_byte":10},{"slug":"alpha","start_byte":2,"end_byte":4},{"slug":"beta","start_byte":8,"end_byte":15}]`
	r := Lint(renderTree(spans, goodDoc, nil))
	want(t, r, StateFindings, "span 2 (alpha) starts at byte 2, inside the span before it (ends at 10)", "span 3 (beta) starts at byte 8, inside the span before it (ends at 10)")
}

func TestLintAMapWithNoDocumentIsAFinding(t *testing.T) {
	tr := without(renderTree(`[{"slug":"alpha","start_byte":0,"end_byte":3}]`, "x", nil), "renders/c/claude/AGENTS.md")
	want(t, Lint(tr), StateFindings, "maps a context document that is not here (renders/c/claude/AGENTS.md)")
}

func TestLintADocumentWithTextAndNoMapIsAFinding(t *testing.T) {
	tr := without(renderTree("", goodDoc, nil), "renders/c/claude/.furnace/spans.json")
	want(t, Lint(tr), StateFindings, "renders/c/claude/AGENTS.md: has text and no .furnace/spans.json beside it")
	// A blank document (a kit with no blocks) needs no map, and a documented
	// render with its map is not reported as lacking one.
	blank := without(renderTree("", "\n", nil), "renders/c/claude/.furnace/spans.json")
	want(t, Lint(blank), StatePass)
	mapped := renderTree(`[{"slug":"alpha","start_byte":0,"end_byte":10}]`, goodDoc, nil)
	wantNot(t, Lint(mapped), "has text and no")
}

func TestLintRefusesRendersItCannotRead(t *testing.T) {
	spans := `[{"slug":"alpha","start_byte":0,"end_byte":10}]`
	for _, p := range []string{"renders/c/claude/AGENTS.md", "renders/c/claude/.furnace/spans.json"} {
		files := map[string]string{
			"kits.toml": "[kits.root]\n", "retired-verbs.toml": ledger, "governance/blocks/alpha.md": "x",
			"renders/c/claude/AGENTS.md": goodDoc, "renders/c/claude/.furnace/spans.json": spans,
		}
		want(t, Lint(mapTree(files, map[string]bool{p: true})), StateCannotRun, p+" would not read", "i/o error")
	}
	// A document with no map is read to see whether it is blank.
	files := map[string]string{
		"kits.toml": "[kits.root]\n", "retired-verbs.toml": ledger, "governance/blocks/alpha.md": "x",
		"renders/c/claude/AGENTS.md": goodDoc,
	}
	want(t, Lint(mapTree(files, map[string]bool{"renders/c/claude/AGENTS.md": true})), StateCannotRun, "renders/c/claude/AGENTS.md would not read")
}

func TestLintReadsEveryRender(t *testing.T) {
	good := `[{"slug":"alpha","start_byte":0,"end_byte":10}]`
	r := Lint(renderTree(good, goodDoc, map[string]string{
		"renders/d/pi/AGENTS.md":             goodDoc,
		"renders/d/pi/.furnace/spans.json":   `[{"slug":"alpha","start_byte":0,"end_byte":99}]`,
		"renders/e/claude/AGENTS.md":         goodDoc,
		"renders/e/claude/CLAUDE.md":         "shim",
		"renders/e/claude/skills/x/SKILL.md": "x",
	}))
	want(t, r, StateFindings,
		"renders/d/pi/.furnace/spans.json: span 1 (alpha) ends at byte 99",
		"renders/e/claude/AGENTS.md: has text and no")
	wantNot(t, r, "renders/c/claude", "CLAUDE.md", "SKILL.md")
}

func TestKitsDeclaredWithoutBlocksAreFine(t *testing.T) {
	want(t, Lint(stocks(map[string]string{"kits.toml": "[kits.empty]\n[kits.\"governance/smoke\"]\nblocks = [\"alpha\"]\n"})), StatePass, "2 kit(s)")
}

var errBoom = errors.New("i/o error")
