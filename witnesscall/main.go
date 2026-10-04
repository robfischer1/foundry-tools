// witnesscall asks narcissus over its mTLS door as the CI lane's SVID. The
// logic, and why it looks the way it does, is in internal/witnesscall; this is
// only its process.
//
//	witnesscall whoami
//	witnesscall post <url> <request-file>
package main

import (
	"context"
	"os"

	"dagger/foundry-tools/internal/witnesscall"
)

// exit is os.Exit, and a variable so main_test.go can run main: go:mutation
// grades a line no test executes as NOT COVERED.
var exit = os.Exit

func main() {
	exit(witnesscall.Run(context.Background(), os.Args[1:], os.Getenv, os.ReadFile, os.Stdout, os.Stderr))
}
