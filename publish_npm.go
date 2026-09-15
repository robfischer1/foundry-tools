package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/publishlane"
)

// THE NPM HALF. A record that says `package` dispatches this lane on every
// landing, and not every package is a wheel. MEASURED 2026-09-15:
// stellar-core-ts's record produces [package], its root is @forge/stellar-core-ts,
// and the uv-only lane settled its landing 7a2e51b red at `uv build` ("/src does
// not appear to be a Python project"). The tree decides which half runs: a
// pyproject.toml or setup.py is Python, a root package.json without either is
// npm, and a tree with neither cannot honour the record — a finding.
//
// A WORKSPACE PUBLISHES ITS DECLARED MEMBERS. theia's root is private and names
// apps/* and packages/*; six members declare publishConfig.registry, and Rob
// decided on 2026-09-15 that all six publish. So the declaration is the rule: a
// non-private member with publishConfig.registry publishes, and nothing else in
// the workspace does.
//
// The contract is theia ci/publish.sh's, and each caveat is its measurement.
// The packument is the probe (the per-version document 404s for a version that
// exists). The auth is Basic `_auth`, because Nexus's npm bearer realm is off.
// And an npm registry has no --check-url: `bun publish` over a released version
// fails 409, so a probe that cannot answer is could-not-run, never a publish on
// hope. One package that will not publish does not strand the others, as the
// themes workflow it replaced did not.

// npmPackage is one package the npm half may publish: where it sits in the tree
// ("" for the root) and what its manifest says.
type npmPackage struct {
	dir string
	publishlane.Npm
}

func (p npmPackage) what() string { return p.Name + "@" + p.Version }

func whats(pkgs []npmPackage) string {
	names := make([]string, len(pkgs))
	for i, p := range pkgs {
		names[i] = p.what()
	}
	return strings.Join(names, ", ")
}

// npmTree answers whether this tree takes the npm half, and that half's verdict
// when it does.
func (l *publishLane) npmTree(ctx context.Context) (code int, reason string, npm bool) {
	entries, err := l.m.Source.Entries(ctx)
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: the tree's root could not be listed: %v", err), true
	}
	has := map[string]bool{}
	for _, e := range entries {
		has[e] = true
	}
	if has["pyproject.toml"] || has["setup.py"] {
		return buildlane.Clean, "", false
	}
	if has["package.json"] {
		code, reason := l.runNpm(ctx, has[".npmrc"], has["turbo.json"])
		return code, reason, true
	}
	return buildlane.Findings, "findings: the record says this repo ships a package, and its root carries neither a pyproject.toml nor a package.json — nothing here can be published", true
}

// runNpm publishes every package the tree declares that the registry does not
// already hold.
func (l *publishLane) runNpm(ctx context.Context, hasNpmrc, turbo bool) (int, string) {
	m := l.m
	key, err := publishlane.AuthKey(l.npmRegistry)
	if err != nil {
		return buildlane.CouldNotRun, "could not run: --npm-registry: " + err.Error()
	}
	pkgs, code, reason := l.npmPackages(ctx)
	if code != buildlane.Clean {
		return code, reason
	}
	// THE CREDENTIAL GOES TO ONE PLACE. A tree that names another registry would
	// otherwise be handed the publisher's password for it.
	for _, p := range pkgs {
		if p.Registry != "" && !publishlane.SameRegistry(p.Registry, l.npmRegistry) {
			return buildlane.Findings, fmt.Sprintf("findings: %s's publishConfig.registry is %s, and this lane publishes only to %s", p.Name, p.Registry, l.npmRegistry)
		}
	}
	if !l.dryRun && l.npmToken == nil {
		return buildlane.CouldNotRun, "a publish uploads: --npm-token is required for a package.json tree (--dry-run publishes nothing and needs none)"
	}
	mode := ""
	if l.dryRun {
		mode = " — dry run, nothing is published"
	}
	publishSay("%s at %.12s: %s%s", starOf(m.Repo), m.Sha, whats(pkgs), mode)

	ts := newRun(m.Source, m.Repo, "").lane(checks.ImageTS)
	var absent, released []npmPackage
	for _, p := range pkgs {
		isReleased, code, reason := l.npmReleased(ctx, ts, p.Npm)
		if code != buildlane.Clean {
			return code, reason
		}
		if isReleased {
			released = append(released, p)
		} else {
			absent = append(absent, p)
		}
	}
	if len(absent) == 0 {
		verb := "is"
		if len(released) > 1 {
			verb = "are"
		}
		return buildlane.Clean, fmt.Sprintf("clean: nothing to do — %s %s already on %s; a released version is immutable, bump it to publish anew", whats(released), verb, l.npmRegistry)
	}
	if l.dryRun {
		return buildlane.Clean, fmt.Sprintf("clean: %s — would publish to %s as %s", whats(absent), l.npmRegistry, l.user)
	}

	installed := ts
	for _, p := range absent {
		if !p.Builds() {
			continue
		}
		// One install for the whole tree: a workspace's lockfile is the root's.
		installed = ts.WithExec([]string{"bun", "install", "--frozen-lockfile"}, anyExit)
		out, code, err := outputBoth(ctx, installed)
		if err != nil {
			return buildlane.CouldNotRun, fmt.Sprintf("could not run: bun install never ran: %v", err)
		}
		fmt.Fprintln(os.Stderr, out)
		if code != 0 {
			return buildlane.ToolFailed("bun install --frozen-lockfile", out)
		}
		break
	}
	npmrc, code, reason := l.npmrc(ctx, hasNpmrc, key)
	if code != buildlane.Clean {
		return code, reason
	}

	var published []npmPackage
	var failures []string
	findings := false
	for _, p := range absent {
		code, reason := l.publishOne(ctx, installed, npmrc, p, turbo)
		if code == buildlane.Clean {
			published = append(published, p)
			continue
		}
		failures = append(failures, reason)
		findings = findings || code == buildlane.Findings
	}
	if len(failures) == 0 {
		reason := fmt.Sprintf("clean: %s — published to %s", whats(published), l.npmRegistry)
		if len(released) > 0 {
			reason += "; already released: " + whats(released)
		}
		return buildlane.Clean, reason
	}
	reason = strings.Join(failures, "; ")
	if len(published) > 0 {
		reason += "; published " + whats(published)
	}
	// A package that did not publish because of the tree is a finding; one that
	// did not because the registry or the engine did not answer is re-run.
	if findings {
		return buildlane.Findings, reason
	}
	return buildlane.CouldNotRun, reason
}

