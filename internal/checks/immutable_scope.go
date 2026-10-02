package checks

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// WHICH PATHS ops:immutable RENDERS. A Kustomization path whose inputs did not
// change between the merge base and the head renders the same stream on both
// sides, so it cannot carry an edit to an immutable field and rendering it
// twice proves nothing. Measured 2026-10-02 on foundry/flux (Shannon37): with
// every path rendered at head AND base, the atom ran ~46 `kubectl kustomize`
// execs per gate and took the flux gate from ~30s to ~60s, on the fleet's most
// frequent gate (every image pin lands through one).
//
// A path's inputs are its own directory and everything its kustomization.yaml
// files reach through a `../` reference, followed recursively: blades/ reads
// ../prime/images, alloy/ reads ../hemera. The reference scan is a plain text
// match, comments included, so it can only widen the set; a path is skipped
// only when no changed file lies under any directory or file it reaches.

// immutableRef is a relative reference that leaves the directory it is in.
var immutableRef = regexp.MustCompile(`(?:\.\./)+[A-Za-z0-9_][A-Za-z0-9_./-]*`)

// ImmutableInputs answers what the Kustomization at dir reads: dir itself and
// every directory or file its kustomization.yaml (and theirs, recursively)
// references through `../`, cleaned and repository-relative. read answers a
// directory's kustomization.yaml, or "" when it has none.
func ImmutableInputs(dir string, read func(dir string) string) []string {
	seen := map[string]bool{}
	var walk func(d string)
	walk = func(d string) {
		d = path.Clean(d)
		if seen[d] {
			return
		}
		seen[d] = true
		for _, ref := range immutableRef.FindAllString(read(d), -1) {
			walk(path.Join(d, strings.TrimRight(ref, "/")))
		}
	}
	walk(dir)
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// ImmutableTouched answers whether any changed file is one of inputs or lies
// under one of them.
func ImmutableTouched(inputs, changed []string) bool {
	for _, c := range changed {
		for _, in := range inputs {
			if c == in || strings.HasPrefix(c, in+"/") {
				return true
			}
		}
	}
	return false
}
