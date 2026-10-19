package releasekit

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
)

// ManifestFile is the release.toml a star keeps at its repository root (the
// root module is the only one go:release builds) when its image's compile is
// not the fleet's default. It declares what the Dockerfile's COPY lines
// cannot: a cgo build, its tags and its ldflags (lld FN-124, FN-128).
//
//	[[binary]]
//	package = "./cmd/narc"
//	out     = "release/narc"
//	cgo     = true
//	tags    = ["netgo", "osusergo"]
//	ldflags = '-s -w -extldflags "-static"'
//
// A tree without one is built exactly as before.
const ManifestFile = "release.toml"

// RootPackage spells the module root as a [[binary]] package, for a star whose
// main package is the module itself (go build .). It is the only spelling:
// "./" and "./." stay refused, and the root needs no directory to be checked.
const RootPackage = "."

type manifest struct {
	Binary []struct {
		Package string   `toml:"package"`
		Out     string   `toml:"out"`
		Cgo     bool     `toml:"cgo"`
		Tags    []string `toml:"tags"`
		Ldflags string   `toml:"ldflags"`
	} `toml:"binary"`
}

// ReadManifest parses a release.toml into the binaries it declares, sorted by
// name. Every refusal is about the file and settles the atom as a could-not-run.
func ReadManifest(body string) ([]checks.ReleaseBinary, error) {
	var m manifest
	md, err := toml.Decode(body, &m)
	if err != nil {
		return nil, fmt.Errorf("%s does not parse: %w", ManifestFile, err)
	}
	if un := md.Undecoded(); len(un) > 0 {
		return nil, fmt.Errorf("%s has an unknown key %s", ManifestFile, un[0])
	}
	if len(m.Binary) == 0 {
		return nil, fmt.Errorf("%s declares no [[binary]], so the release build has nothing to compile", ManifestFile)
	}
	var out []checks.ReleaseBinary
	for _, b := range m.Binary {
		pkg := strings.TrimPrefix(b.Package, "./")
		switch {
		case b.Package == "":
			return nil, fmt.Errorf("a [[binary]] in %s names no package", ManifestFile)
		case b.Package == RootPackage:
			// the module root itself: a star whose main package is the module
		case pkg == b.Package || pkg == "" || path.Clean(pkg) != pkg || strings.HasPrefix(pkg, "../") || pkg == "..":
			return nil, fmt.Errorf("%s package %q is not a ./ package inside the module", ManifestFile, b.Package)
		}
		name, ok := strings.CutPrefix(b.Out, buildlane.ReleaseDir+"/")
		if !ok || name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "-") {
			return nil, fmt.Errorf("%s out %q must be release/<name>", ManifestFile, b.Out)
		}
		for _, tag := range b.Tags {
			if tag == "" || strings.ContainsAny(tag, " \t\n,") {
				return nil, fmt.Errorf("%s tag %q is not a build tag", ManifestFile, tag)
			}
		}
		if strings.ContainsAny(b.Ldflags, "\n\r") {
			return nil, fmt.Errorf("%s ldflags for %s spans lines", ManifestFile, name)
		}
		if slices.ContainsFunc(out, func(o checks.ReleaseBinary) bool { return o.Name == name }) {
			return nil, fmt.Errorf("%s declares %s twice", ManifestFile, b.Out)
		}
		out = append(out, checks.ReleaseBinary{Name: name, Package: b.Package, Cgo: b.Cgo, Tags: b.Tags, Ldflags: b.Ldflags})
	}
	slices.SortFunc(out, func(a, b checks.ReleaseBinary) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}
