// Package diesvalestyles is the atom dies:vale-styles: the Forge Vale style's
// own cases pass, with every rule exercised, in the tree that carries the
// style. fleet:vale reads styles/Forge off the dies mount in every repo's
// gate, so a rule edit that breaks a case or leaves a rule uncovered would
// otherwise land green here and red every consumer's gate at once.
package diesvalestyles

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. The surface is a root .vale.ini beside a styles/ directory,
// which is the package root `vale test` resolves; today only foundry-dies has
// it, and every other tree is ABSENT.
var Def = atomkit.Def{
	ID: "dies:vale-styles", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The Forge Vale style's own cases pass under `vale test --coverage`, with every rule exercised, in the tree that carries styles/ beside a root .vale.ini.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"tool:vale"},
	Check:     check,
}

const id = "dies:vale-styles"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// neverTested is the refusal every could-not-run carries.
const neverTested = "A style whose cases never ran is not a style that passed."

// version proves the vale on PATH runs and is the PINNED one (checks.ValeVersionOK).
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

// HasStylePackage reports whether a tracked population carries a root
// .vale.ini and at least one file under styles/: the layout `vale test` takes
// as a package root.
func HasStylePackage(files []string) bool {
	ini, styles := false, false
	for _, f := range files {
		switch {
		case f == ".vale.ini":
			ini = true
		case strings.HasPrefix(f, "styles/"):
			styles = true
		}
	}
	return ini && styles
}

// check runs the style's cases in the tree's own package root.
//
// NO STYLE PACKAGE IS ABSENT, decided from the population before vale is
// touched. The run is `vale test --coverage styles/` from the root, so the
// cases resolve their rules and Tengo scripts through the tree's own
// .vale.ini, exactly as a session running the suite by hand does. vale's exit
// is three-valued already: 0 every case passed and every rule fired somewhere,
// 1 a failed case or an uncovered rule, 2 a case or the run could not start.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	if !HasStylePackage(in.Files) {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - this tree carries no root .vale.ini beside a styles/ directory, so it holds no Vale style whose cases could run")
	}
	if err := version(ctx, in); err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %v. %s", id, err, neverTested))
	}
	args := []string{"test", "--no-global", "--no-color", "--coverage", "styles/"}
	out, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "vale", Args: args, Both: true})
	if code < 0 {
		return atoms.NeverRan(row, nil, code, out)
	}
	return checks.VerdictOf(row, code, out)
}
