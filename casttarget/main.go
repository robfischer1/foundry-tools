// casttarget archives the cast lane's Rust release target in SeaweedFS. The
// logic, and why it looks the way it does, is in internal/casttarget; this is
// only its process.
//
//	casttarget creds
//	casttarget restore|save --repo URL --toolchain KEY --dir DIR
package main

import (
	"context"
	"os"

	"dagger/foundry-tools/internal/casttarget"
)

// exit is os.Exit, a variable so main_test.go can run main.
var exit = os.Exit

func main() {
	exit(casttarget.Main(context.Background(), os.Args[1:], os.Getenv, os.Stdout))
}
