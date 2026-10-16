// Package atomkit is what a logic atom is built on: the declaration (Def), the
// process around its Check (Main), the wire shapes it reads and writes, and the
// parsers for the data files the executor and the atom builder share
// (command-atom@1, lane@1, tools@1). It imports internal/checks and nothing
// from package main (lld.md SYS-003-MOD-001).
//
// manifest.go, onecommand.go, lane.go, tools.go, tomlstrict.go, runplan.go,
// plan.go and index.go are copies of daedalus internal/runner/wire's files of
// the same names, equal after the package clause.
package atomkit
