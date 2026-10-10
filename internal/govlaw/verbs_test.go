// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package govlaw

import (
	"strings"
	"testing"
)

// forgeBlock is the text of foundry-stocks' governance/blocks/forge.md section
// that names verbs, as it stood on 2026-10-09: the current law the lane must
// pass.
const forgeBlock = "## GitOps\n\n" +
	"- **CI logs** — `repo_ci_logs` (Daedalus's CI record) through the Hades gateway, never a git-host REST API; `sha` alone resolves the run for `repo_ci_logs`; `repo_ci_await` needs `repo` + `sha` (or `v7`) and refuses a bare sha.\n" +
	"- **Tasks** — `plan_task_update` ends a node; `/todo` pins on the inbox, which Casper promotes through `plan_quick_add`.\n" +
	"- **Doors** — `git_pr open` lands on green; `git_merge` is the manual landing; the `git_guard` hook enforces it.\n" +
	"- **Memory** — `mnemosyne_store` at capture; `rhyme_recall(cue=...)` to read; `mcp__hades__eros_search` for semantic search.\n"

func TestVerbLivenessIsGreenOnTheCurrentBlocks(t *testing.T) {
	r := VerbLiveness(stocks(map[string]string{"governance/blocks/forge.md": forgeBlock}))
	want(t, r, StatePass, "9 verb mention(s) in 2 unit(s) are all served by hades (the snapshot embedded in foundry-tools) and none is retired")
}

func TestVerbLivenessIsRedOnTheAugustText(t *testing.T) {
	august := forgeBlock + "- **Backlog** — `athena_quick_add` pins a task; `mcp__hades__athena_task_update` closes it.\n"
	r := VerbLiveness(stocks(map[string]string{"governance/blocks/forge.md": august}))
	want(t, r, StateFindings,
		"governance/blocks/forge.md:7  athena_quick_add is not a verb on hades's surface",
		"governance/blocks/forge.md:7  athena_task_update is not a verb on hades's surface")
	wantNot(t, r, "repo_ci_logs", "git_guard", "rhyme_recall")
}

func TestMentionsReadsQualifiedAndBareNames(t *testing.T) {
	s, err := ParseSurface(`verbs = ["plan_quick_add", "git_pr", "pass"]
former_prefixes = ["athena"]
not_verbs = ["git_guard"]`)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		line string
		want []Mention
	}{
		{"nothing here", nil},
		{"mcp__hades__plan_quick_add and mcp__hades__pass.", []Mention{{"plan_quick_add", false}, {"pass", false}}},
		{"mcp__hades__athena*", []Mention{{"athena", true}}},
		{"mcp__hades__athena_*", []Mention{{"athena", true}}},
		{"mcp__hades__athena__", []Mention{{"athena", true}}},
		{"mcp__vault-mcp__write_note", nil},
		{"mcp__paneless__canvas_show is another server", nil},
		{"`plan_quick_add` and `git_pr open` and `athena_x(arg=1)`", []Mention{{"plan_quick_add", false}, {"git_pr", false}, {"athena_x", false}}},
		{"`git_guard` is a hook, `rhyme_recall` has no star, `Plan_X` is not a name", nil},
		{"`plan` is one word, `plan_` ends open, `plan__x` is not a name, `plan_QuickAdd` neither", nil},
		{"plan_quick_add outside backticks is prose", nil},
		{"``` fenced ```", nil},
		{"` plan_quick_add `", []Mention{{"plan_quick_add", false}}},
		{"`a b` `git_pr_x2` `git_9`", []Mention{{"git_pr_x2", false}, {"git_9", false}}},
	} {
		got := s.Mentions(c.line)
		if len(got) != len(c.want) {
			t.Errorf("%q: got %v, want %v", c.line, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q: got %v, want %v", c.line, got, c.want)
			}
		}
	}
}

