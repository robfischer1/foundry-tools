// Package gogovulncheck is the atom go:govulncheck: govulncheck ./... reports no
// known vulnerability. It is a logic atom, not a command: its exit is remapped
// (3 is a finding) and an advisory the gate allows can turn a finding into a pass.
package gogovulncheck

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. govulncheck is the lane's tool, baked in at its pin.
var Def = atomkit.Def{
	ID: "go:govulncheck", Stage: checks.StagePrepush, Lane: checks.LaneGo,
	Group:     checks.GroupLanguage,
	Desc:      "govulncheck ./... reports no known vulnerability.",
	Surface:   "any:**/go.mod",
	Toolchain: "go",
	Needs:     []string{"tool:govulncheck"},
	Check:     check,
}

const id = "go:govulncheck"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneGo}

// check scans every Go module the tree carries.
//
// A LAYER, LIKE staticcheck. The old script `go install`ed
// golang.org/x/vuln/cmd/govulncheck@latest on every run; the lane installs it
// once at its pin, and the version probe is the check in its place.
//
// THE MODULES ARE DOWNLOADED FIRST. govulncheck loads the package graph before
// it scans, so a proxy that is briefly gone surfaces as a govulncheck failure,
// and at exit 1 that reads as a vulnerability finding. MEASURED 2026-09-10,
// gate-hades-c099a35: govulncheck red at "loading packages" while the door
// rolled onto 9a5313fb. A door that is briefly gone is not a verdict on the
// code, so the fetch is its own step and its failure is a 2.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return atoms.EachGoModule(in, row, func(dir string) checks.Verdict { return module(ctx, in, dir) })
}

// module scans one module.
func module(ctx context.Context, in atomkit.In, dir string) checks.Verdict {
	cwd := atoms.GoModuleRoot(in, dir)
	if v, ok := atoms.Provision(ctx, in, row, cwd, "go", "mod", "download"); !ok {
		return v
	}
	if v, ok := atoms.Provision(ctx, in, row, cwd, "govulncheck", "-version"); !ok {
		return v
	}
	run := func(ctx context.Context, c atoms.Cmd) (string, int) { return atoms.RunCmd(ctx, in, c) }
	out, code := run(ctx, atoms.Cmd{Dir: cwd, Name: "govulncheck", Args: []string{"./..."}})
	if code < 0 {
		return atoms.CouldNotStart(row, out)
	}
	// govulncheck's 3 is "vulnerabilities found" and reads as 1; any other
	// non-zero with a network fault in it is the database not answering, which
	// AuditVerdict reads as could-not-run.
	exit := checks.GovulncheckExit(code)
	// AND ONLY THEN, THE ALLOWANCE. It is checked against a FINDINGS exit alone —
	// never a could-not-run — because a scan that did not complete has found
	// nothing to allow, and letting an allowance answer for it would turn "the
	// advisory database was unreachable" into a pass. checks.SuppressedVulnReason
	// fails closed on anything it cannot parse and on any advisory not covered.
	if exit == 1 {
		if why, ok := checks.SuppressedVulnReason(out, in.Now); ok {
			return checks.AllowedVulnVerdict(row, why, out)
		}
	}
	return checks.AuditVerdict(row, exit, out)
}
