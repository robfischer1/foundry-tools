// execmem runs a command and prints its exec's cgroup memory readings to
// stderr while it runs. The logic, and why it looks the way it does, is in
// internal/execmem; this is only its process.
//
//	execmem -- <command> [args...]
package main

import (
	"os"
	"time"

	"dagger/foundry-tools/internal/execmem"
)

func main() {
	os.Exit(execmem.Run(execmem.Command(os.Args[1:]), "/sys/fs/cgroup", time.Second, os.Stderr))
}
