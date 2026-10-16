// Package composenotrackedsecrets is the atom compose:no-tracked-secrets: no
// credential-shaped file is tracked in a repository that ships compose specs.
// One file holds the atom and both runners execute it (lld.md FN-085).
package composenotrackedsecrets

import (
	"context"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It reads the committable file list and nothing else, so it
// runs in lead.
var Def = atomkit.Def{
	ID: "compose:no-tracked-secrets", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "No credential-shaped file is tracked in a repository that ships compose specs.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "compose:no-tracked-secrets"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check: no credential-shaped file is tracked in a repository
// that ships compose specs. The population is the BARE tracked set
// (checks.CredentialShaped carries the argument).
func check(_ context.Context, in atomkit.In) checks.Verdict {
	files, stop := atomlib.ComposeSurface(row, in)
	if stop != nil {
		return *stop
	}
	if hits := checks.CredentialShaped(files); len(hits) > 0 {
		return checks.VerdictOf(row, int(checks.StateFindings), id+": FINDINGS - tracked files that must never be committed:\n"+
			strings.Join(checks.Indent(strings.Join(hits, "\n"), "  "), "\n"))
	}
	return checks.VerdictOf(row, int(checks.StatePass), id+": no credential-shaped file is tracked")
}
