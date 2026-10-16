package main

import (
	"os"
	"slices"
	"testing"

	"dagger/foundry-tools/internal/atoms"
)

// The binary links the catalogue, so the atoms that live in their own files are
// among the ones it votes for.
func TestTheBinaryCarriesThePortedAtoms(t *testing.T) {
	ids := atoms.StageIDs("precommit")
	for _, id := range []string{"fleet:check-merge-conflict", "fleet:manifest-one-command"} {
		if !slices.Contains(ids, id) {
			t.Errorf("%s is not among the precommit atoms the binary carries: %v", id, ids)
		}
	}
}

// main exits with what atoms.Run answers: an unknown flag is a usage error.
func TestMainExitsWithTheRunsCode(t *testing.T) {
	args := os.Args
	got := -1
	exit = func(code int) { got = code }
	t.Cleanup(func() { exit, os.Args = os.Exit, args })
	os.Args = []string{"atoms", "-bogus"}
	main()
	if got != 2 {
		t.Errorf("exit %d, want 2", got)
	}
}
