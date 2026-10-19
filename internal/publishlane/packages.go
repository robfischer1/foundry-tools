package publishlane

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/buildlane"
)

// Source is what package selection reads of a tree: a file's body and the files
// matching a glob, both relative to the root. The executor's atoms read the
// checkout on disk (DiskSource).
type Source interface {
	File(rel string) (string, error)
	Glob(pattern string) ([]string, error)
}

// DiskSource is a Source over a checkout.
func DiskSource(root string) Source { return disk(root) }

type disk string

func (d disk) File(rel string) (string, error) {
	b, err := os.ReadFile(filepath.Join(string(d), rel))
	return string(b), err
}

func (d disk) Glob(pattern string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(string(d), pattern))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		rel, _ := filepath.Rel(string(d), m) // a match is under the root by construction
		out = append(out, filepath.ToSlash(rel))
	}
	return out, nil
}

// Package is one package the npm half publishes: where it sits in the tree
// ("" for the root) and what its manifest says.
type Package struct {
	Dir string
	Npm
}

// What is name@version.
func (p Package) What() string { return p.Name + "@" + p.Version }

// RootPackDir is the directory under npm/ that holds the root package's tarball.
// A workspace member's is its own path, which never starts with an underscore
// segment named like this.
const RootPackDir = "_root"

// PackDir is where the pack step stages this package's tarball under out (the
// release directory): out/npm/<dir>, one tarball in it. The publish capability
// reads the same path (daedalus capability/publishnpm.go).
func (p Package) PackDir(out string) string {
	if p.Dir == "" {
		return filepath.Join(out, "npm", RootPackDir)
	}
	return filepath.Join(out, "npm", p.Dir)
}

// Packages answers the packages a tree publishes, with the lane's own words when
// it cannot: a plain root is its own package and must be publishable; a
// workspace root publishes its declared members (non-private, with
// publishConfig.registry) and never itself. The code is a buildlane class.
// Dagger's publish lane makes the same selection (publish_npm.go npmPackages),
// and its sentences are copied here.
func Packages(src Source) ([]Package, int, string) {
	root, code, why := manifest(src, "")
	if code != buildlane.Clean {
		return nil, code, why
	}
	if len(root.Workspaces) == 0 {
		if root.Private {
			return nil, buildlane.Findings, fmt.Sprintf("findings: the record says this repo ships a package, and its root package.json (%s) is private", npmName(root))
		}
		if root.Name == "" || root.Version == "" {
			return nil, buildlane.Findings, "findings: the root package.json names no package or no version — there is nothing to publish"
		}
		return []Package{{Npm: root}}, buildlane.Clean, ""
	}
	seen := map[string]bool{}
	var pkgs []Package
	for _, glob := range root.Workspaces {
		matches, err := src.Glob(path.Join(glob, "package.json"))
		if err != nil {
			return nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: the workspace %s could not be listed: %v", glob, err)
		}
		for _, match := range matches {
			dir := path.Dir(match)
			if seen[dir] {
				continue
			}
			seen[dir] = true
			pkg, code, why := manifest(src, dir)
			if code != buildlane.Clean {
				return nil, code, why
			}
			if pkg.Private || pkg.Registry == "" {
				continue
			}
			if pkg.Name == "" || pkg.Version == "" {
				return nil, buildlane.Findings, fmt.Sprintf("findings: %s declares a registry and names no package or no version", match)
			}
			pkgs = append(pkgs, Package{Dir: dir, Npm: pkg})
		}
	}
	if len(pkgs) == 0 {
		return nil, buildlane.Findings, fmt.Sprintf("findings: %s is a workspace root, and none of its members declares where it publishes — a non-private package.json with publishConfig.registry is the declaration", npmName(root))
	}
	slices.SortFunc(pkgs, func(a, b Package) int { return strings.Compare(a.Dir, b.Dir) })
	return pkgs, buildlane.Clean, ""
}

func manifest(src Source, dir string) (Npm, int, string) {
	file := path.Join(dir, "package.json")
	body, err := src.File(file)
	if err != nil {
		return Npm{}, buildlane.CouldNotRun, fmt.Sprintf("could not run: %s could not be read: %v", file, err)
	}
	pkg, err := NpmOf(body)
	if err != nil {
		return Npm{}, buildlane.Findings, fmt.Sprintf("findings: %s %v", file, err)
	}
	return pkg, buildlane.Clean, ""
}

// npmName is a manifest's name for a sentence: "the root package" when it has none.
func npmName(n Npm) string {
	if n.Name == "" {
		return "the root package"
	}
	return n.Name
}
