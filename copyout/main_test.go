package main

import (
	"os"
	"testing"
)

// main exits with what copyout.Main answers: no "--" is a usage error.
func TestMainExitsWithTheRunsCode(t *testing.T) {
	args := os.Args
	got := -1
	exit = func(code int) { got = code }
	t.Cleanup(func() { exit, os.Args = os.Exit, args })
	os.Args = []string{"copyout", "a=b"}
	main()
	if got != 2 {
		t.Errorf("exit %d, want 2", got)
	}
}
