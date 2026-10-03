package main

import (
	"context"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

func init() {
	register("ops:orbit-sidecars", opsOrbitSidecars)
}

// ops:orbit-sidecars — every composed orbit sidecar an ops tree tracks parses
// with the reader the star itself runs.
//
// WHY IT EXISTS. A star on mcpserve.Door in witness mode reads
// /etc/stellar/orbit.toml at boot, and stellar-core-go policy.New REFUSES TO
// START on a sidecar that is present and does not parse (an absent one is a
// warning). The sidecars reach that path as ConfigMaps flux renders from
// prime/orbits (foundry-tools orbitcompose), so a sidecar the reader rejects
// would land, roll, and leave its star down. This is the same parse, run in
// the flux pull, before the mount.
//
// THE READER IS IMPORTED, NOT RE-IMPLEMENTED. The tool is a ten-line main
// over policy.ParseACL, pinned by its own go.mod and go.sum (orbitParseDir)
// and built in the Go toolchain container through the fleet goproxy. A port
// of the parser here would agree with the star's only until one of them
// changed.
//
// THE THREE STATES. 0: every sidecar parses (or the tree tracks none, or is
// not an ops tree). 1: at least one would refuse its star's boot, each named.
// 2: the tool could not be built (the proxy, the toolchain) or a file could
// not be read — a sidecar nobody parsed is not a sidecar that parses.
func opsOrbitSidecars(ctx context.Context, r *run) checks.Verdict {
	return opsAtom(ctx, r, "ops:orbit-sidecars", opsOrbitSidecarsPhase, nil)
}

// opsOrbitSidecarsPhase hands every tracked sidecar to the reader and settles
// on its exit: 0 and 1 are its verdict, anything else could not run. The
// reader is built HERE, not as the atom's provisioning: every ops tree runs
// this atom, one tracks sidecars, and the rest must not pay a module download
// and a build to learn they have none.
func opsOrbitSidecarsPhase(ctx context.Context, ctr *dagger.Container, files []checks.OpsFile) (opsResult, error) {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	sidecars := checks.OrbitSidecars(paths)
	if len(sidecars) == 0 {
		return opsResult{absent: "this tree tracks no composed orbit sidecar (*.orbit.toml), so no star reads one from it"}, nil
	}
	args := []string{"/usr/local/bin/orbitparse"}
	for _, s := range sidecars {
		args = append(args, "/src/"+s)
	}
	ctr = ctr.WithFile("/usr/local/bin/orbitparse", orbitParse(), dagger.ContainerWithFileOpts{Permissions: 0o755})
	_, out, rc, err := opsRun(ctx, ctr, args)
	if err != nil {
		return opsResult{}, err
	}
	return opsResult{state: min(rc, 2), out: out}, nil
}

// orbitParseDir is the reader's own Go module, inside this module's source.
//
// A REAL go.mod, SO RENOVATE BUMPS IT. The pin was three embedded .txt files
// until 2026-10-03, and Renovate's gomod manager reads only files named
// go.mod: stellar-core-go sat at v0.66.0 while the library reached v0.69.0
// and the stars moved with it (Task #104.2). A go.mod makes the directory a
// nested module, which go:embed refuses to reach into, so the tool is read
// from the module's source at run time, as execmem's is. The leading "_"
// keeps it out of this module's own build and out of every lane's module
// enumeration (checks.GoModuleDirs), exactly as the .txt names did.
const orbitParseDir = "internal/checks/scripts/_orbitparse"

// orbitParse builds the reader. The build is one exec under the default
// Expect, so a proxy that will not serve the reader is an engine error
// (state 2, re-asked), never a finding.
func orbitParse() *dagger.File {
	return goToolchain().
		WithMountedDirectory("/orbitparse", dag.CurrentModule().Source().Directory(orbitParseDir)).
		WithWorkdir("/orbitparse").
		WithExec([]string{"go", "build", "-trimpath", "-o", "/out/orbitparse", "."}).
		File("/out/orbitparse")
}
