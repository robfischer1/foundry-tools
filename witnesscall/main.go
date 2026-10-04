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

func main() {
	os.Exit(witnesscall.Run(context.Background(), os.Args[1:], os.Getenv, os.ReadFile, os.Stdout, os.Stderr))
}
