package atomlib

import (
	"fmt"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// THE COMPOSE ATOMS read the RAW committable tree (Input.Committable), not the
// gate population: the chains never applied the fleet exclude to the compose
// lane, and a vendored compose spec is still a compose spec.

// ComposeSurface is the condition the compose atoms share: does this repository
// track a compose spec at all? A scan that failed is a 2, not an absence the
// repository never declared.
func ComposeSurface(a checks.AtomDef, in atoms.Input) (files []string, stop *checks.Verdict) {
	if in.FilesErr != nil {
		v := checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the compose-surface scan itself failed (%v). An empty surface read off a broken scan is an absence this repository never declared.", a.ID, in.FilesErr))
		return nil, &v
	}
	if len(checks.ComposeSpecs(in.Committable)) == 0 {
		v := checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - this repository tracks no compose.yaml/compose.yml, so it declares no compose spec. Most of the fleet is Kubernetes YAML, which this says nothing about.")
		return nil, &v
	}
	return in.Committable, nil
}
