// orbitparse parses each composed orbit sidecar named on its command line with
// stellar-core-go's own policy.ParseACL — the reader a star in witness mode
// runs at boot, which refuses to start on a sidecar it cannot parse. The star
// is the file's name up to ".orbit.toml".
//
// Exit 0: every sidecar parses. 1: at least one does not (each is named).
// 2: no file was named, or a file could not be read.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.notusmi.com/rob/stellar-core-go/policy"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: orbitparse <star>.orbit.toml...")
		os.Exit(2)
	}
	bad := 0
	for _, path := range os.Args[1:] {
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "CANNOT RUN - %v\n", err)
			os.Exit(2)
		}
		star := strings.TrimSuffix(filepath.Base(path), ".orbit.toml")
		acl, err := policy.ParseACL(star, raw)
		if err != nil {
			fmt.Printf("REFUSED  %s: %v\n", path, err)
			bad++
			continue
		}
		fmt.Printf("parses   %s: %d consumer(s)\n", path, acl.Len())
	}
	if bad > 0 {
		fmt.Printf("FINDINGS - %d of %d sidecar(s) would refuse their star's boot in witness mode\n", bad, len(os.Args)-1)
		os.Exit(1)
	}
	fmt.Printf("PASS - %d sidecar(s) parse with stellar-core-go policy.ParseACL\n", len(os.Args)-1)
}
