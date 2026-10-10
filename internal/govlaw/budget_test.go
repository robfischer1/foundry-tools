// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package govlaw

import (
	"strings"
	"testing"
)

func budgetTree(budget string, extra map[string]string) Tree {
	files := map[string]string{
		"renders/forge-root/claude/AGENTS.md": strings.Repeat("x", 100),
		"renders/forge-root/claude/CLAUDE.md": strings.Repeat("y", 400),
		"renders/vault/pi/AGENTS.md":          strings.Repeat("a", 60),
		"renders/vault/pi/APPEND_SYSTEM.md":   strings.Repeat("b", 40),
	}
	if budget != "" {
		files["budget.toml"] = budget
	}
	for k, v := range extra {
		files[k] = v
	}
	return stocks(files)
}

const exactBudget = "[budget.forge-root]\nclaude = 100\n[budget.vault]\npi = 100\n"

func TestBudgetHoldsRendersAtTheirCeilings(t *testing.T) {
	r := Budget(budgetTree(exactBudget, nil))
	want(t, r, StatePass, "2 render(s) are within their always-on ceilings")
	wantNot(t, r, "WARNINGS")
}

func TestBudgetCountsContextAndAppendDocumentsButNotTheShim(t *testing.T) {
	// forge-root/claude is 100 (AGENTS.md) with a 400-byte shim beside it; vault/pi
	// is 60+40. A ceiling of 99 / 99 is exceeded by exactly one byte each.
	r := Budget(budgetTree("[budget.forge-root]\nclaude = 99\n[budget.vault]\npi = 99\n", nil))
	want(t, r, StateFindings,
		"renders/forge-root/claude: 100 bytes of always-on context is 1 over its ceiling of 99",
		"renders/vault/pi: 100 bytes of always-on context is 1 over its ceiling of 99")
}

func TestBudgetANewRenderNeedsACeilingAndAHeldCeilingNeedsARender(t *testing.T) {
	r := Budget(budgetTree(exactBudget, map[string]string{"renders/new/claude/AGENTS.md": "12345"}))
	want(t, r, StateFindings, "renders/new/claude: 5 bytes of always-on context and no ceiling in budget.toml")
	wantNot(t, r, "forge-root", "vault")

	r = Budget(budgetTree(exactBudget+"[budget.gone]\nclaude = 10\n", nil))
	want(t, r, StatePass, "WARNINGS (1, not gating)", "budget.toml: a ceiling for gone/claude, which has no render")
}

func TestBudgetSlackIsAWarningNotAStop(t *testing.T) {
	r := Budget(budgetTree("[budget.forge-root]\nclaude = 130\n[budget.vault]\npi = 100\n", nil))
	want(t, r, StatePass, "renders/forge-root/claude: 100 bytes is 30 under its ceiling of 130")
	wantNot(t, r, "vault")
}

func TestBudgetWithoutAFileHoldsNothing(t *testing.T) {
	r := Budget(budgetTree("", nil))
	want(t, r, StateFindings,
		"renders/forge-root/claude: 100 bytes of always-on context and no ceiling",
		"renders/vault/pi: 100 bytes of always-on context and no ceiling")
}

func TestBudgetIsAbsentWithNoRender(t *testing.T) {
	want(t, Budget(stocks(map[string]string{"budget.toml": exactBudget})), StatePass, "ABSENT", "nothing to hold to a budget")
	// A render of nothing but a shim has no always-on context of its own.
	want(t, Budget(stocks(map[string]string{"renders/c/claude/CLAUDE.md": "shim"})), StatePass, "ABSENT")
}

func TestBudgetCannotRunOnABudgetItCannotRead(t *testing.T) {
	good := budgetTree(exactBudget, nil)
	for _, c := range []struct {
		name   string
		budget string
		want   string
	}{
		{"not toml", "[budget.a\n", "budget.toml is not a budget this lane can read"},
		{"not a number", "[budget.a]\nclaude = \"big\"\n", "is not a budget this lane can read"},
		{"zero", "[budget.a]\nclaude = 0\n", "[budget.a] claude = 0 is not a ceiling"},
		{"negative", "[budget.a]\nclaude = -5\n", "[budget.a] claude = -5 is not a ceiling"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tr := budgetTree(c.budget, nil)
			want(t, Budget(tr), StateCannotRun, "CANNOT RUN", c.want)
		})
	}
	// Reads that fail.
	for _, p := range []string{"budget.toml", "renders/forge-root/claude/AGENTS.md"} {
		tr := good
		base := tr.Read
		tr.Read = func(q string) (string, error) {
			if q == p {
				return "", errBoom
			}
			return base(q)
		}
		want(t, Budget(tr), StateCannotRun, "CANNOT RUN", p+" would not read", "i/o error")
	}
}

func TestBudgetAddsAProvidersDocumentsAndNotAnotherRenders(t *testing.T) {
	tr := budgetTree("[budget.vault]\npi = 99\n[budget.forge-root]\nclaude = 100\n", nil)
	r := Budget(tr)
	want(t, r, StateFindings, "renders/vault/pi: 100 bytes")
	wantNot(t, r, "forge-root")
}
