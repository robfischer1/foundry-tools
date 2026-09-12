package main

import (
	"context"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// legacyVerdict runs an atom that still carries a shell Script — the shape
// this module is leaving (runtime.go says why). It exists so the branch that
// ports the lanes one file at a time keeps answering the door's vector at
// every commit; it is DELETED, with AtomDef.Script and every prelude in
// atoms.go, once registry.go names all 48 atoms.
func legacyVerdict(ctx context.Context, r *run, a checks.AtomDef) checks.Verdict {
	ctr := r.lane(a.Image).
		WithEnvVariable("GATE_BASE", r.base).
		WithEnvVariable("CRD_SCHEMA_LOCATION", checks.CRDSchemaLocation).
		WithEnvVariable("CRD_SCHEMA_PROBE", checks.CRDSchemaProbe).
		WithEnvVariable("ORAS_MIRROR", checks.OrasMirror).
		WithEnvVariable("ORAS_URL", checks.OrasURL).
		WithEnvVariable("COMPOSE_VERSION", checks.ComposeVersion).
		WithEnvVariable("COMPOSE_MIRROR", checks.ComposeMirror).
		WithEnvVariable("COMPOSE_URL", checks.ComposeURL).
		WithEnvVariable("OPA_VERSION", checks.OpaVersion).
		WithEnvVariable("OPA_MIRROR", checks.OpaMirror).
		WithEnvVariable("OPA_URL", checks.OpaURL)
	if a.NeedsStocks {
		ctr = r.withStocks(ctr)
	}
	if a.NeedsDies {
		ctr = r.withDies(ctr)
	}
	return verdict(ctx, a, ctr.WithExec(
		[]string{"sh", "-c", a.Script},
		dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny},
	))
}
