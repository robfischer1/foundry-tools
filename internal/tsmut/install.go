package tsmut

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// install installs the tree against its frozen lockfile, patches the vitest
// runners it brought and runs each planned package.
func (g *grading) install(ctx context.Context, plan []checks.StrykerPackage, owned map[string]string) checks.Verdict {
	if v, ok := g.setup(ctx); !ok {
		return v
	}
	return g.runPackages(ctx, plan, owned)
}

// setup installs the tree once a run: the coverage of a change's tests and the
// Stryker runs share it. A verdict with false is the run's end.
func (g *grading) setup(ctx context.Context) (checks.Verdict, bool) {
	if g.installed {
		return checks.Verdict{}, true
	}
	// bun writes node_modules into the tree it installs in; on the executor the
	// tree is locked, so it installs in a copy (atoms.WorkingTree).
	tree, _, err := atoms.WorkingTree(g.in.Root, work())
	if err != nil {
		return g.settle(2, "CANNOT RUN - the tree could not be copied to install in: "+err.Error()), false
	}
	g.tree = tree
	installed := g.do(ctx, g.tree, "bun", "install", "--frozen-lockfile")
	if !atoms.ToolRan(installed.code) {
		return g.neverRan(installed), false
	}
	if installed.code != 0 {
		return g.settle(checks.DiagnoseInstall(installed.out, installed.code, g.registry(ctx))), false
	}
	patch := g.patchVitestRunners()
	g.runner = "\n" + patch.Note
	if patch.Blocked != "" {
		return g.settle(2, "CANNOT RUN - "+patch.Blocked), false
	}
	g.installed = true
	return checks.Verdict{}, true
}

// registry answers checks.DiagnoseInstall's questions with curl, from the lane the
// install failed in — the registry the lockfile names is the one that lane
// reaches.
func (g *grading) registry(ctx context.Context) registry {
	return registry{ctx: ctx, g: g}
}

type registry struct {
	ctx context.Context
	g   *grading
}

func (r registry) Versions(reg, name string) []string {
	s := r.g.do(r.ctx, r.g.tree, "curl", "-fsSL", reg+"/"+name)
	if s.code != 0 {
		return nil
	}
	versions, _ := checks.RegistryVersions([]byte(s.out))
	return versions
}

func (r registry) Serves(url string) bool {
	return r.g.do(r.ctx, r.g.tree, "curl", "-fsSI", "-o", "/dev/null", url).code == 0
}

// patchVitestRunners applies checks.PatchVitestRunner to every installed copy of
// @stryker-mutator/vitest-runner (stryker-js#6210). The walk does not follow
// symlinks, so a copy bun links into a package is patched once, at its real path,
// and the patch is a new file, never a write through bun's cache hardlinks (the
// old file is removed first, so the new one is a new inode).
//
// THE ROOT IS NAMED, not inherited from the working directory: the retired bash
// body searched $MUT_INSTALL_ROOT, which defaulted to the PACKAGE, and found
// nothing in a workspace that hoists. Naming the root spells the claim the search
// is making, and checks.ReportVitestRunners repeats it back.
//
// AND THE PATCH IS READ BACK, from the place node will load it: whether the bytes
// arrive is not something this lane should take on faith (gijmo-ui#28).
func (g *grading) patchVitestRunners() checks.VitestRunnerReport {
	root := g.tree
	var found []string
	err := walk(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && strings.HasSuffix(filepath.ToSlash(p), "/node_modules/@stryker-mutator/vitest-runner/package.json") {
			found = append(found, p)
		}
		return nil
	})
	if err != nil {
		// A search that fails patches nothing, and now says so: the survivors it
		// produces are a fact about this search, not about the pull's tests.
		return checks.VitestRunnerReport{
			Note: "@stryker-mutator/vitest-runner: the search of " + g.shown(root) + " failed",
			Blocked: "find could not enumerate " + g.shown(root) +
				" for installed copies of @stryker-mutator/vitest-runner, so no copy was inspected and none patched:\n" + err.Error(),
		}
	}
	read := func(p string) string {
		b, _ := readFile(p)
		return string(b)
	}
	var copies []checks.VitestRunnerCopy
	for _, pkg := range found {
		dir := filepath.Dir(pkg)
		files := make([]string, len(checks.VitestRunnerFiles))
		for i, f := range checks.VitestRunnerFiles {
			files[i] = read(filepath.Join(dir, f))
		}
		c := checks.VitestRunnerCopy{
			Dir:    g.shown(dir),
			Runner: checks.PackageVersion(read(pkg)),
			Vitest: checks.PackageVersion(read(filepath.Join(dir, "../../vitest/package.json"))),
		}
		for i, src := range checks.PatchVitestRunner(c.Runner, c.Vitest, files) {
			target := filepath.Join(dir, checks.VitestRunnerFiles[i])
			// A write that fails is a patch that did not land: it counts as
			// attempted and is not verified.
			c.Patched++
			if removeFile(target) == nil && writeFile(target, []byte(src), 0o644) == nil && strings.Contains(read(target), checks.VitestRunnerNewJoin) {
				c.Verified++
			}
		}
		copies = append(copies, c)
	}
	return checks.ReportVitestRunners(g.shown(root), copies)
}

// shown is p as the tree's own path: the verdict's words must not depend on where
// the copy sits (/src on the Dagger runner, a directory of /work on the executor),
// so a path under the working copy is said under the tree's root.
func (g *grading) shown(p string) string {
	rel, err := filepath.Rel(g.tree, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return p
	}
	return filepath.Join(g.in.Root, rel)
}
