package bundleresign

import (
	"fmt"
	"io"
	"os"
)

const usage = `usage:
  bundleresign input <bundle.tar.gz> <keyid>           print the header.payload a KMS signs
  bundleresign apply <bundle.tar.gz> <keyid> <sigfile>  write the KMS's base64 DER signature into the bundle, in place`

// writeFile is os.WriteFile, a variable so a test can fail it: as root no
// permission does.
var writeFile = os.WriteFile

// Main is bundleresign's process.
func Main(args []string, stdout, stderr io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintf(stderr, "bundleresign: %v\n", err)
		return 1
	}
	if len(args) < 3 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	tgz, err := os.ReadFile(args[1])
	if err != nil {
		return fail(err)
	}
	switch {
	case args[0] == "input" && len(args) == 3:
		in, err := SigningInput(tgz, args[2])
		if err != nil {
			return fail(err)
		}
		fmt.Fprint(stdout, in)
		return 0
	case args[0] == "apply" && len(args) == 4:
		sig, err := os.ReadFile(args[3])
		if err != nil {
			return fail(err)
		}
		out, err := Apply(tgz, args[2], string(sig))
		if err != nil {
			return fail(err)
		}
		if err := writeFile(args[1], out, 0o600); err != nil {
			return fail(err)
		}
		return 0
	}
	fmt.Fprintln(stderr, usage)
	return 2
}
