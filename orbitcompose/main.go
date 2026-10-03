// orbitcompose composes each star's orbit sidecar from foundry-dies/orbits
// and renders the set into the Flux directory it owns. The logic, and why it
// looks the way it does, is in internal/orbitcompose; this is only its
// process.
//
//	go run ./orbitcompose -contracts ../foundry-dies/orbits -out ../flux/prime/orbits [-check]
package main

import (
	"os"

	"dagger/foundry-tools/internal/orbitcompose"
)

// exit is os.Exit, and a variable so main_test.go can run main: go:mutation
// grades a line no test executes as NOT COVERED.
var exit = os.Exit

func main() { exit(orbitcompose.Main(os.Args[1:], os.Stdout, os.Stderr)) }