// npmPackages answers the packages this tree publishes: a plain root is its own
// package and must be publishable; a workspace root publishes its declared
// members and never itself.
func (l *publishLane) npmPackages(ctx context.Context) ([]npmPackage, int, string) {
	root, code, reason := l.npmManifest(ctx, "")
	if code != buildlane.Clean {
		return nil, code, reason
	}
	if len(root.Workspaces) == 0 {
		if root.Private {
			return nil, buildlane.Findings, fmt.Sprintf("findings: the record says this repo ships a package, and its root package.json (%s) is private", npmName(root))
		}
		if root.Name == "" || root.Version == "" {
			return nil, buildlane.Findings, "findings: the root package.json names no package or no version — there is nothing to publish"
		}
		return []npmPackage{{Npm: root}}, buildlane.Clean, ""
	}
	seen := map[string]bool{}
	var pkgs []npmPackage
	for _, glob := range root.Workspaces {
		matches, err := l.m.Source.Glob(ctx, path.Join(glob, "package.json"))
		if err != nil {
			return nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: the workspace %s could not be listed: %v", glob, err)
		}
		for _, match := range matches {
			dir := path.Dir(match)
			if seen[dir] {
				continue
			}
			seen[dir] = true
			pkg, code, reason := l.npmManifest(ctx, dir)
			if code != buildlane.Clean {
				return nil, code, reason
			}
			if pkg.Private || pkg.Registry == "" {
				continue
			}
			if pkg.Name == "" || pkg.Version == "" {
				return nil, buildlane.Findings, fmt.Sprintf("findings: %s declares a registry and names no package or no version", match)
			}
			pkgs = append(pkgs, npmPackage{dir: dir, Npm: pkg})
		}
	}
	if len(pkgs) == 0 {
		return nil, buildlane.Findings, fmt.Sprintf("findings: %s is a workspace root, and none of its members declares where it publishes — a non-private package.json with publishConfig.registry is the declaration", npmName(root))
	}
	slices.SortFunc(pkgs, func(a, b npmPackage) int { return strings.Compare(a.dir, b.dir) })
	return pkgs, buildlane.Clean, ""
}

// npmManifest reads the package.json in dir ("" for the root).
func (l *publishLane) npmManifest(ctx context.Context, dir string) (publishlane.Npm, int, string) {
	file := path.Join(dir, "package.json")
	manifest, err := l.m.Source.File(file).Contents(ctx)
	if err != nil {
		return publishlane.Npm{}, buildlane.CouldNotRun, fmt.Sprintf("could not run: %s could not be read: %v", file, err)
	}
	pkg, err := publishlane.NpmOf(manifest)
	if err != nil {
		return publishlane.Npm{}, buildlane.Findings, fmt.Sprintf("findings: %s %v", file, err)
	}
	return pkg, buildlane.Clean, ""
}

