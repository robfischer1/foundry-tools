// Package fleethadolint is the atom fleet:hadolint: every Dockerfile in the tree passes hadolint
// under the fleet's ruleset. One file holds the atom, and both runners execute it (lld.md FN-085).
package fleethadolint

import (
	"context"
	"fmt"
	"os"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. hadolint is a pinned tool (a Haskell binary no language
// toolchain ships); the atom execs it by name and holds it to the pin.
var Def = atomkit.Def{
	ID: "fleet:hadolint", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "Every Dockerfile in the tree passes hadolint under the fleet's ruleset.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"tool:hadolint"},
	Check:     check,
}

const id = "fleet:hadolint"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// version proves the hadolint on PATH runs and is the PINNED one: the rule set
// is a property of the binary (checks.HadolintVersionOK).
func version(ctx context.Context, in atomkit.In) error {
	out, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "hadolint", Args: []string{"--version"}})
	if code != 0 {
		return fmt.Errorf("the hadolint version probe never ran: %s", out)
	}
	if !checks.HadolintVersionOK(out, checks.HadolintVersion) {
		return fmt.Errorf("the binary on disk does not answer %q, so the rules are not the pinned ones: %s", "Haskell Dockerfile Linter "+checks.HadolintVersion, out)
	}
	return nil
}

// check lints every Dockerfile in the tree under the fleet's ruleset.
//
// NO DOCKERFILE IS ABSENT, decided from the population before hadolint is
// touched. THE FLEET'S RULESET IS WRITTEN FOR THE RUN to a scratch file and
// named by --config, which REPLACES hadolint's lookup of the repository's own
// .hadolint.yaml rather than merging with it (checks.HadolintConfigPath has the
// measurement): the ruleset is the fleet's and not the repository's. The tool's
// own exit code is the verdict, 0/1 straight through StateFor; inline pragmas
// are honoured.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	dockerfiles := checks.DockerfilePopulation(in.Files)
	if len(dockerfiles) == 0 {
		return checks.VerdictOf(row, int(checks.StatePass), "fleet:hadolint: ABSENT - this repository tracks no Dockerfile or Containerfile, so it ships no image for hadolint to read")
	}
	if err := version(ctx, in); err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %v. A Dockerfile that was never linted is not a Dockerfile that passed.", id, err))
	}
	config := atoms.TempPath("hadolint", ".yaml")
	defer func() { _ = os.Remove(config) }()
	if err := os.WriteFile(config, []byte(checks.HadolintConfig), 0o644); err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the fleet's ruleset could not be written for the run: %v. A Dockerfile that was never linted is not a Dockerfile that passed.", id, err))
	}
	args := append([]string{"--no-color", "--config", config, "--"}, dockerfiles...)
	out, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "hadolint", Args: args, Both: true})
	if code < 0 {
		return atoms.NeverRan(row, nil, code, out)
	}
	return checks.VerdictOf(row, code, out)
}
