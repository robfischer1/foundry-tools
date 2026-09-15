package main

import (
	"context"
	"fmt"
	"os"
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
// The contract is theia ci/publish.sh's, and each caveat is its measurement.
// The packument is the probe (the per-version document 404s for a version that
// exists). The auth is Basic `_auth`, because Nexus's npm bearer realm is off.
// And an npm registry has no --check-url: `bun publish` over a released version
// fails 409, so a probe that cannot answer is could-not-run, never a publish on
// hope.

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
		code, reason := l.runNpm(ctx, has[".npmrc"])
		return code, reason, true
	}
	return buildlane.Findings, "findings: the record says this repo ships a package, and its root carries neither a pyproject.toml nor a package.json — nothing here can be published", true
}

// runNpm publishes the root package unless the registry already holds its
// version.
func (l *publishLane) runNpm(ctx context.Context, hasNpmrc bool) (int, string) {
	m := l.m
	key, err := publishlane.AuthKey(l.npmRegistry)
	if err != nil {
		return buildlane.CouldNotRun, "could not run: --npm-registry: " + err.Error()
	}
	manifest, err := m.Source.File("package.json").Contents(ctx)
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: package.json could not be read: %v", err)
	}
	pkg, err := publishlane.NpmOf(manifest)
	if err != nil {
		return buildlane.Findings, "findings: " + err.Error()
	}
	// WHICH PACKAGES A WORKSPACE RELEASES IS THE REPOSITORY'S DECISION. theia
	// declares a registry on six public packages and publishes four; publishing
	// every public one would invent two releases.
	if pkg.Workspaces {
		return buildlane.Findings, fmt.Sprintf("findings: %s is a workspace root — this lane publishes a single root package, and which workspace packages release is the repository's decision", npmName(pkg))
	}
	if pkg.Private {
		return buildlane.Findings, fmt.Sprintf("findings: the record says this repo ships a package, and its root package.json (%s) is private", npmName(pkg))
	}
	if pkg.Name == "" || pkg.Version == "" {
		return buildlane.Findings, "findings: the root package.json names no package or no version — there is nothing to publish"
	}
	// THE CREDENTIAL GOES TO ONE PLACE. A tree that names another registry would
	// otherwise be handed the publisher's password for it.
	if pkg.Registry != "" && !publishlane.SameRegistry(pkg.Registry, l.npmRegistry) {
		return buildlane.Findings, fmt.Sprintf("findings: %s's publishConfig.registry is %s, and this lane publishes only to %s", pkg.Name, pkg.Registry, l.npmRegistry)
	}
	if !l.dryRun && l.npmToken == nil {
		return buildlane.CouldNotRun, "a publish uploads: --npm-token is required for a package.json tree (--dry-run publishes nothing and needs none)"
	}
	what := pkg.Name + "@" + pkg.Version
	mode := ""
	if l.dryRun {
		mode = " — dry run, nothing is published"
	}
	publishSay("%s at %.12s: %s%s", starOf(m.Repo), m.Sha, what, mode)

	ts := newRun(m.Source, m.Repo, "").lane(checks.ImageTS)
	released, code, reason := l.npmReleased(ctx, ts, pkg)
	if code != buildlane.Clean {
		return code, reason
	}
	if released {
		return buildlane.Clean, fmt.Sprintf("clean: nothing to do — %s is already on %s; a released version is immutable, bump it to publish anew", what, l.npmRegistry)
	}
	if l.dryRun {
		return buildlane.Clean, fmt.Sprintf("clean: %s — would publish to %s as %s", what, l.npmRegistry, l.user)
	}

	built := ts
	if pkg.Builds {
		// prepublishOnly runs under `bun publish`, and it needs the lockfile's
		// dependencies (stellar-core-ts's is tsc).
		built = ts.WithExec([]string{"bun", "install", "--frozen-lockfile"}, anyExit)
		out, code, err := outputBoth(ctx, built)
		if err != nil {
			return buildlane.CouldNotRun, fmt.Sprintf("could not run: bun install never ran: %v", err)
		}
		fmt.Fprintln(os.Stderr, out)
		if code != 0 {
			return buildlane.ToolFailed("bun install --frozen-lockfile", out)
		}
	}
	npmrc, code, reason := l.npmrc(ctx, hasNpmrc, key)
	if code != buildlane.Clean {
		return code, reason
	}
	up := built.
		WithMountedSecret("/src/.npmrc", npmrc).
		WithEnvVariable("PUBLISH_UPLOADED_AT", l.stamp).
		WithExec([]string{"bun", "publish", "--registry", l.npmRegistry}, anyExit)
	out, code, err := outputBoth(ctx, up)
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: bun publish never ran for %s: %v", what, err)
	}
	fmt.Fprintln(os.Stderr, out)
	if code != 0 {
		return buildlane.ToolFailed("bun publish "+what, out)
	}
	publishSay("published %s", what)
	return buildlane.Clean, fmt.Sprintf("clean: %s — published to %s", what, l.npmRegistry)
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
// env.
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
