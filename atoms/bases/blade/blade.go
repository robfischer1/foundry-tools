// Package basesblade is the atom bases:blade: the validation of the BLADE bases'
// RUN-free images, whose work is internal/bladefixed's. It runs in the build lane,
// for a repository of bases, between assemble and scan, over the assembled layouts.
//
// ENFORCING. A problem is a finding and a layout it cannot read is a 2: the blade
// bases' build-time asserts were dropped with their RUNs (base-images#80, #81)
// and this is their replacement.
package basesblade

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/bladefixed"
	"dagger/foundry-tools/internal/checks"
)

// ImageEnv names the directory the assemble capability lays its OCI layouts in,
// one directory per image (the executor sets it for the build lane's atoms).
const ImageEnv = "ATOM_IMAGE_DIR"

const (
	id        = "bases:blade"
	defaultIm = "/work/image"
	maxShown  = 60
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "bases:blade", Stage: checks.StageBases, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The assembled blade-base, blade-python-claude and blade-differ images keep the privilege split the dropped build-time asserts held: /opt/blade root:blade 0550, the agent and gate accounts, /run/blade and /work, the pytest, claude and differ binaries. A problem fails the build; an unreadable layout cannot run.",
	Surface:   "any:bases/blade-base/Dockerfile,bases/blade-python-claude/Dockerfile,bases/blade-differ/Dockerfile",
	Toolchain: "none",
	Timeout:   15 * time.Minute,
	Check:     check,
}

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StageBases, Lane: checks.LaneAny}

func imageDir() string {
	if d := os.Getenv(ImageEnv); d != "" {
		return d
	}
	return defaultIm
}

// validate is bladefixed.Validate; a test replaces it.
var validate = bladefixed.Validate

func check(_ context.Context, _ atomkit.In) checks.Verdict {
	res, err := validate(imageDir())
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the blade bases went unchecked: "+err.Error())
	}
	if len(res.Checked) == 0 {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - no blade image is laid at "+imageDir()+" (the assemble stood down, or this lane assembles no blade base)")
	}
	if len(res.Problems) > 0 {
		shown := res.Problems
		if len(shown) > maxShown {
			shown = append(shown[:maxShown:maxShown], fmt.Sprintf("... and %d more", len(res.Problems)-maxShown))
		}
		return checks.VerdictOf(row, int(checks.StateFindings), fmt.Sprintf("%s: FAIL - %d problem(s) in the blade bases:\n  %s", id, len(res.Problems), strings.Join(shown, "\n  ")))
	}
	return checks.VerdictOf(row, int(checks.StatePass), fmt.Sprintf("%s: PASS - %d checks over %s", id, res.Checks, strings.Join(res.Checked, ", ")))
}
