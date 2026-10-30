package checks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsModelProseReadsNamesDirsAndSkips(t *testing.T) {
	t.Parallel()
	for p, want := range map[string]bool{
		"SKILL.md":                        true,
		"CLAUDE.md":                       true,
		"CLAUDE-INIT.md":                  true,
		"AGENTS.md":                       true,
		"GEMINI.md":                       true,
		"COWORK.md":                       true,
		"docs/CLAUDE.md":                  true,
		"skills/navigating/navigating.md": true,
		"skills/navigating/references/recipes.md":        true,
		"plugins/skill-pipeline/agents/skill-auditor.md": true,
		".claude/rules/python-style.md":                  true,
		".claude/skills/speckit-plan/SKILL.md":           true,
		"hooks/forge/README.md":                          true,
		"renders/home/claude/.claude/rules/frontend.md":  true,
		"README.md":                         false,
		"docs/design.md":                    false,
		"internal/checks/valelane.go":       false,
		"skills/navigating/scripts/term.py": false,
		"internal/assemble/testdata/conformance/stocks/skills/x/SKILL.md": false,
		"tests/fixtures/AGENTS.md":    false,
		"node_modules/x/SKILL.md":     false,
		"vendor/x/.claude/rules/a.md": false,
	} {
		if got := IsModelProse(p); got != want {
			t.Errorf("IsModelProse(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestModelProsePopulationKeepsOrderAndDropsTheRest(t *testing.T) {
	t.Parallel()
	got := ModelProsePopulation([]string{"z/AGENTS.md", "main.go", "a/SKILL.md", "README.md"})
	if strings.Join(got, ",") != "z/AGENTS.md,a/SKILL.md" {
		t.Errorf("population %v", got)
	}
}

func TestValeVersionOKReadsTheProbeLine(t *testing.T) {
	t.Parallel()
	if !ValeVersionOK("vale version "+ValeVersion+"\n", ValeVersion) {
		t.Error("the pinned line is not accepted")
	}
	if ValeVersionOK("vale version 3.0.0\n", ValeVersion) || ValeVersionOK("", ValeVersion) {
		t.Error("another release or nothing is accepted")
	}
}

func TestValeStylesPathNeedsTheMountAndTheStyle(t *testing.T) {
	t.Parallel()
	if _, err := ValeStylesPath(""); err == nil || !strings.Contains(err.Error(), "ATOM_DIES is empty") {
		t.Errorf("no mount: %v", err)
	}
	dies := t.TempDir()
	if _, err := ValeStylesPath(dies); err == nil || !strings.Contains(err.Error(), "carries no styles/"+ValeStyle) {
		t.Errorf("mount without the style: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dies, "styles", ValeStyle), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ValeStylesPath(dies)
	if err != nil || got != filepath.Join(dies, "styles") {
		t.Errorf("mount with the style: %q, %v", got, err)
	}
}

func TestValeConfigIsTheFleetsAndScopesTheSlotRuleToSkillBodies(t *testing.T) {
	t.Parallel()
	c := ValeConfig("/dies/styles")
	for _, want := range []string{
		"StylesPath = /dies/styles\n",
		"MinAlertLevel = suggestion\n",
		"[*.md]\nBasedOnStyles = Forge\nForge.ArgumentSlot = NO\n",
		"[{SKILL.md,**/SKILL.md,skills/*/*.md,**/skills/*/*.md}]\nForge.ArgumentSlot = YES\n",
		"[**/naming-things/**]\nForge.Retired = NO\n",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("config lacks %q:\n%s", want, c)
		}
	}
}

func TestValeURLIsTheLinuxTarballOfThePinnedRelease(t *testing.T) {
	t.Parallel()
	if !strings.HasSuffix(ValeURL, "/v"+ValeVersion+"/vale_"+ValeVersion+"_Linux_64-bit.tar.gz") {
		t.Errorf("ValeURL %q does not name release %s", ValeURL, ValeVersion)
	}
	if ToolSHA256[ValeURL] == "" {
		t.Error("ValeURL has no checksum line")
	}
}
