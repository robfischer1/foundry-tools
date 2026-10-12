// svidcred is a docker credential helper that logs in to the registry with a
// JWT-SVID fetched, at the moment of the call, from the SPIRE agent's socket.
// The logic is in internal/svidcred; this is only its process. Installed as
// docker-credential-svid, it is what a docker config's credHelpers names.
//
//	docker-credential-svid get|list|store|erase
package main

import (
	"context"
	"os"

	"dagger/foundry-tools/internal/svidcred"
)

func main() {
	os.Exit(svidcred.Run(context.Background(), os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr))
}
