package atoms

import (
	"context"
	"fmt"
	"path/filepath"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitcompose"
)

// THE OPS ATOMS that are a function over the tracked files. An ops atom stands
// down ABSENT on a tree that is not an ops tree (flux/, ansible/, a chezmoi
// source, a compose spec, a rego policy, one of infra's tools): running them
// over every star would be a new gate turned on across the fleet at once.

// opsSurface is the stand-down every ops atom shares. A scan that failed is a
// 2, not an absence the repository never declared.
func opsSurface(a checks.AtomDef, in Input) *checks.Verdict {
	var v checks.Verdict
	switch {
	case in.FilesErr != nil:
		v = cannotEnumerate(a, in.FilesErr)
	case checks.IsOpsTree(in.Files):
		return nil
	default:
		v = checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - this repository has no ops shape (no flux/, ansible/, chezmoi source, compose spec, rego policy or infra tool), so the ops lane does not gate it. A star is gated by its language lane.")
	}
	return &v
}

// opsYAML: no tracked YAML carries a duplicate key, the defect every loader the
// fleet runs resolves last-wins and never reports.
//
// THE CHAIN'S PYTHON BRANCH IS NOT PORTED. It runs a repo's own tools/yaml-strict
// instead when the tree carries one, and that tool exists in no repository in
// the fleet (infra, the one repo it was written for, does not have it): the
// branch has never fired, so the binary has the parse alone, checks.OpsStrictYAML.
// If a tree ever does carry one, the shadow will say so as a STATE difference.
func opsYAML(_ context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if stop := opsSurface(a, in); stop != nil {
		return *stop
	}
	yamls := checks.OpsYAMLFiles(checks.OpsFiles(in.Committable))
	if len(yamls) == 0 {
		return checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - no yaml in this tree")
	}
	t := in.tree()
	errs := map[string]error{}
	for _, f := range yamls {
		src, err := t.read(f)
		if err != nil {
			return checks.VerdictOf(a, int(checks.StateCannotRun), "the atom never ran: "+err.Error())
		}
		errs[f] = checks.OpsStrictYAML(src)
	}
	report, bad := checks.OpsYAMLReport(yamls, errs)
	rc := 0
	if bad {
		rc = 1
	}
	state, line := checks.OpsSettle("yaml", rc, report)
	if line != "" {
		report += "\n" + line
	}
	return checks.VerdictOf(a, state, report)
}

const (
	// orbitRenderDir is the directory orbitcompose owns in the tree under check.
	orbitRenderDir = "prime/orbits"
	// orbitNamespace is the namespace the sidecars' ConfigMaps are rendered for:
	// orbitcompose's own default, and the one flux renders.
	orbitNamespace = "prime"
)

// opsOrbitComposed: flux's rendered orbit sidecars are not stale against
// foundry-dies main. A tree that renders none is ABSENT.
func opsOrbitComposed(_ context.Context, a checks.AtomDef, in Input) checks.Verdict {
	cannot := func(why string, err error) checks.Verdict {
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - "+why+err.Error())
	}
	have, err := filesByName(in.tree(), orbitRenderDir+"/*")
	if err != nil {
		return cannot("the rendered sidecars could not be read: ", err)
	}
	if len(have) == 0 {
		return checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - this tree renders no orbit sidecars (no "+orbitRenderDir+"/)")
	}
	dies, err := in.dies()
	if err != nil {
		return cannot("foundry-dies' contracts could not be read: ", err)
	}
	contracts, err := filesByName(dies, "orbits/*.toml")
	if err != nil {
		return cannot("foundry-dies' contracts could not be read: ", err)
	}
	prefixes, err := roster(dies)
	if err != nil {
		return cannot("foundry-dies' roster could not be read: ", err)
	}
	d := orbitcompose.CheckDrift(contracts, have, orbitNamespace, starSet(prefixes))
	return checks.VerdictOf(a, d.State, orbitRenderDir+" against foundry-dies main: "+d.Report)
}

// opsOrbitSidecars: every composed orbit sidecar an ops tree tracks parses with
// the reader the star itself runs. The reader is a ten-line main over
// policy.ParseACL (internal/checks/scripts/_orbitparse) built by the module and
// mounted on PATH as orbitparse: IMPORTED, NOT RE-IMPLEMENTED, because a port of
// the parser agrees with the star's only until one of them changes. Not a
// registered atom of its own here; orbit:sidecars runs it as a finding.
func opsOrbitSidecars(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if stop := opsSurface(a, in); stop != nil {
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
	out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "orbitparse", Args: args, Both: true})
	if code < 0 {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("the atom never ran: %s", out))
	}
	return checks.VerdictOf(a, min(code, int(checks.StateCannotRun)), out)
}