// publishOne publishes one package, building it first when its prepublishOnly
// asks for nothing but its own build. That build runs HERE — through turbo when
// the workspace has it, so its workspace dependencies build first (theia's
// chrome compiles against aglaia's built types) — and the publish then skips
// lifecycle scripts, because the script it would have run names a package
// manager the lane need not carry (aglaia's says pnpm). Any other prepublishOnly
// is the package's own to run, so bun runs it.
func (l *publishLane) publishOne(ctx context.Context, installed *dagger.Container, npmrc *dagger.Secret, p npmPackage, turbo bool) (int, string) {
	workdir := path.Join("/src", p.dir)
	ready := installed
	args := []string{"bun", "publish", "--registry", l.npmRegistry}
	if publishlane.PlainBuild(p.PrepublishOnly) {
		if turbo {
			ready = installed.WithExec([]string{"bunx", "turbo", "run", "build", "--filter=" + p.Name}, anyExit)
		} else {
			ready = installed.WithWorkdir(workdir).WithExec([]string{"bun", "run", "build"}, anyExit)
		}
		out, code, err := outputBoth(ctx, ready)
		if err != nil {
			return buildlane.CouldNotRun, fmt.Sprintf("could not run: the build of %s never ran: %v", p.what(), err)
		}
		fmt.Fprintln(os.Stderr, out)
		if code != 0 {
			return buildlane.ToolFailed("the build of "+p.what(), out)
		}
		args = append(args, "--ignore-scripts")
	}
	up := ready.
		WithMountedSecret("/src/.npmrc", npmrc).
		WithWorkdir(workdir).
		WithEnvVariable("PUBLISH_UPLOADED_AT", l.stamp).
		WithExec(args, anyExit)
	out, code, err := outputBoth(ctx, up)
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: bun publish never ran for %s: %v", p.what(), err)
	}
	fmt.Fprintln(os.Stderr, out)
	if code != 0 {
		return buildlane.ToolFailed("bun publish "+p.what(), out)
	}
	publishSay("published %s", p.what())
	return buildlane.Clean, ""
}

// npmReleased asks the registry's packument whether this version is released. A
// 404 is an answer: nothing of that name is published. Anything that is neither
// that nor a readable 200 leaves the answer unknown, and unknown is
// could-not-run.
func (l *publishLane) npmReleased(ctx context.Context, ts *dagger.Container, pkg publishlane.Npm) (bool, int, string) {
	url := publishlane.PackumentURL(l.npmRegistry, pkg.Name)
	unknown := fmt.Sprintf("whether %s@%s is released is unknown, and this lane does not guess", pkg.Name, pkg.Version)
	out, code, err := output(ctx, ts.
		WithEnvVariable("PUBLISH_PROBED_AT", l.stamp).
		WithExec([]string{"curl", "-sL", "--retry", "2", "--retry-delay", "2", "--max-time", "60", "-w", "\n%{http_code}", url}, anyExit))
	if err != nil {
		return false, buildlane.CouldNotRun, fmt.Sprintf("could not run: the registry probe never ran: %v", err)
	}
	if code != 0 {
		return false, buildlane.CouldNotRun, fmt.Sprintf("could not run: could not read %s (curl exit %d) — %s", url, code, unknown)
	}
	status, page, err := publishlane.Probe(out)
	if err != nil {
		return false, buildlane.CouldNotRun, fmt.Sprintf("could not run: %v — %s", err, unknown)
	}
	if status == 404 {
		publishSay("%s answers 404: nothing of that name is published", url)
		return false, buildlane.Clean, ""
	}
	if status != 200 {
		return false, buildlane.CouldNotRun, fmt.Sprintf("could not run: %s answered HTTP %d — %s", url, status, unknown)
	}
	released, err := publishlane.NpmReleased(page, pkg.Version)
	if err != nil {
		return false, buildlane.CouldNotRun, fmt.Sprintf("could not run: %v — %s", err, unknown)
	}
	return released, buildlane.Clean, ""
}

// npmrc is the tree's own .npmrc with the publisher's auth line appended under
// key, as a secret mounted over the tree's file: the repository's scope mappings
// survive, and the credential never lands in the tree, an exec's argv or its
// env. It sits at the workspace root, where theia's recipe wrote it and a
// member's publish found it.
func (l *publishLane) npmrc(ctx context.Context, hasNpmrc bool, key string) (*dagger.Secret, int, string) {
	password, err := l.npmToken.Plaintext(ctx)
	if err != nil {
		return nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: the npm token did not read: %v", err)
	}
	own := ""
	if hasNpmrc {
		own, err = l.m.Source.File(".npmrc").Contents(ctx)
		if err != nil {
			return nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: the tree's .npmrc could not be read: %v", err)
		}
	}
	line := publishlane.AuthLine(key, l.user, strings.TrimSpace(password))
	return dag.SetSecret("publish-npmrc-"+l.stamp, strings.TrimRight(own, "\n")+"\n"+line+"\n"), buildlane.Clean, ""
}

// npmName is the package a refusal names, or what stands in for a nameless one.
func npmName(pkg publishlane.Npm) string {
	if pkg.Name == "" {
		return "the root package"
	}
	return pkg.Name
}
