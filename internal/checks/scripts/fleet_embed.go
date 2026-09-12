// Package scripts carries the scripts that ARE an atom's tool, embedded in the
// module binary rather than written inline as a heredoc inside a shell string.
//
// Rule 6 of runtime.go: a script that is the tool stays the tool. It stops
// being a heredoc, because a heredoc inside a Go string inside `sh -c` is a
// program nothing can lint, test or syntax-check — the orbit-drift body was 76
// lines of Python that only python3 inside a container had ever parsed. Here it
// is a `.py` file: `gofmt` leaves it alone, an editor highlights it, `python3
// -m py_compile` can read it, and the atom mounts it with WithNewFile.
package scripts

import "embed"

// Fleet holds the fleet lane's scripts.
//
//go:embed fleet_*.py
var Fleet embed.FS
