// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package retiredverbs

import (
	"reflect"
	"strings"
	"testing"
)

const ledger = `# a comment
[retired.ourea_pr]
star = "ourea"
successor = 'git_pr(...)'
retired_by = "the prefix rename"

[retired.tethys_pnl]
star = "tethys"
successor = "model_pnl(by=...)"
retired_by = "F12"
`

func mustParse(t *testing.T, body string) Ledger {
	t.Helper()
	l, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return l
}

func TestParseReadsEveryEntry(t *testing.T) {
	l := mustParse(t, ledger)
	if got := Names(l); !reflect.DeepEqual(got, []string{"ourea_pr", "tethys_pnl"}) {
		t.Fatalf("names %v", got)
	}
	want := Entry{Star: "ourea", Successor: "git_pr(...)", RetiredBy: "the prefix rename"}
	if l["ourea_pr"] != want {
		t.Errorf("entry %+v, want %+v", l["ourea_pr"], want)
	}
}

func TestParseRefuses(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"not toml", "[retired.x\n", "not TOML"},
		{"no entries", "# nothing\n", "no [retired.<name>] entry"},
		{"an empty table", "[retired]\n", "no [retired.<name>] entry"},
		{"no successor", "[retired.a_b]\nstar = \"a\"\nretired_by = \"x\"\n", "[retired.a_b] names no successor"},
		{"a blank successor", "[retired.a_b]\nstar = \"a\"\nsuccessor = \"  \"\n", "[retired.a_b] names no successor"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, err := Parse(c.body)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want it to contain %q", err, c.want)
			}
			if l != nil {
				t.Errorf("a refused ledger was returned: %v", l)
			}
		})
	}
}

func TestScanned(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"skills/todo/skill.toml", true},
		{"./skills/todo/todo.md", true},
		{"agents/a.md", true},
		{"commands/c.md", true},
		{"hooks/git_guard/git_guard.py", true},
		{"settings/base.json", true},
		{"governance/blocks/forge.md", true},
		{"rules/worktree-check.md", true},
		{"skills/todo/run.sh", false},
		{"skills", false},
		{"skills.md", false},
		{"retired-verbs.toml", false},
		{"specs/001/plan.md", false},
		{"tests/skills/x.md", false},
		{"skillsets/x.md", false},
	}
	for _, c := range cases {
		if got := Scanned(c.path); got != c.want {
			t.Errorf("Scanned(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestFindMatchesAWholeNameOnly(t *testing.T) {
	l := mustParse(t, ledger)
	cases := []struct {
		name, line string
		want       bool
	}{
		{"alone", "ourea_pr", true},
		{"at the start", "ourea_pr opens a pull", true},
		{"at the end", "call ourea_pr", true},
		{"in backticks", "call `ourea_pr` now", true},
		{"called", "ourea_pr(action=\"open\")", true},
		{"in a json string", `"allowed": ["ourea_pr"]`, true},
		{"the session spelling", "mcp__hades__ourea_pr", true},
		{"the session spelling mid-line", "tools: Read, mcp__hades__ourea_pr, Edit", true},
		{"a longer name on the right", "ourea_pr_node_mint_total", false},
		{"a longer name on the right, digit", "ourea_pr2", false},
		{"a longer name on the left", "my_ourea_pr", false},
		{"a longer name on the left, letter", "xourea_pr", false},
		{"a longer name on the left, digit", "9ourea_pr", false},
		{"one underscore is not the session prefix", "_ourea_pr", false},
		{"a miss then a hit", "ourea_pr_total and ourea_pr", true},
		{"a hit after a left miss", "xourea_pr ourea_pr", true},
		{"upper case on the right", "ourea_prX", false},
		{"not there", "git_pr", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hits := Find("p", c.line, l)
			if got := len(hits) == 1; got != c.want {
				t.Fatalf("Find(%q) = %v, want a hit: %v", c.line, hits, c.want)
			}
			if c.want && hits[0] != (Hit{Path: "p", Line: 1, Verb: "ourea_pr"}) {
				t.Errorf("hit %+v", hits[0])
			}
		})
	}
}

func TestFindReportsLinesAndEveryVerb(t *testing.T) {
	l := mustParse(t, ledger)
	text := "nothing here\ncall tethys_pnl then ourea_pr\n\nourea_pr twice ourea_pr\n"
	got := Find("skills/x.md", text, l)
	want := []Hit{
		{Path: "skills/x.md", Line: 2, Verb: "ourea_pr"},
		{Path: "skills/x.md", Line: 2, Verb: "tethys_pnl"},
		{Path: "skills/x.md", Line: 4, Verb: "ourea_pr"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hits %+v\nwant %+v", got, want)
	}
}

func TestFindOnACleanTextIsEmpty(t *testing.T) {
	if hits := Find("p", "git_pr and model_pnl\n", mustParse(t, ledger)); len(hits) != 0 {
		t.Errorf("hits %+v", hits)
	}
}
