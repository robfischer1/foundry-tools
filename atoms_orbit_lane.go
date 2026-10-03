package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/orbitcompose"
	"dagger/foundry-tools/internal/orbitlane"
)

// THE ORBIT LANE'S ATOMS (stage `orbit`, run by `gate-file --stage=orbit` as
// a lane of its own). Each is a separate atom so a red names what broke. The
// judgements are internal/orbitlane's; this file reads the trees — the repo
// under test, and foundry-dies at main for a star's pull — and narcissus's
// analyzer's answers, and hands them over.
//
// REPORT-ONLY until Rob flips orbitlane.Enforce: a would-be violation is
// recorded as drifted and the atom stays state 0.

func init() {
	register("orbit:contracts", orbitContracts)
	register("orbit:surface", orbitSurface)
	register("orbit:sidecars", orbitSidecars)
	register("orbit:repo", orbitRepo)
}

// NarcissusImage carries `narc`, narcissus's own analyzer, at its released
// build. The lane runs the star's reader, never a port of it: `narc scan` is
// the table code_query answers on the wire, and it takes no store and no door.
const NarcissusImage = "registry.notusmi.com/rob/narcissus:stable"

// orbitVerdict settles an atom's findings into its vector element.
func orbitVerdict(id string, found []checks.Finding) checks.Verdict {
	state, reason, settled := orbitlane.Settle(id, found)
	v := checks.VerdictOf(checks.AtomByID(id), state, reason)
	v.Findings = settled
	return v
}

// orbitAbsent is an atom with no surface in this tree, saying why.
func orbitAbsent(id, why string) checks.Verdict {
	return checks.VerdictOf(checks.AtomByID(id), 0, id+": ABSENT - "+why)
}

// orbitCannot is an atom that could not look.
func orbitCannot(id string, err error) checks.Verdict {
	return checks.VerdictOf(checks.AtomByID(id), 2, id+": CANNOT RUN - "+err.Error())
}

// filesIn is filesByName (ops:orbit-composed's reader) naming the pattern
// it could not read.
func filesIn(ctx context.Context, dir *dagger.Directory, pattern string) (map[string][]byte, error) {
	out, err := filesByName(ctx, dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("%s could not be read: %v", pattern, err)
	}
	return out, nil
}

// hasContract reports whether a directory's files hold any contract.
func hasContract(files map[string][]byte) bool {
	for name := range files {
		if strings.HasSuffix(name, ".toml") {
			return true
		}
	}
	return false
}

// roster answers each star on dir's fleet roster and its verb prefix.
func roster(ctx context.Context, dir *dagger.Directory) (map[string]string, error) {
	paths, err := dir.Glob(ctx, "fleet/stars/*/data.json")
	if err != nil {
		return nil, fmt.Errorf("the roster could not be listed: %v", err)
	}
	out := map[string]string{}
	for _, p := range paths {
		body, err := dir.File(p).Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s could not be read: %v", p, err)
		}
		var star struct {
			VerbPrefix string `json:"verb_prefix"`
		}
		// A shard that is not JSON still names a star by its directory; its
		// prefix is unknown, so no gateway name is attributed through it.
		_ = json.Unmarshal([]byte(body), &star)
		out[path.Base(path.Dir(p))] = star.VerbPrefix
	}
	return out, nil
}

// fleetContracts answers the contracts on foundry-dies main.
func (r *run) fleetContracts(ctx context.Context) ([]orbitcompose.Contract, error) {
	files, err := filesIn(ctx, r.dies, "orbits/*.toml")
	if err != nil {
		return nil, fmt.Errorf("foundry-dies: %v", err)
	}
	cs, err := orbitcompose.ContractsOf(files)
	if err != nil {
		return nil, fmt.Errorf("foundry-dies/orbits does not compose: %v", err)
	}
	return cs, nil
}

// orbit:contracts — every contract parses, names two stars on the roster,
// carries a known status, and an approved one moved its version with its
// verbs (against foundry-dies main, the branch the pull merges into).
func orbitContracts(ctx context.Context, r *run) checks.Verdict {
	const id = "orbit:contracts"
	const notHere = "this tree is not the contracts' repository (it needs orbits/*.toml beside fleet/stars/)"
	files, err := filesIn(ctx, r.src, "orbits/*")
	if err != nil {
		return orbitCannot(id, err)
	}
	if !hasContract(files) {
		return orbitAbsent(id, notHere)
	}
	stars, err := roster(ctx, r.src)
	if err != nil {
		return orbitCannot(id, err)
	}
	if len(stars) == 0 {
		return orbitAbsent(id, notHere)
	}
	base, err := filesIn(ctx, r.dies, "orbits/*.toml")
	if err != nil {
		return orbitCannot(id, fmt.Errorf("foundry-dies main: %v", err))
	}
	names := map[string]bool{}
	for s := range stars {
		names[s] = true
	}
	return orbitVerdict(id, orbitlane.Contracts(files, base, names))
}

