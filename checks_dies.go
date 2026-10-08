package main

import (
	"context"

	"dagger/foundry-tools/internal/dagger"
)

// Dies is the policy die's namespace: the checks that were foundry-dies'
// `ci.yml`, `contracts.yml` and `schema.yml` on the act-runner.
//
// SIX ATOMS AND TWO DIFFERENT QUESTIONS. OpaTest and AdmissionDogfood grade the
// SOURCE tree; DataKeys and CanaryVisibility grade the BUILT BUNDLE, and the
// gap between them is the reason the third gate was written in the first place:
// `opa test policy/` passes on a tree whose built bundle is empty, because
// directory mode merges every *.json by top-level key while bundle mode reads
// only files literally named data.json. Empty data means star_only undefined,
// which means every verb visible to every principal — fail-open, silent, and
// green the whole way down. So the gate never trusts the source tree as a proxy
// for the artifact.
//
// THE CONDITION IS THE TREE'S SHAPE, not its name: policy/.manifest and
// fleet/stars/ together, because either alone is a name another repo could
// reasonably take. Everywhere else these report ABSENT and say why.
//
// build.yml and fleet-bundle.yml are NOT here. They publish rather than
// validate, and the bundle recipe lane owns them.
type Dies struct {
	// +private
	Source *dagger.Directory
}

// The rego unit and invariant suite passes — and a policy tree with no
// assertion in it is a CANNOT RUN, because `opa test` exits 0 over one.
//
// +check
func (d *Dies) OpaTest(ctx context.Context) (string, error) {
	return check(ctx, d.Source, "dies:opa-test")
}

// The admission domain admits this repo's own star shape. A deny means the
// policy has drifted from the fleet it governs, reported on the repo that owns
// the rule rather than on whichever star was poured next.
//
// +check
func (d *Dies) AdmissionDogfood(ctx context.Context) (string, error) {
	return check(ctx, d.Source, "dies:admission-dogfood")
}

// The BUILT bundle carries every data root the policy reads, non-empty. An
// empty or partial data document is the silent fail-open, and it is invisible
// from the source tree.
//
// +check
func (d *Dies) DataKeys(ctx context.Context) (string, error) {
	return check(ctx, d.Source, "dies:data-keys")
}

// The BUILT bundle still hides what it should: a curated verb is invisible to a
// session principal and visible to a star principal. The canary is read off the
// bundle's own roster rather than named, because a named canary goes stale the
// day its verb is retired — which happened twice.
//
// +check
func (d *Dies) CanaryVisibility(ctx context.Context) (string, error) {
	return check(ctx, d.Source, "dies:canary-visibility")
}

// Every copy of every shared closed set agrees, after the fixtures prove the
// checker still detects — seven that must fail and three controls that must
// pass. A gate that cannot fail is a gate that is not there.
//
// +check
func (d *Dies) Contracts(ctx context.Context) (string, error) {
	return check(ctx, d.Source, "dies:contracts")
}

// The slag v1 schema (the published slag-schema die) and the v3 schema are valid
// Draft 2020-12 documents whose `required` names only defined properties, and
// every fleet/stars/*/slag.json satisfies v3.
//
// +check
func (d *Dies) Schema(ctx context.Context) (string, error) {
	return check(ctx, d.Source, "dies:schema")
}

// The findings schema is a valid Draft 2020-12 document and still refuses what
// it exists to refuse: every fixture under tests/findings classifies as its
// name claims, and the invalid-* cases are the assertions — positives alone
// would pass against a schema with every constraint removed.
//
// +check
func (d *Dies) Findings(ctx context.Context) (string, error) {
	return check(ctx, d.Source, "dies:findings")
}

// The committed wit/ is byte-for-byte what tools/wit-from-schema generates from
// schema/: a schema change without regeneration, or a hand edit of a generated
// file, fails. The generator's refusals are listed in a generated report that
// is compared too, so a new refusal is a diff, not a surprise.
//
// +check
func (d *Dies) WitRegenerated(ctx context.Context) (string, error) {
	return check(ctx, d.Source, "dies:wit-regenerated")
}

// Every die schema is the byte-for-byte render of the shapes its snapshot holds:
// a schema edited by hand and never dissolved back through the shape door, a
// schema with no shape, or a snapshot nobody rendered fails.
//
// +check
func (d *Dies) SchemaRendered(ctx context.Context) (string, error) {
	return check(ctx, d.Source, "dies:schema-rendered")
}

// Every committed record is byte-for-byte its own canonical form — the form
// hephaestus' golden grades one repo away, refused here at the push instead.
//
// +check
func (d *Dies) Canonical(ctx context.Context) (string, error) {
	return check(ctx, d.Source, "dies:canonical")
}

// Every refusal code a world or a star spells is in the fleet registry, and
// every registered code is still spelled: in foundry-dies the whole registry
// against every use, in stellar-core its tapes and enums against dies' registry.
//
// +check
func (d *Dies) RefusalCodes(ctx context.Context) (string, error) {
	return check(ctx, d.Source, "dies:refusal-codes")
}
