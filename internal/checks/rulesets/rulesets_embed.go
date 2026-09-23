// Package rulesets is the fleet's linter configuration, shipped WITH the atoms
// that enforce it.
//
// IT CAME HOME 2026-09-23 (CA F18). These three files lived in foundry-stocks
// ci/lib/rulesets and were read through a git mount at /stocks. The comments
// around that arrangement called it "their one home" and said the fleet decides
// the atoms AND their rulesets — and the principle is right, but the location
// was not: a ruleset fetched from another repo can drift from the atom that
// reads it, and the atom cannot tell. Embedded here they move together or not
// at all, which is what "one home" was always reaching for.
//
// A tool pointed at one of these ignores whatever config the repository under
// test carries. That is the point: the fleet's ruff decides, not the seven
// repos that had drifted from the template's copy by 2026-09-11.
package rulesets

import _ "embed"

// Ruff is python:ruff-check and python:ruff-format's configuration.
//
//go:embed ruff.toml
var Ruff string

// Mypy is python:mypy's configuration.
//
//go:embed mypy.ini
var Mypy string

// ESLint is ts:bun-gate's configuration.
//
//go:embed eslint.config.mjs
var ESLint string
