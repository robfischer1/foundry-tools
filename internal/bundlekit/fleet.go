package bundlekit

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/checks"
)

// The fleet die's files in the out directory.
const (
	FleetPlain = "fleet-bundle.tar.gz"
)

// Flux is the foundry/flux checkout the fetch role laid and the commit main
// resolved to when it did: every flux fact in the die is read from that one
// commit, and the die names it (data.fleet.flux_source).
type Flux struct {
	Tree atoms.Tree
	SHA  string
}

// ReadFlux opens the checkout at dir.
func ReadFlux(dir string) (Flux, *Fault) {
	raw, err := os.ReadFile(filepath.Join(dir, FluxShaFile))
	if err != nil {
		return Flux{}, CouldNotRun("foundry/flux %s could not be resolved to a commit: %v", checks.FluxRef, err)
	}
	return Flux{Tree: atoms.Input{Root: dir}.Tree(), SHA: strings.TrimSpace(string(raw))}, nil
}

// Fleet is the roster die,: the two
// tiers gated, the catalog entries and flux's facts injected, the roster built
// once, unsigned, from a staged tree, and the artifact gated for the roster
// it claims to carry and for composing with the policy.
func Fleet(ctx context.Context, r Run) *Fault {
	sha, bad := r.Sha(ctx)
	if bad != nil {
		return bad
	}
	if _, err := bundlelane.Pin(sha); err != nil {
		return CouldNotRun("%v", err)
	}
	changed, bad := r.Changed(ctx, sha)
	if bad != nil {
		return bad
	}
	_, publish := bundlelane.Publishes(changed)
	t := r.In.Tree()
	derive := atoms.Present(t, checks.FleetDerive)

	data, err := t.Read("fleet/data.json")
	if err != nil {
		return Findings("fleet/data.json is absent — the fleet tier (map, topics, wiring) is not in the repo")
	}
	entries := t.Glob(bundlelane.DeclaredGlob)
	orphans, mapped, err := bundlelane.Orphans(entries, data)
	if err != nil {
		return Findings("%v", err)
	}
	if len(orphans) > 0 {
		return Findings("catalog entries with no fleet/data.json map entry: %s", strings.Join(orphans, " "))
	}
	Say("fleet: tier gate — %d catalog entries, all present in a %d-repo map", len(entries), mapped)

	flux, bad := ReadFlux(FluxDir())
	if bad != nil {
		return bad
	}
	inj, bad := r.inject(t, flux, data)
	if bad != nil {
		return bad
	}
	if bad := r.stage(inj); bad != nil {
		return bad
	}
	if derive {
		Say("fleet: %s derives data.fleet.stars and data.fleet.repos from the catalog entries and flux", checks.FleetDerive)
	}
	revision := bundlelane.FleetRevision(sha, flux.SHA)
	scratch, err := os.MkdirTemp(atoms.WorkDir(), "bundle-fleet-")
	if err != nil {
		return CouldNotRun("a scratch directory could not be made: %v", err)
	}
	defer os.RemoveAll(scratch)
	if err := os.MkdirAll(OutDir(), 0o755); err != nil {
		return CouldNotRun("the bundle directory could not be made: %v", err)
	}
	args := []string{"build", "-b", StageDir(), "-o", filepath.Join(OutDir(), FleetPlain), "--revision", revision}
	out, code, bad := r.Exec(ctx, "opa build", r.In.Root, "", "opa", args...)
	if bad != nil {
		return bad
	}
	if code != 0 {
		return Findings("opa build over the staged roster failed:\n%s", out)
	}
	if bad := r.gateFleet(ctx, scratch, inj, flux.SHA); bad != nil {
		return bad
	}
	pin, _ := bundlelane.FleetPin(sha, flux.SHA)
	meta := Meta{
		Die: bundlelane.FleetDie, Kind: "bundle", Pin: pin, Revision: revision, Sha: sha, Publish: publish, PublishIfMissing: true,
		Artifacts: []Artifact{{FleetPlain, "", "stable"}},
	}
	if err := WriteMeta("fleet", meta); err != nil {
		return CouldNotRun("%v", err)
	}
	return nil
}

// inject reads what the fleet die serves beside the roster — the catalog
// entries and flux's star and data/ manifests — and hands them to
// bundlelane.Inject (bundle_inject.go).
func (r Run) inject(t atoms.Tree, flux Flux, data string) (bundlelane.Injection, *Fault) {
	for _, p := range []string{bundlelane.DeclaredPath, bundlelane.FluxPath, bundlelane.FluxSourcePath} {
		if atoms.Present(t, p) {
			return bundlelane.Injection{}, Findings("%s is committed, and the lane writes it — the injected key would be overwritten without anyone seeing", p)
		}
	}
	in := bundlelane.Inputs{FluxSHA: flux.SHA, Tier: data, ServiceManifests: map[string]string{}}
	var bad *Fault
	if in.Entries, bad = readGlob(t, bundlelane.DeclaredGlob); bad != nil {
		return bundlelane.Injection{}, bad
	}
	if in.StarManifests, bad = readGlob(flux.Tree, bundlelane.StarManifestGlob); bad != nil {
		return bundlelane.Injection{}, bad
	}
	if in.ClusterManifest, bad = readGlob(flux.Tree, bundlelane.ClusterGlob); bad != nil {
		return bundlelane.Injection{}, bad
	}
	for _, pattern := range bundlelane.ServiceGlobs {
		files, bad := readGlob(flux.Tree, pattern)
		if bad != nil {
			return bundlelane.Injection{}, bad
		}
		for p, b := range files {
			in.ServiceManifests[p] = b
		}
	}
	inj, err := bundlelane.Inject(in)
	if err != nil {
		return bundlelane.Injection{}, Findings("%v", err)
	}
	Say("fleet: %d catalog entries; flux %.12s states %d of %d known stars (%d CNPG clusters)", inj.DeclaredRows, flux.SHA, inj.FluxRows, inj.Known, inj.Clusters)
	return inj, nil
}