// orbit:repo — a laid orbit.toml is what the contracts compose to for this
// star (fleet:orbit-drift's digest comparison, plus the edge set).
func orbitRepo(ctx context.Context, r *run) checks.Verdict {
	const id = "orbit:repo"
	laid, ok, err := fileIn(ctx, r.src, "orbit.toml")
	if err != nil {
		return orbitCannot(id, err)
	}
	if !ok {
		return orbitAbsent(id, "this repository carries no root orbit.toml (it is laid from the data/orbits die)")
	}
	cs, err := r.fleetContracts(ctx)
	if err != nil {
		return orbitCannot(id, err)
	}
	return orbitVerdict(id, orbitlane.Repo(starOf(r.repo), []byte(laid), cs))
}

// orbit:surface — the star's code against its contracts, from narcissus's
// analyzer run over the checkout.
func orbitSurface(ctx context.Context, r *run) checks.Verdict {
	const id = "orbit:surface"
	own, err := filesIn(ctx, r.src, "orbits/*")
	if err != nil {
		return orbitCannot(id, err)
	}
	if hasContract(own) {
		return orbitAbsent(id, "the contracts' repository: each contract is checked against the code in its producer's and consumer's own orbit lane, where the checkout is the code")
	}
	star := starOf(r.repo)
	cs, err := r.fleetContracts(ctx)
	if err != nil {
		return orbitCannot(id, err)
	}
	if !party(star, cs) {
		return orbitAbsent(id, star+" takes part in no contracted seam in foundry-dies/orbits")
	}
	prefixes, err := roster(ctx, r.dies)
	if err != nil {
		return orbitCannot(id, fmt.Errorf("foundry-dies: %v", err))
	}
	ctr := r.lane(checks.ImageFleet).
		WithFile("/usr/local/bin/narc", dag.Container().From(NarcissusImage).File("/narc"), dagger.ContainerWithFileOpts{Permissions: 0o755})
	// STDOUT ALONE, AND ANY EXIT. narc exits 2 when a site could not be
	// followed and still prints its whole JSON report — the hole is part of
	// the answer — and with --json - the report is its only stdout, so
	// stderr is never folded in (output() would, on a non-zero exit).
	reports := map[string][]byte{}
	for _, scan := range []string{"surface", "orbits"} {
		out, err := ctr.WithExec([]string{"narc", "scan", scan, "--json", "-", "/src"}, anyExit).Stdout(ctx)
		if err != nil {
			return orbitCannot(id, fmt.Errorf("narc scan %s never ran: %v", scan, err))
		}
		reports[scan] = []byte(out)
	}
	found, err := orbitlane.Surface(orbitlane.SurfaceInput{
		Star: star, Contracts: cs, Prefixes: prefixes, Surface: reports["surface"], Orbits: reports["orbits"],
	})
	if err != nil {
		return orbitCannot(id, err)
	}
	return orbitVerdict(id, found)
}

// party reports whether star is either side of any contract.
func party(star string, cs []orbitcompose.Contract) bool {
	for _, c := range cs {
		if c.Producer == star || c.Consumer == star {
			return true
		}
	}
	return false
}

// orbit:sidecars — what the contracts compose to is what flux carries, and it
// parses with the reader a star refuses to boot without. On a flux tree it is
// the two gate atoms that already say so (ops:orbit-composed's -check and
// ops:orbit-sidecars' policy.ParseACL), run here as findings; the gate keeps
// them until this lane blocks.
func orbitSidecars(ctx context.Context, r *run) checks.Verdict {
	const id = "orbit:sidecars"
	rendered, err := r.src.Glob(ctx, "prime/orbits/*"+orbitcompose.SidecarSuffix)
	if err != nil {
		return orbitCannot(id, err)
	}
	if len(rendered) == 0 {
		return orbitAbsent(id, "this tree renders no orbit sidecars (no prime/orbits/*.orbit.toml)")
	}
	var found []checks.Finding
	for _, sub := range []string{"ops:orbit-composed", "ops:orbit-sidecars"} {
		v := registry[sub](ctx, r)
		switch v.State {
		case int(checks.StateCannotRun):
			return orbitCannot(id, fmt.Errorf("%s could not run: %s", sub, v.Reason))
		case int(checks.StateFindings):
			found = append(found, checks.Finding{Verdict: checks.VerdictViolated, Subject: "prime/orbits", Cause: sub, Detail: oneLine(v.Reason)})
		default:
			found = append(found, checks.Finding{Verdict: checks.VerdictHolds, Subject: "prime/orbits", Cause: sub, Detail: oneLine(v.Reason)})
		}
	}
	return orbitVerdict(id, found)
}

// oneLine is a reason folded onto one line, for a finding's detail.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
