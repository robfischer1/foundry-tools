package main

import (
	"os"
	"testing"
)

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
