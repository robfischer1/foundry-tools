// Package fleetvale is the atom fleet:vale: every file a model reads as
// instructions (a skill body, CLAUDE.md, AGENTS.md, a rule, an agent
// definition) passes the Forge style under Vale. The style is foundry-dies'
// (styles/Forge), mounted in the lane; the configuration is written here for
// the run, so the ruleset is the fleet's and never the repository's.
package fleetvale

import (
	"context"
	"fmt"
	"os"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. vale is a pinned tool (a Go binary no lane toolchain
// ships); the atom execs it by name and holds it to the pin. It needs the dies
// mount because the style lives in foundry-dies, not in this module.
var Def = atomkit.Def{
	ID: "fleet:vale", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "Every file a model reads as instructions (SKILL.md, CLAUDE.md, AGENTS.md, rules, agent definitions) passes the Forge style under Vale.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"dies", "tool:vale"},
	Check:     check,
}

const id = "fleet:vale"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// neverLinted is the refusal every could-not-run carries: an unlinted file is
// not a passing one.
const neverLinted = "A file that was never linted is not a file that passed."

// version proves the vale on PATH runs and is the PINNED one: the rule engine
// is a property of the binary (checks.ValeVersionOK).
func version(ctx context.Context, in atomkit.In) error {
	out, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "vale", Args: []string{"--version"}})
	if code != 0 {
		return fmt.Errorf("the vale version probe never ran: %s", out)
	}
	if !checks.ValeVersionOK(out, checks.ValeVersion) {
		return fmt.Errorf("the binary on disk does not answer %q, so the rules are not the pinned ones: %s", "vale version "+checks.ValeVersion, out)
	}
	return nil
}

// check lints every model-read markdown file in the tree under the Forge style.
//
// NO MODEL-READ FILE IS ABSENT, decided from the population before vale or the
// dies mount is touched. THE STYLE IS FOUNDRY-DIES' and the configuration is
// written for the run (checks.ValeConfig) and named by --config with
// --no-global, so neither the repository's .vale.ini nor the runner's is read.
// vale's exit is three-valued already: 0 clean, 1 an error-level alert, 2 it
// could not run, straight through StateFor; warnings and suggestions are
// printed and do not fail the gate.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	files := checks.ModelProsePopulation(in.Files)
	if len(files) == 0 {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - this repository tracks no file a model reads as instructions (SKILL.md, CLAUDE.md, AGENTS.md, a rule, an agent definition), so it has no prose for the Forge style to read")
	}
	styles, err := checks.ValeStylesPath(in.Dies)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %v. %s", id, err, neverLinted))
	}
	if err := version(ctx, in); err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %v. %s", id, err, neverLinted))
	}
	config := atoms.TempPath("vale", ".ini")
	defer func() { _ = os.Remove(config) }()
	if err := os.WriteFile(config, []byte(checks.ValeConfig(styles)), 0o644); err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the fleet's configuration could not be written for the run: %v. %s", id, err, neverLinted))
	}
	args := append([]string{"--no-global", "--no-color", "--no-wrap", "--output=line", "--config", config, "--"}, files...)
	out, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "vale", Args: args, Both: true})
	if code < 0 {
		return atoms.NeverRan(row, nil, code, out)
	}
	return checks.VerdictOf(row, code, out)
}
