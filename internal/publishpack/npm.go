package publishpack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/publishlane"
	"dagger/foundry-tools/internal/releasekit"
)

// TS is ts:pack, the npm half of the publish lane's build: every package the
// tree publishes (publishlane.Packages, the Dagger lane's selection) is built as
// the lane builds it and packed to <release>/npm/<dir>/ as a tarball, which the
// publish capability uploads as it finds it. THE REGISTRY PROBE STAYS IN THE
// CAPABILITY (it needs the network), so every selected package is packed and
// only the absent ones are uploaded; the Dagger lane builds only the absent.
// The cost is a build per released package per landing.
//
// The lane's steps, per package: one `bun install --frozen-lockfile` for the
// tree when any package runs a prepublishOnly; a prepublishOnly that is only
// the package's own build runs as `bunx turbo run build --filter=<name>` (turbo
// trees, so workspace dependencies build first) or `bun run build` in its dir,
// and its tarball is packed with --ignore-scripts, as the lane published it;
// any other prepublishOnly is the package's own, which bun ran inside
// `bun publish` and runs here as `bun run prepublishOnly` before packing; a
// package with none is packed with bun's lifecycle as publish ran it.
//
// `bun pm pack` and `bun publish` build the tarball with the same packer
// (bun's pack command is publish's first step), so the tarball is the one
// Dagger's publish uploaded. That is read from bun's source, not measured.
//
// A package that will not build or pack does not strand the others, as the lane
// does not.
func TS(ctx context.Context, in atoms.Input, a checks.AtomDef) checks.Verdict {
	// The executor builds at /src, which it gives this container as a tree of its
	// own (releasekit.SrcEnv, DEF-B6-013): a bun bundle embeds its build directory.
	in.Root = releasekit.SrcRoot(in.Root)
	entries, err := atoms.RootEntries(in.Root)
	if err != nil {
		return cannot(a, fmt.Sprintf("could not run: the tree's root could not be listed: %v", err))
	}
	has := map[string]bool{}
	for _, e := range entries {
		has[e] = true
	}
	switch {
	case pythonRoot(entries):
		return absent(a, "the tree is a Python project, so python:dist is its build")
	case !has["package.json"]:
		return absent(a, "the tree has no package.json, so it publishes no npm package")
	}
	pkgs, code, why := publishlane.Packages(publishlane.DiskSource(in.Root))
	if code != buildlane.Clean {
		return checks.VerdictOf(a, code, why)
	}
	out := releasekit.OutDir()
	_ = os.RemoveAll(filepath.Join(out, "npm"))
	if bad := mkdir(a, filepath.Join(out, "npm")); bad != nil {
		return *bad
	}
	for _, p := range pkgs {
		if p.Builds() {
			// One install for the whole tree: a workspace's lockfile is the root's.
			o, c := atoms.RunCmd(ctx, in, atoms.Cmd{Dir: in.Root, Name: "bun", Args: []string{"install", "--frozen-lockfile"}, Both: true})
			if _, cls, why := ran("bun install --frozen-lockfile", "bun install", o, c); cls != buildlane.Clean {
				return checks.VerdictOf(a, cls, why)
			}
			break
		}
	}
	var packed, failures, logs []string
	findings := false
	for _, p := range pkgs {
		cls, why := pack(ctx, in, p, out, has["turbo.json"])
		if cls == buildlane.Clean {
			packed = append(packed, p.What())
			continue
		}
		logs = append(logs, why)
		failures = append(failures, strings.SplitN(why, "\n", 2)[0])
		findings = findings || cls == buildlane.Findings
	}
	if len(failures) == 0 {
		return checks.VerdictOf(a, 0, fmt.Sprintf("publish build: packed %s into %s", list(packed), filepath.Join(out, "npm")))
	}
	reason := strings.Join(failures, "; ") + "\n" + strings.Join(logs, "\n")
	if findings {
		return checks.VerdictOf(a, buildlane.Findings, reason)
	}
	return checks.VerdictOf(a, buildlane.CouldNotRun, reason)
}

// pack builds and packs one package: the class, and the reason when it is not
// clean (the reason's first line is the sentence, the rest the tool's words).
func pack(ctx context.Context, in atoms.Input, p publishlane.Package, out string, turbo bool) (int, string) {
	dir := filepath.Join(in.Root, p.Dir)
	run := func(step, what, cwd, name string, args ...string) (int, string) {
		o, c := atoms.RunCmd(ctx, in, atoms.Cmd{Dir: cwd, Name: name, Args: args, Both: true})
		_, cls, why := ran(step, what, o, c)
		return cls, why
	}
	args := []string{"pm", "pack"}
	switch {
	case publishlane.PlainBuild(p.PrepublishOnly):
		build := func() (int, string) {
			if turbo {
				return run("the build of "+p.What(), "the build of "+p.What(), in.Root, "bunx", "turbo", "run", "build", "--filter="+p.Name)
			}
			return run("the build of "+p.What(), "the build of "+p.What(), dir, "bun", "run", "build")
		}
		if cls, why := build(); cls != buildlane.Clean {
			return cls, why
		}
		args = append(args, "--ignore-scripts")
	case p.Builds():
		if cls, why := run("the prepublishOnly of "+p.What(), "the prepublishOnly of "+p.What(), dir, "bun", "run", "prepublishOnly"); cls != buildlane.Clean {
			return cls, why
		}
	}
	dest := p.PackDir(out)
	args = append(args, "--destination", dest)
	if cls, why := run("bun pm pack "+p.What(), "bun pm pack for "+p.What(), dir, "bun", args...); cls != buildlane.Clean {
		return cls, why
	}
	if tgz, _ := filepath.Glob(filepath.Join(dest, "*.tgz")); len(tgz) != 1 {
		return buildlane.Findings, fmt.Sprintf("findings: bun pm pack wrote no tarball for %s in %s", p.What(), dest)
	}
	return buildlane.Clean, ""
}
