package checks

import (
	"slices"
	"strings"
)

// OrbitSidecarSuffix names a composed orbit sidecar: <star>.orbit.toml, the
// file orbitcompose writes and ops:orbit-sidecars parses.
const OrbitSidecarSuffix = ".orbit.toml"

// OrbitSidecars answers the tracked composed sidecars, sorted. The star is the
// name before the suffix, so a bare ".orbit.toml" names no star and is not one.
func OrbitSidecars(files []string) []string {
	var out []string
	for _, f := range files {
		base := f[strings.LastIndex(f, "/")+1:]
		if strings.HasSuffix(base, OrbitSidecarSuffix) && base != OrbitSidecarSuffix {
			out = append(out, f)
		}
	}
	slices.Sort(out)
	return out
}
