package main

import "dagger/foundry-tools/internal/dagger"

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
