package releasekit

import (
	"context"
	"fmt"
	"regexp"
	"slices"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
)

// THE PER-BASE RELEASE ROOT (D7). A repository of base images builds each
// bases/<name>/ from a Dockerfile, and a base that ends FROM scratch has no base
// image to take a release from. Such a base carries its own release inputs
// beside its Dockerfile — a package.json (the bun CLIs' pinned install, with a
// bun.lock) whose "release" script leaves the layer's files under its own
// release/ — and its Dockerfile copies them from release/<name>/, where the
// release atom stages them. The root of that release is bases/<name>/, not the
// tree's root.

// BasesDir is where a repository of bases keeps them.
const BasesDir = "bases"

var baseFile = regexp.MustCompile(`^bases/([^/]+)/(Dockerfile|package\.json)$`)

// BaseRoot is a base that carries its own release inputs.
type BaseRoot struct {
	Name       string // the base's directory name, and its directory under release/
	Dockerfile string // its path
}

// Dir is the base's directory in a tree.
func (b BaseRoot) Dir() string { return BasesDir + "/" + b.Name }

// BaseRoots are the bases of the tree with a package.json and a Dockerfile that
// copies from release/, by name. refused names a base whose Dockerfile copies
// from release/ but not from release/<name>/, the directory its release is
// staged in: it would be given nothing.
func BaseRoots(ctx context.Context, t Tree) (roots []BaseRoot, refused []string, err error) {
	files, err := t.Population(ctx)
	if err != nil {
		return nil, nil, err
	}
	have := map[string]map[string]bool{}
	for _, f := range files {
		if m := baseFile.FindStringSubmatch(f); m != nil {
			if have[m[1]] == nil {
				have[m[1]] = map[string]bool{}
			}
			have[m[1]][m[2]] = true
		}
	}
	names := make([]string, 0, len(have))
	for n, h := range have {
		if h["Dockerfile"] && h["package.json"] {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	for _, n := range names {
		root := BaseRoot{Name: n, Dockerfile: BasesDir + "/" + n + "/Dockerfile"}
		body, err := t.File(ctx, root.Dockerfile)
		if err != nil {
			return nil, nil, fmt.Errorf("%s could not be read: %w", root.Dockerfile, err)
		}
		switch {
		case buildlane.CopiesReleaseUnder(body, n):
			roots = append(roots, root)
		case buildlane.CopiesRelease(body):
			refused = append(refused, fmt.Sprintf("%s copies from %s/ but not from %s/%s/, where its release is staged", root.Dockerfile, buildlane.ReleaseDir, buildlane.ReleaseDir, n))
		}
	}
	return roots, refused, nil
}

// THE GO ANALOGUE (D9). A base whose artifact is a Go binary (chairman's
// chairman-svid) carries bases/<name>/release.toml beside its Dockerfile: the
// [[binary]] it compiles and, when the Go module is not the base's own directory,
// `module = "<dir>"` naming the directory under bases/<name>/ that holds go.mod.
// The release compiles in that module and stages the binaries at
// release/<name>/, where the Dockerfile COPYs them from.

// BaseGoRoot is a base with a Go release of its own.
type BaseGoRoot struct {
	Name       string // the base's directory name, and its directory under release/
	Dockerfile string // its path
	Module     string // the Go module's directory in the tree
	Binaries   []checks.ReleaseBinary
	Vendored   bool
}

var baseManifest = regexp.MustCompile(`^bases/([^/]+)/(Dockerfile|release\.toml)$`)

// BaseGoRoots are the bases with a release.toml and a Dockerfile that copies from
// release/<name>/, by name. refused names a base that declared a release and
// cannot be given one: a Dockerfile that copies from release/ but not from
// release/<name>/, a manifest that does not parse, a module with no go.mod.
func BaseGoRoots(ctx context.Context, t Tree) (roots []BaseGoRoot, refused []string, err error) {
	files, err := t.Population(ctx)
	if err != nil {
		return nil, nil, err
	}
	have := map[string]map[string]bool{}
	for _, f := range files {
		if m := baseManifest.FindStringSubmatch(f); m != nil {
			if have[m[1]] == nil {
				have[m[1]] = map[string]bool{}
			}
			have[m[1]][m[2]] = true
		}
	}
	names := make([]string, 0, len(have))
	for n, h := range have {
		if h["Dockerfile"] && h["release.toml"] {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	for _, n := range names {
		dir := BasesDir + "/" + n
		docker := dir + "/Dockerfile"
		body, err := t.File(ctx, docker)
		if err != nil {
			return nil, nil, fmt.Errorf("%s could not be read: %w", docker, err)
		}
		if !buildlane.CopiesReleaseUnder(body, n) {
			if buildlane.CopiesRelease(body) {
				refused = append(refused, fmt.Sprintf("%s copies from %s/ but not from %s/%s/, where its release is staged", docker, buildlane.ReleaseDir, buildlane.ReleaseDir, n))
			}
			continue
		}
		toml, err := t.File(ctx, dir+"/"+ManifestFile)
		if err != nil {
			return nil, nil, fmt.Errorf("%s/%s could not be read: %w", dir, ManifestFile, err)
		}
		module, bins, merr := ReadBaseManifest(toml)
		if merr != nil {
			refused = append(refused, fmt.Sprintf("%s/%s: %v", dir, ManifestFile, merr))
			continue
		}
		modDir := dir
		if module != "." {
			modDir = dir + "/" + module
		}
		if _, ok := t.Present(ctx, modDir+"/go.mod"); !ok {
			refused = append(refused, fmt.Sprintf("%s/%s: no go.mod at %s, which the manifest names as its module", dir, ManifestFile, modDir))
			continue
		}
		_, vendored := t.Present(ctx, modDir+"/vendor/modules.txt")
		roots = append(roots, BaseGoRoot{Name: n, Dockerfile: docker, Module: modDir, Binaries: bins, Vendored: vendored})
	}
	return roots, refused, nil
}
