// hadescall asks hades one verb over mTLS, as the SVID the SPIRE agent's
// socket issues. The logic, and why it looks the way it does, is in
// internal/hadescall; this is only its process.
//
//	hadescall <verb> <json-args>
package main

import (
	"context"
	"os"

	"dagger/foundry-tools/internal/hadescall"
)

func main() {
	os.Exit(hadescall.Run(context.Background(), os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}
