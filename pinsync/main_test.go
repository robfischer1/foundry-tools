package main

import (
	"os"
	"testing"
)

// main exits with what pinsync.Main answers: a tree that is not there is a
// run that could not happen.
func TestMainExitsWithTheRunsCode(t *testing.T) {
	args := os.Args
	got := -1
	exit = func(code int) { got = code }
	t.Cleanup(func() { exit, os.Args = os.Exit, args })
	os.Args = []string{"pinsync", "-n", "-C", t.TempDir()}
	main()
	if got != 2 {
		t.Errorf("exit %d, want 2", got)
	}
}
