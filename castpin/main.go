// castpin prints a payload tree's content pin, then its files in pin order.
// The logic, and why the pin is computed the way it is, is in
// internal/castlane; this is only its process.
//
//	castpin <dir>
package main

import (
	"os"

	"dagger/foundry-tools/internal/castlane"
)

func main() { os.Exit(castlane.RunPin(os.Args[1:], os.Stdout, os.Stderr)) }
