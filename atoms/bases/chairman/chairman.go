// Package baseschairman is the atom bases:chairman: the validation of the
// chairman base's RUN-free image, whose work is internal/chairmanfixed's. It runs
// in the bases lane between assemble and scan, over the committed files and the
// assembled layout.
package baseschairman

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/chairmanfixed"
	"dagger/foundry-tools/internal/checks"
)

// ImageEnv names the directory the assemble capability lays its OCI layouts in,
// one directory per image (the executor sets it for the bases lane's atoms).
const ImageEnv = "ATOM_IMAGE_DIR"

const (
	id        = "bases:chairman"
	defaultIm = "/work/image"
	maxShown  = 60
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "bases:chairman", Stage: checks.StageBases, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The chairman base's fixed files agree with their tables, the Dockerfile that lays them, the units and the lock's maintainer scripts; and the assembled image, when there is one, is what fixed-set.tsv says.",
	Surface:   "all:bases/chairman/Dockerfile,bases/chairman/packages.lock",
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

// validate is chairmanfixed.Validate; a test replaces it.
var validate = chairmanfixed.Validate

func check(_ context.Context, in atomkit.In) checks.Verdict {
	res, err := validate(in.Root, imageDir())
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - "+err.Error())
	}
	if len(res.Problems) > 0 {
		shown := res.Problems
		if len(shown) > maxShown {
			shown = append(shown[:maxShown:maxShown], fmt.Sprintf("... and %d more", len(res.Problems)-maxShown))
		}
		return checks.VerdictOf(row, int(checks.StateFindings), fmt.Sprintf("%s: FINDINGS - %d problem(s) in the chairman base:\n  %s", id, len(res.Problems), strings.Join(shown, "\n  ")))
	}
	return checks.VerdictOf(row, int(checks.StatePass), fmt.Sprintf("%s: PASS - %d checks over %d fixed-set rows and %d locked packages; %s", id, res.Checks, res.Rows, res.Locked, res.Note))
}