// stage lays the tree the roster is built from. The catalog entries
// (fleet/repos/<n>.json) reach the die through the staged declared/data.json, not
// as themselves, and nothing under fleet/stars/ is bundle data: opa build reads
// data.json, and a fleet/stars/<n>/data.json beside the derived data.fleet.stars
// rule is a document and a rule at one path, which OPA refuses.
func (r Run) stage(inj bundlelane.Injection) *Fault {
	dir := StageDir()
	if err := os.RemoveAll(dir); err != nil {
		return CouldNotRun("the stage %s could not be cleared: %v", dir, err)
	}
	if err := copyTree(filepath.Join(r.In.Root, "fleet"), filepath.Join(dir, "fleet"), FleetExcluded); err != nil {
		return CouldNotRun("fleet/ could not be staged: %v", err)
	}
	for rel, body := range map[string]string{
		bundlelane.DeclaredPath:   inj.Declared,
		bundlelane.FluxPath:       inj.Flux,
		bundlelane.FluxSourcePath: inj.Source,
		".manifest":               bundlelane.FleetManifest,
	} {
		if err := writeFile(dir, rel, body); err != nil {
			return CouldNotRun("%s could not be staged: %v", rel, err)
		}
	}
	return nil
}

// gateFleet interrogates the built roster: its manifest, the roster it
// evaluates to, what was injected, and the composition with the policy.
func (r Run) gateFleet(ctx context.Context, scratch string, inj bundlelane.Injection, fluxSHA string) *Fault {
	plain := filepath.Join(OutDir(), FleetPlain)
	built, bad := ReadFile(plain)
	if bad != nil {
		return bad
	}
	manifest, ok, err := Member(built, "/.manifest")
	if err != nil || !ok {
		return Findings("%s carries no /.manifest: %v", FleetPlain, err)
	}
	if !bundlelane.RootsAreFleet(manifest) {
		return Findings("the roster's manifest no longer declares exactly [fleet]: %s", manifest)
	}
	// Evaluated, not read out of the tarball: data.fleet.stars may be a rule.
	roster, code, bad := r.Exec(ctx, "evaluating the built roster", r.In.Root, "", "opa", "eval", "-b", plain, "--format", "raw", "data")
	if bad != nil {
		return bad
	}
	if code != 0 {
		return Findings("the built roster does not evaluate:\n%s", roster)
	}
	rows, stars, topics, problem, err := bundlelane.Roster(roster)
	if err != nil {
		return Findings("%v", err)
	}
	if problem != "" {
		return Findings("%s", problem)
	}
	Say("fleet: roster — %d repos mapped, %d stars, %d topics", rows, stars, topics)
	if problem := bundlelane.Injected(roster, inj.DeclaredRows, inj.FluxRows, fluxSHA); problem != "" {
		return Findings("%s", problem)
	}

	// It composes with the policy bundle, and the guards FIRE.
	policy := filepath.Join(scratch, "policy.tar.gz")
	out, code, bad := r.Exec(ctx, "opa build", r.In.Root, "", "opa", "build", "-b", "policy/", "-o", policy, "--revision", "gate", "--ignore", "*_test.rego")
	if bad != nil {
		return bad
	}
	if code != 0 {
		return Findings("opa build over policy/ for the composition gate failed:\n%s", out)
	}
	probe := filepath.Join(scratch, "planted-seams.json")
	if err := osWrite(probe, []byte(bundlelane.PlantedSeams), 0o644); err != nil {
		return CouldNotRun("the composition probe could not be written: %v", err)
	}
	out, code, bad = r.Exec(ctx, "the composition probe", r.In.Root, "", "opa",
		"eval", "-b", policy, "-b", plain, "-i", probe, "--format", "json", "data.admission.deny")
	if bad != nil {
		return bad
	}
	if code != 0 {
		return Findings("the roster does not compose with the policy bundle — opa refused to load both:\n%s", out)
	}
	denies, err := checks.OpaDenySet(out)
	if err != nil {
		return CouldNotRun("the composition probe answered a shape this lane cannot read: %v", err)
	}
	if n := bundlelane.RosterDenials(denies); n != 2 {
		return Findings("planted seam violations produced %d/2 roster denials — data.fleet did not compose, or the guards stopped matching", n)
	}
	Say("fleet: composition gate — 2/2 planted seam violations denied")
	return nil
}