func TestSurfaceLiveness(t *testing.T) {
	s, err := ParseSurface(`verbs = ["plan_quick_add", "git_pr", "calliope_look"]`)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		glob bool
		live bool
	}{
		{"plan_quick_add", false, true},
		{"plan_quick", false, false},
		{"plan", false, false},
		{"plan", true, true},
		{"plan_quick_add", true, true},
		{"git_pr", true, true},
		{"pla", true, false},
		{"athena", true, false},
		{"calliope_loo", true, false},
	} {
		if got := s.live(c.name, c.glob); got != c.live {
			t.Errorf("live(%q, glob=%v) = %v, want %v", c.name, c.glob, got, c.live)
		}
	}
}

func TestParseSurfaceRefusesWhatItCannotGradeAgainst(t *testing.T) {
	for body, wantErr := range map[string]string{
		"verbs = [":                      "not TOML",
		"":                               "names no verb",
		"former_prefixes = [\"athena\"]": "names no verb",
		`verbs = ["Plan_Quick"]`:         `verb "Plan_Quick" is not a wire name`,
		`verbs = ["ok_one", "bad name"]`: `verb "bad name" is not a wire name`,
		`verbs = ["_leading"]`:           `verb "_leading" is not a wire name`,
	} {
		_, err := ParseSurface(body)
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("%q: err %v, want %q", body, err, wantErr)
		}
	}
}

func TestTheEmbeddedSnapshotParsesAndKnowsTheSurface(t *testing.T) {
	s, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"plan_quick_add", "repo_ci_logs", "repo_ci_await", "eros_search", "mnemosyne_store", "git_pr", "git_merge", "whoami"} {
		if !s.live(v, false) {
			t.Errorf("the snapshot does not serve %s", v)
		}
	}
	for _, v := range []string{"athena_quick_add", "tethys_pnl", "fleet_get_checkpoint"} {
		if s.live(v, false) {
			t.Errorf("the snapshot serves %s, which left the surface", v)
		}
	}
	for _, p := range []string{"plan", "athena", "calliope"} {
		if !s.prefixes[p] {
			t.Errorf("prefix %s is not known", p)
		}
	}
	if !s.notVerbs["git_guard"] {
		t.Error("git_guard is a hook, not a verb")
	}
	// The snapshot carries no duplicate and is sorted, so a refresh is a diff.
	var prev string
	for _, line := range strings.Split(embeddedSurface, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `"`) || !strings.HasSuffix(line, `",`) || strings.Contains(line, " ") {
			continue
		}
		if line <= prev && prev != "" {
			t.Errorf("verbs are not sorted and unique at %s (after %s)", line, prev)
		}
		prev = line
	}
}

func TestRetiredNamesAreReportedOnceAsRetired(t *testing.T) {
	r := VerbLiveness(stocks(map[string]string{
		"governance/blocks/forge.md": "call `tethys_pnl` or mcp__hades__fleet_get_checkpoint here\nand `fleet_get_checkpoint` again\n",
	}))
	want(t, r, StateFindings,
		`governance/blocks/forge.md:1  tethys_pnl is retired; call instead: model_pnl(by="job")`,
		"governance/blocks/forge.md:1  fleet_get_checkpoint is retired; call instead: session_latest_checkpoint(...)",
		"governance/blocks/forge.md:2  fleet_get_checkpoint is retired")
	wantNot(t, r, "is not a verb on hades's surface")
	if n := strings.Count(r.Report, "\n  "); n != 3 {
		t.Errorf("%d finding lines, want 3:\n%s", n, r.Report)
	}
}

func TestARetiredNameOnAnotherLineIsStillNotServed(t *testing.T) {
	// The retired hit on line 1 must not excuse the same name, as a mention, on line 2
	// when it is not in the ledger for that line - and a name outside the ledger is judged alone.
	r := VerbLiveness(stocks(map[string]string{
		"governance/blocks/forge.md": "prose names tethys_pnl bare\n`athena_x`\n",
	}))
	want(t, r, StateFindings, "forge.md:1  tethys_pnl is retired", "forge.md:2  athena_x is not a verb")
	wantNot(t, r, "forge.md:1  tethys_pnl is not a verb")
}

