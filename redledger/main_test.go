package main

import (
	"os"
	"testing"
)

// main exits with what redledger.Main answers: two trees is a usage error,
// decided before hades is asked anything.
func TestMainExitsWithTheChecksCode(t *testing.T) {
	args := os.Args
	got := -1
	exit = func(code int) { got = code }
	t.Cleanup(func() { exit, os.Args = os.Exit, args })
	os.Args = []string{"redledger", "a", "b"}
	main()
	if got != 2 {
		t.Errorf("exit %d, want 2", got)
	}
}
