package main

import (
	"dagger/foundry-tools/internal/dagger"
)

// THE HELPER CLIs. Seven small programs in this module's own directories run in
// the lanes' containers (castpin, hadescall, verdict, witnesscall, pgroupps,
// execmem, copyout). Each used to be built from dag.CurrentModule().Source() WHOLE, so
// the build's cache key was a function of every file in the module and an
// edit to a README rebuilt all six; moduleBinary also built with no cache
// volumes and GOPROXY=off. Now every helper builds the way the atoms binary
// does (atomsBinary): in goToolchain(), with the Go cache volumes mounted, from
// go.mod, go.sum, the helper's own directory and the internal packages it
// imports, and nothing else.

// helperSource is what one helper is built from.
type helperSource struct {
	// internal is the module's own packages the helper imports, transitively,
	// as directories. TestHelperSourcesCoverTheirImports holds the list to the
	// import closure in both directions: a package the helper imports and this
	// omits is a `go build` that fails in the engine where no test can see it,
	// and a stale entry rebuilds the helper for edits it does not read.
	internal []string
	// offline builds with the network refused. verdict, execmem, pgroupps and copyout
	// import the standard library only and have to build when nothing could be
	// fetched (internal/verdict has the measurement); the others import
	// third-party modules the cache volume or the proxy supplies.
	offline bool
}

// helperSources is every helper, by the directory and binary name.
var helperSources = map[string]helperSource{
	"castpin":     {internal: []string{"internal/buildlane", "internal/castlane"}},
	"hadescall":   {internal: []string{"internal/hadescall"}},
	"verdict":     {internal: []string{"internal/verdict"}, offline: true},
	"witnesscall": {internal: []string{"internal/checks", "internal/execmem", "internal/hadescall", "internal/unitkey", "internal/witnesscall"}},
	"pgroupps":    {internal: []string{"internal/pgroupps"}, offline: true},
	"execmem":     {internal: []string{"internal/execmem"}, offline: true},
	"copyout":     {internal: []string{"internal/copyout"}, offline: true},
}

// helperInclude is the filtered source of a helper: the module files, the
// helper's directory and its internal packages, as the directories' globs.
func helperInclude(name string) []string {
	include := []string{"go.mod", "go.sum", name + "/**"}
	for _, dir := range helperSources[name].internal {
		include = append(include, dir+"/**")
	}
	return include
}

// helperBinary is the named helper, built static from its filtered source.
// Tests are excluded: they are not compiled into the program, and a test edit
// must not rebuild it. An offline helper also pins the toolchain to the one in
// the image and refuses the network, as moduleBinary and settle did.
func helperBinary(name string) *dagger.File {
	src := dag.CurrentModule().Source().Filter(dagger.DirectoryFilterOpts{
		Include: helperInclude(name),
		Exclude: []string{"**/*_test.go"},
	})
	ctr := goToolchain()
	if helperSources[name].offline {
		ctr = ctr.WithEnvVariable("GOTOOLCHAIN", "local").WithEnvVariable("GOPROXY", "off")
	}
	return ctr.
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithExec([]string{"go", "build", "-trimpath", "-o", "/out/" + name, "./" + name}).
		File("/out/" + name)
}
