// verdict prints a lane's reason and exits with its verdict. The logic, and
// why it looks the way it does, is in internal/verdict; this is only its
// process.
//
//	verdict <code> <reason...>
package main

import (
	"os"

	"dagger/foundry-tools/internal/verdict"
)

func main() { os.Exit(verdict.Run(os.Args[1:], os.Stderr)) }
