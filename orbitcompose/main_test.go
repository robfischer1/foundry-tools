package main

import (
	"os"
	"testing"
)

// main exits with what orbitcompose.Main answers: no flags is a usage error.
func TestMainExitsWithTheComposersCode(t *testing.T) {
	args := os.Args
	got := -1
	exit = func(code int) { got = code }
	t.Cleanup(func() { exit, os.Args = os.Exit, args })
	os.Args = []string{"orbitcompose"}
	main()
	if got != 2 {
		t.Errorf("exit %d, want 2", got)
	}
}
