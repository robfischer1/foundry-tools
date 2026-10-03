package scripts

import _ "embed"

// FleetOrbitDrift is fleet:orbit-drift's checker, byte for byte the script
// that used to live in a heredoc.
//
//go:embed fleet_orbit_drift.py
var FleetOrbitDrift string

// OrbitParse is ops:orbit-sidecars' tool: a main package that parses each
// sidecar named on its command line with stellar-core-go policy.ParseACL,
// with the go.mod and go.sum that pin the reader. The three are .txt so this
// module neither compiles the program (it imports a module foundry-tools
// does not require) nor reads the directory as a nested module go:embed
// would refuse to reach into.
//
//go:embed orbitparse.go.txt
var OrbitParse string

// OrbitParseMod is OrbitParse's go.mod.
//
//go:embed orbitparse.mod.txt
var OrbitParseMod string

// OrbitParseSum is OrbitParse's go.sum.
//
//go:embed orbitparse.sum.txt
var OrbitParseSum string
