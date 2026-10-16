package orbitkit

import (
	"context"
	"fmt"
	"path/filepath"

	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitcompose"
)

const (
	// orbitRenderDir is the directory orbitcompose owns in the tree under check.
	orbitRenderDir = "prime/orbits"
	// orbitNamespace is the namespace the sidecars' ConfigMaps are rendered for:
	// orbitcompose's own default, and the one flux renders.
	orbitNamespace = "prime"
)

// OrbitComposed is ops:orbit-composed's question, which orbit:sidecars also asks
// as a finding: flux's rendered orbit sidecars are not stale against foundry-dies
// main. A tree that renders none is ABSENT.
func OrbitComposed(a checks.AtomDef, in atoms.Input) checks.Verdict {
	cannot := func(why string, err error) checks.Verdict {
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - "+why+err.Error())
	}
	have, err := FilesByName(in.Tree(), orbitRenderDir+"/*")
	if err != nil {
		return cannot("the rendered sidecars could not be read: ", err)
	}
	if len(have) == 0 {
		return checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - this tree renders no orbit sidecars (no "+orbitRenderDir+"/)")
	}
	dies, err := Dies(in)
	if err != nil {
		return cannot("foundry-dies' contracts could not be read: ", err)
	}
	contracts, err := FilesByName(dies, "orbits/*.toml")
	if err != nil {
		return cannot("foundry-dies' contracts could not be read: ", err)
	}
	prefixes, err := Roster(dies)
	if err != nil {
		return cannot("foundry-dies' roster could not be read: ", err)
	}
	d := orbitcompose.CheckDrift(contracts, have, orbitNamespace, StarSet(prefixes))
	return checks.VerdictOf(a, d.State, orbitRenderDir+" against foundry-dies main: "+d.Report)
}

// OrbitSidecars is ops:orbit-sidecars's question, which orbit:sidecars also asks
// as a finding: every composed orbit sidecar an ops tree tracks parses with the
// reader the star itself runs. The reader is a ten-line main over
// policy.ParseACL (internal/checks/scripts/_orbitparse) built by the module and
// mounted on PATH as orbitparse: IMPORTED, NOT RE-IMPLEMENTED, because a port of
// the parser agrees with the star's only until one of them changes.
func OrbitSidecars(ctx context.Context, a checks.AtomDef, in atoms.Input) checks.Verdict {
	if stop := atomlib.OpsSurface(a, in); stop != nil {
		return *stop
	}
	sidecars := checks.OrbitSidecars(in.Committable)
	if len(sidecars) == 0 {
		return checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - this tree tracks no composed orbit sidecar (*.orbit.toml), so no star reads one from it")
	}
	args := make([]string, len(sidecars))
	for i, s := range sidecars {
		args[i] = filepath.Join(in.Root, s)
	}
	out, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "orbitparse", Args: args, Both: true})
	if code < 0 {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("the atom never ran: %s", out))
	}
	return checks.VerdictOf(a, min(code, int(checks.StateCannotRun)), out)
}
