package witnesscall

import (
	"bytes"
	"context"
	"os"
	"strings"
)

// Exec is Run as the atom reads it: the helper's output and exit code, with no
// process. fleet:witness used to exec a witnesscall program; the in-pod
// executor's lead container carries none (the atom needs only svid, and the
// index pins no such tool), so the exec never ran there and every ask went to
// the clear port as an `unidentified` caller. The SVID is fetched from the
// Workload API in the atom's own process now, on both runners.
//
// env is KEY=VALUE pairs, the shape of an exec's environment; a name it does
// not carry reads as unset. The output is stdout, with stderr after it when
// the code is not 0, trimmed: what a program exec would have answered.
func Exec(ctx context.Context, args, env []string) (string, int) {
	var stdout, stderr bytes.Buffer
	code := Run(ctx, args, lookup(env), os.ReadFile, &stdout, &stderr)
	out := stdout.String()
	if code != 0 {
		out += stderr.String()
	}
	return strings.TrimSpace(out), code
}

// lookup reads a name from KEY=VALUE pairs.
func lookup(env []string) func(string) string {
	return func(name string) string {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, name+"="); ok {
				return v
			}
		}
		return ""
	}
}
