package main

import (
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// Where `uv tool install` puts a tool's venv and its entry point in the python
// lane. The bin directory is already on PATH, so an atom execs the tool by name.
const (
	uvToolDir = "/opt/uv-tools"
	uvToolBin = "/usr/local/bin"
)

// withUVTools installs checks.PythonLaneTools into the lane, ONE EXEC PER TOOL
// AT AN EXACT VERSION, each followed by a probe. This is the go lane's `go
// install <module>@<version>` for python: the engine caches each exec by its
// inputs, so a tool is resolved and fetched once per pin and every atom that
// execs it reuses the layer, where `uvx ruff@x` and `uv run --with cosmic-ray`
// resolved it again on every run. The upstream python image is the only base;
// nothing here is a fleet-owned image.
//
// Runs in provision, before the cache volumes mount, so a layer never depends on
// what a volume happens to hold.
func withUVTools(ctr *dagger.Container) *dagger.Container {
	ctr = ctr.
		WithEnvVariable("UV_TOOL_DIR", uvToolDir).
		WithEnvVariable("UV_TOOL_BIN_DIR", uvToolBin)
	for _, t := range checks.PythonLaneTools {
		ctr = ctr.
			WithExec([]string{"uv", "tool", "install", t.Spec()}).
			WithExec(t.Probe)
	}
	return ctr
}