func TestVerbLivenessReadsRendersAndOnlyTheAlwaysOnDocuments(t *testing.T) {
	r := VerbLiveness(stocks(map[string]string{
		"renders/c/claude/AGENTS.md":         "ok `git_pr`\nbad `athena_one`\n",
		"renders/c/claude/CLAUDE.md":         "shim `athena_two`\n",
		"renders/c/pi/APPEND_SYSTEM.md":      "persona `athena_three`\n",
		"renders/c/claude/skills/x/SKILL.md": "`athena_four`\n",
		"renders/c/claude/AGENTS.md.bak":     "`athena_five`\n",
		"renders/AGENTS.md":                  "`athena_six`\n",
		"governance/blocks/sub/deeper.md":    "`athena_seven`\n",
		"governance/other.md":                "`athena_eight`\n",
	}))
	want(t, r, StateFindings,
		"renders/c/claude/AGENTS.md:2  athena_one",
		"renders/c/claude/CLAUDE.md:1  athena_two",
		"renders/c/pi/APPEND_SYSTEM.md:1  athena_three")
	wantNot(t, r, "athena_four", "athena_five", "athena_six", "athena_seven", "athena_eight")
}

func TestVerbLivenessPrefersTheTreesOwnSurface(t *testing.T) {
	own := `verbs = ["brand_new_verb"]`
	r := VerbLiveness(stocks(map[string]string{
		"verb-surface.toml":          own,
		"governance/blocks/forge.md": "`brand_new_verb` and `plan_quick_add`\n",
	}))
	// plan_quick_add shares no prefix with this surface, so it is not read as a verb at all.
	want(t, r, StatePass, "(verb-surface.toml)", "1 verb mention(s)")
	r = VerbLiveness(stocks(map[string]string{
		"verb-surface.toml":          own,
		"governance/blocks/forge.md": "`brand_other_verb`\n",
	}))
	want(t, r, StateFindings, "brand_other_verb is not a verb on hades's surface (verb-surface.toml)")
}

func TestRetiredPrefixesMakeBareNamesVerbs(t *testing.T) {
	// `tethys` is in the ledger and in no surface: a bare tethys_x is a verb mention.
	r := VerbLiveness(stocks(map[string]string{"governance/blocks/forge.md": "`tethys_unlisted`\n"}))
	want(t, r, StateFindings, "tethys_unlisted is not a verb on hades's surface")
}

func TestVerbLivenessCannotRunWhenItCannotGrade(t *testing.T) {
	good := map[string]string{"governance/blocks/forge.md": "text\n"}
	for _, c := range []struct {
		name  string
		files map[string]string
		fail  map[string]bool
		drop  []string
		want  string
	}{
		{"ledger unreadable", good, map[string]bool{"retired-verbs.toml": true}, nil, "retired-verbs.toml would not read"},
		{"ledger unusable", map[string]string{"retired-verbs.toml": "# nothing\n"}, nil, nil, "not a ledger this lane can grade against"},
		{"unit unreadable", good, map[string]bool{"governance/blocks/forge.md": true}, nil, "governance/blocks/forge.md would not read"},
		{"tree surface unreadable", map[string]string{"verb-surface.toml": ""}, map[string]bool{"verb-surface.toml": true}, nil, "verb-surface.toml would not read"},
		{"tree surface unusable", map[string]string{"verb-surface.toml": "x = 1\n"}, nil, nil, "verb-surface.toml is not a surface this lane can grade against: names no verb"},
		{"no units", nil, nil, []string{"governance/blocks/alpha.md"}, "no block or render is"},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"kits.toml": "[kits.root]\n", "retired-verbs.toml": ledger, "governance/blocks/alpha.md": "a"}
			for k, v := range c.files {
				files[k] = v
			}
			tr := mapTree(files, c.fail)
			want(t, VerbLiveness(without(tr, c.drop...)), StateCannotRun, "CANNOT RUN", c.want)
		})
	}
}
