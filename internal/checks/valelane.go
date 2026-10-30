package checks

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// THE PROSE A MODEL READS AS INSTRUCTIONS IS CODE, AND fleet:vale LINTS IT.
// The writing-prompts convergence loop (Tesla70, rounds 13-19) plateaued at one
// or two findings per round, every one on a line the author had added earlier,
// and ten of its fourteen recurring patterns were reference-integrity checks a
// linter settles before an LLM judge is spent on them. The rules are a Vale
// style, Forge, kept in foundry-dies' styles/ and mounted in the lane; this
// file holds the pin, the population, and the configuration the atom writes.

const (
	// ValeVersion is the Vale release the tools container carries and the atom
	// holds the binary on PATH to (ValeVersionOK). The rule engine is a
	// property of the binary: a newer Vale re-judges the same tree, which is a
	// decision, not a pin.
	// renovate: datasource=github-releases depName=errata-ai/vale extractVersion=^v(?<version>.*)$
	ValeVersion = "3.25.1"
	// ValeURL is the release tarball; daedalus unpacks it into the tool's
	// directory, where `vale` sits at the top level.
	ValeURL = "https://github.com/errata-ai/vale/releases/download/v" + ValeVersion + "/vale_" + ValeVersion + "_Linux_64-bit.tar.gz"
	// ValeStyle is the style's name under <dies>/styles and in every alert.
	ValeStyle = "Forge"
)

// ValeVersionOK reports whether `vale --version` answered the pinned release.
func ValeVersionOK(versionOutput, want string) bool {
	for _, line := range strings.Split(versionOutput, "\n") {
		if strings.TrimSpace(line) == "vale version "+want {
			return true
		}
	}
	return false
}

// ValeStylesPath is the StylesPath for a run: foundry-dies' styles directory,
// read off the lane's dies mount. A mount that is absent or carries no Forge
// style is an error, because a file linted against no rules is not a file
// that passed.
func ValeStylesPath(dies string) (string, error) {
	if dies == "" {
		return "", errors.New("the lane mounted no foundry-dies (ATOM_DIES is empty), so the Forge style cannot be read")
	}
	styles := filepath.Join(dies, "styles")
	fi, err := os.Stat(filepath.Join(styles, ValeStyle))
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("foundry-dies at %s carries no styles/%s directory", dies, ValeStyle)
	}
	return styles, nil
}

// ValeConfig is THE FLEET'S CONFIGURATION, written for each run and named by
// --config beside --no-global, so neither the repository's .vale.ini nor the
// runner's is read: a repo has no say in what runs or how (the hadolint
// precedent, Rob 2026-09-11). MinAlertLevel prints every level; only an
// error-level alert fails the run, which is Vale's own exit contract.
//
// The sections are the per-artifact profiles (Master List 10.5). ArgumentSlot
// bites only where Claude Code substitutes arguments, which is a skill body:
// references, rules and governance keep their dollar signs (measured
// 2026-10-30: every hit outside a body was a reference or a rule).
func ValeConfig(stylesPath string) string {
	return "# The fleet's Vale configuration, written by foundry-tools (fleet:vale) for\n" +
		"# one run. The repository's own .vale.ini is not read; the style is\n" +
		"# foundry-dies' styles/" + ValeStyle + ".\n" +
		"StylesPath = " + stylesPath + "\n" +
		"MinAlertLevel = suggestion\n\n" +
		"[*.md]\n" +
		"BasedOnStyles = " + ValeStyle + "\n" +
		ValeStyle + ".ArgumentSlot = NO\n\n" +
		"[{" + strings.Join(ValeSkillBodyGlobs, ",") + "}]\n" +
		ValeStyle + ".ArgumentSlot = YES\n"
}

// ValeSkillBodyGlobs name the files Claude Code loads as a skill body and
// substitutes arguments into: SKILL.md anywhere, and the `<name>.md` beside a
// skill.toml under a skills/ directory at any depth.
var ValeSkillBodyGlobs = []string{"SKILL.md", "**/SKILL.md", "skills/*/*.md", "**/skills/*/*.md"}

// ModelProseNames are the files a model reads as instructions by name,
// wherever they sit in a tree.
var ModelProseNames = map[string]bool{
	"SKILL.md": true, "CLAUDE.md": true, "CLAUDE-INIT.md": true,
	"AGENTS.md": true, "GEMINI.md": true, "COWORK.md": true,
}

// ModelProseDirs are the path segments under which every markdown file is
// read by a model: a skill and its references, an agent definition, a rule,
// a hook's instructions, and anything laid under .claude.
var ModelProseDirs = map[string]bool{
	".claude": true, "skills": true, "agents": true, "rules": true, "hooks": true,
}

// ModelProseSkip are the segments that hold fixtures and vendored trees, not
// prose a fix would land in. renders/ is NOT here: foundry-stocks' rendered
// copies are the laid files themselves, and a finding there lands in the law
// repo where Rob inscribes, before it reaches eighty consumers.
var ModelProseSkip = map[string]bool{
	"testdata": true, "fixtures": true, "node_modules": true, "vendor": true,
}

// IsModelProse reports whether one tracked path is prose a model reads as
// instructions: a markdown file named in ModelProseNames or under a segment in
// ModelProseDirs, and under no segment in ModelProseSkip.
func IsModelProse(p string) bool {
	if !strings.HasSuffix(p, ".md") {
		return false
	}
	dir, base := path.Split(p)
	segments := strings.Split(strings.Trim(dir, "/"), "/")
	for _, s := range segments {
		if ModelProseSkip[s] {
			return false
		}
	}
	if ModelProseNames[base] {
		return true
	}
	for _, s := range segments {
		if ModelProseDirs[s] {
			return true
		}
	}
	return false
}

// ModelProsePopulation is the files fleet:vale lints, in the order given.
func ModelProsePopulation(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if IsModelProse(f) {
			out = append(out, f)
		}
	}
	return out
}
