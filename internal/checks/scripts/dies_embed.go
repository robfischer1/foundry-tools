// Package scripts carries the bodies that ARE a check rather than an
// invocation of one.
//
// Rule 6 of runtime.go: a script that is the tool stays the tool. The dies
// atoms embedded two python programs in shell heredocs — the door-reachability
// probe and the slag-schema validator — and a heredoc inside a Go string is a
// program nothing can lint, test, or diff. Embedded here they are ordinary
// files on disk: `python -m py_compile` reads them, a reviewer reads them, and
// the atom mounts the bytes with WithNewFile rather than writing them through
// a shell.
package scripts

import "embed"

// Dies is the dies lane's python: dies_door_probe.py (contracts' reachability
// probe) and dies_schema.py (the Draft 2020-12 + v2-record validator). Both
// moved here VERBATIM from internal/checks/atoms.go — same bytes, so the
// behaviour the old comments measured is the behaviour that runs.
//
//go:embed dies_*.py
var Dies embed.FS
