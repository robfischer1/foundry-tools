package checks

import (
	"cmp"
	"slices"
	"strings"
)

// catalogueOrder is the order of the catalogue: the order the atoms were
// declared in when the table was the whole of it, now the order the catalogue
// keeps whichever file an atom lives in. IT MEANS SOMETHING. A stage that runs
// its atoms in sequence stops at the first red, so the cheap atoms come first
// (the push stage: "cheapest first"), and a vector lists atoms in this order.
// An atom that moves into its own file keeps its place here; a new atom is added
// where it belongs. An id not listed sorts after the listed ones, by id
// (TestEveryCatalogueIDHasAPlace holds the list to the catalogue).
var catalogueOrder = []string{
	"fleet:check-yaml",
	"fleet:check-added-large-files",
	"fleet:check-merge-conflict",
	"fleet:manifest-one-command",
	"fleet:stop-justifications",
	"fleet:sast-ruleset-lanes",
	"fleet:copier-answers-intact",
	"fleet:ourea-config-retired-keys",
	"fleet:wit-topics",
	"fleet:pdp-wired",
	"fleet:renovate-config",
	"fleet:retired-verbs",
	"fleet:orbit-drift",
	"fleet:dagger-lockstep",
	"fleet:renovate-lockstep",
	"fleet:node-kinds-declared",
	"fleet:consumed-events-emitted",
	"fleet:opengrep-sast",
	"fleet:kafka-flows",
	"fleet:hadolint",
	"ops:shell",
	"ops:chezmoi",
	"ops:yaml",
	"ops:dup",
	"ops:declaration",
	"ops:specs",
	"ops:metrics",
	"ops:star-sa",
	"ops:world-patches",
	"ops:record-lanes",
	"ops:ansible",
	"ops:flux",
	"ops:kube-linter",
	"ops:immutable",
	"ops:orbit-sidecars",
	"ops:orbit-composed",
	"template:render-matrix",
	"wit:validate",
	"go:gofmt",
	"go:vet",
	"go:test",
	"go:staticcheck",
	"go:govulncheck",
	"go:build",
	"go:release",
	"go:test-race",
	"go:diff-coverage",
	"python:ruff-check",
	"python:ruff-format",
	"python:forge-testkit-assertion-free",
	"python:forge-testkit-fake-placement",
	"python:forge-testkit-schema-budget",
	"python:mypy",
	"python:pytest",
	"python:pip-audit",
	"python:release",
	"rust:cargo-fmt",
	"rust:cargo-clippy",
	"rust:cargo-test",
	"rust:release",
	"rust:cargo-audit",
	"rust:wit-guest",
	"rust:wit-compose",
	"ts:bun-gate",
	"ts:bun-audit",
	"ts:regen",
	"ts:release",
	"compose:config",
	"compose:no-tracked-secrets",
	"compose:third-party-pins",
	"dies:opa-test",
	"dies:admission-dogfood",
	"dies:data-keys",
	"dies:canary-visibility",
	"dies:contracts",
	"dies:contract-copies",
	"fleet:witness",
	"dies:schema",
	"dies:findings",
	"dies:schemas",
	"dies:wit-regenerated",
	"dies:schema-rendered",
	"dies:canonical",
	"dies:refusal-codes",
	"dies:wire-grammar",
	"dies:grants-stamp",
	"dies:resource-referents",
	"dies:capabilities-served",
	"dies:render-author",
	"dies:authz-grid",
	"law:lint",
	"law:verb-liveness",
	"law:budget",
	"orbit:contracts",
	"orbit:surface",
	"orbit:sidecars",
	"orbit:repo",
	"ts:visual",
	"go:mutation",
	"python:mutation",
	"rust:mutation",
	"ts:mutation",
}

// ordered is rows in the catalogue's order.
func ordered(rows []AtomDef) []AtomDef {
	place := func(id string) int {
		if i := slices.Index(catalogueOrder, id); i >= 0 {
			return i
		}
		return len(catalogueOrder)
	}
	slices.SortStableFunc(rows, func(a, b AtomDef) int {
		return cmp.Or(place(a.ID)-place(b.ID), strings.Compare(a.ID, b.ID))
	})
	return rows
}
