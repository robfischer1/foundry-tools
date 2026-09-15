package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/publishlane"
)

// THE PUBLISH LANE, AS ONE FUNCTION. The door's publish Job runs `dagger call
// … publish` as its only process. What it replaces: infra's ca-recipe
// recipe.py and the publish half of its tools.sh, and stellar_core's
// ci/publish.sh. The decisions live in internal/publishlane; this file is the
// chain.
//
// THE TRIGGER IS THE TREE, NOT A TAG. The door runs this lane on every landing
// of a repo whose record ships a package, so a landing that did not bump the
// version must be a no-op and one that did must publish. The index is asked,
// file by file, whether each artifact is already released; a released file is
// immutable and is skipped, never compared. `uv publish --check-url` alone
// does not deliver that: it skips only IDENTICAL bytes, these builds are not
// reproducible (publish.sh measured three digests for one commit on
// 2026-08-16), and a differing rebuild of a released version errors with a
// hash mismatch — athena 0.14.2.
//
// BUILD FIRST, THEN ASK. publish.sh read the version literal before building,
// to skip the build. Reading it here would mean parsing TOML and following
// each build backend's dynamic-version rules; the built filenames already
// carry the distribution and version the backend resolved, and the engine
// caches the build by tree. What that moves: a landing that breaks the build
// without bumping the version reds here as well as at the gate, where
// publish.sh exited clean before it built.
//
// THE VERDICT IS THE EXIT CODE, through settle(), as the build lane's is: 0
// published or nothing to publish, 1 the build or an upload was refused, 2 the
// lane could not run.
//
// SERIALISATION IS THE DOOR'S. One publish of a package at a time, and never
// an upload aborted in flight: nothing inside a function can hold either.

// Publish builds the python package at the commit the module was constructed
// on and uploads every artifact the index does not already carry.
func (m *FoundryTools) Publish(
	ctx context.Context,
	// The index password: the Nexus publisher's (ca-publish-lane's PYPI_TOKEN).
	// Required unless --dry-run.
	// +optional
	token *dagger.Secret,
	// The upload endpoint.
	// +optional
	// +default="https://nexus.notusmi.com/repository/pypi-hosted/"
	publishURL string,
	// The hosted index's simple root, which the probe asks and uv's --check-url
	// reads. HOSTED, NOT THE GROUP: a group merges pypi.org, where 8 of the 13
	// names the fleet publishes also exist, and would read a public release as
	// ours (infra ca-recipe-env has the measurement).
	// +optional
	// +default="https://nexus.notusmi.com/repository/pypi-hosted/simple/"
	checkURL string,
	// The index login whose password --token is.
	// +optional
	// +default="publisher"
	user string,
	// The index `uv build` resolves the build backend from, as UV_INDEX_URL.
	// Empty leaves the resolution to uv and the project.
	// +optional
	indexURL string,
	// The npm registry password for a tree whose root is a package.json: the
	// Nexus publisher's (ca-publish-lane's NPM_TOKEN). Required unless --dry-run.
	// +optional
	npmToken *dagger.Secret,
	// The npm registry a package.json tree is probed on and published to. A
	// package whose publishConfig.registry names anywhere else is refused.
	// +optional
	// +default="https://nexus.notusmi.com/repository/npm-hosted/"
	npmRegistry string,
	// Build and ask the index for real; upload nothing, and need no --token.
	// +optional
	dryRun bool,
) error {
	l := &publishLane{
		m: m, token: token, npmToken: npmToken,
		publishURL: publishURL, checkURL: checkURL, user: user, indexURL: indexURL, npmRegistry: npmRegistry, dryRun: dryRun,
		stamp: strconv.FormatInt(time.Now().UnixNano(), 10),
	}
	code, reason := l.run(ctx)
	return settle(ctx, code, "publish: "+reason)
}

type publishLane struct {
	m                                                 *FoundryTools
	token, npmToken                                   *dagger.Secret
	publishURL, checkURL, user, indexURL, npmRegistry string
	dryRun                                            bool
	// stamp is this run's, on the probe and on every upload: what the index
	// holds is a fact about now, not about the tree, and an engine that
	// answered either from its cache would answer last run's.
	stamp string
}

// distDir is where uv build writes: outside the mounted tree, so nothing the
// tree carries under dist/ can be mistaken for this build's output.
const distDir = "/dist"

func publishSay(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "publish: "+format+"\n", args...)
}

func (l *publishLane) run(ctx context.Context) (int, string) {
	m := l.m
	if m.Repo == "" || m.Sha == "" {
		return buildlane.CouldNotRun, "the publish lane publishes a commit the engine fetched — construct the module with --repo and --sha"
	}
	if code, reason, npm := l.npmTree(ctx); npm {
		return code, reason
	}
	if !l.dryRun && l.token == nil {
		return buildlane.CouldNotRun, "a publish uploads: --token is required (--dry-run uploads nothing and needs none)"
	}
	mode := ""
	if l.dryRun {
		mode = " — dry run, nothing is uploaded"
	}
	publishSay("%s at %.12s%s", starOf(m.Repo), m.Sha, mode)

	py := newRun(m.Source, m.Repo, "").lane(checks.ImagePython)
	if l.indexURL != "" {
		py = py.WithEnvVariable("UV_INDEX_URL", l.indexURL)
	}
	built := py.WithExec([]string{"uv", "build", "--out-dir", distDir}, anyExit)
	out, code, err := outputBoth(ctx, built)
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: uv build never ran: %v", err)
	}
	fmt.Fprintln(os.Stderr, out)
	if code != 0 {
		return buildlane.ToolFailed("uv build", out)
	}
	files, err := built.Directory(distDir).Entries(ctx)
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: what uv build wrote could not be read: %v", err)
	}
	var artifacts []publishlane.Artifact
	for _, f := range files {
		if a, ok := publishlane.ArtifactOf(f); ok {
			artifacts = append(artifacts, a)
		}
	}
	if len(artifacts) == 0 {
		return buildlane.Findings, fmt.Sprintf("findings: uv build exited clean and wrote no wheel or sdist to %s (it wrote: %s)", distDir, strings.Join(files, ", "))
	}

	pages := map[string]string{}
	var unguarded []string
	uploaded, skipped := 0, 0
	for _, a := range artifacts {
		name := publishlane.IndexName(a.Dist)
		page, asked := pages[name]
		if !asked {
			var answered bool
			page, answered = l.probe(ctx, py, name)
			pages[name] = page
			if !answered {
				unguarded = append(unguarded, name)
			}
		}
		if publishlane.Released(page, a.File) {
			publishSay("%s is already released — skipped; a released version is immutable, bump it to publish anew", a.File)
			skipped++
			continue
		}
		if l.dryRun {
			publishSay("dry run: would upload %s to %s as %s", a.File, l.publishURL, l.user)
			uploaded++
			continue
		}
		up := built.
			WithEnvVariable("UV_PUBLISH_USERNAME", l.user).
			WithSecretVariable("UV_PUBLISH_PASSWORD", l.token).
			WithEnvVariable("PUBLISH_UPLOADED_AT", l.stamp).
			WithExec([]string{"uv", "publish", "--publish-url", l.publishURL, "--check-url", l.checkURL, distDir + "/" + a.File}, anyExit)
		out, code, err := outputBoth(ctx, up)
		if err != nil {
			return buildlane.CouldNotRun, fmt.Sprintf("could not run: uv publish never ran for %s: %v", a.File, err)
		}
		fmt.Fprintln(os.Stderr, out)
		if code != 0 {
			return buildlane.ToolFailed("uv publish "+a.File, out)
		}
		publishSay("uploaded %s", a.File)
		uploaded++
	}

	// Say which happened: "0 uploaded" and "nothing to do" are different facts,
	// and a clean settle that cannot tell them apart hides the second.
	what := artifacts[0].Dist + " " + artifacts[0].Version
	verb := "uploaded"
	if l.dryRun {
		verb = "would upload"
	}
	reason := fmt.Sprintf("clean: %s — %d %s, %d already released", what, uploaded, verb, skipped)
	if uploaded == 0 {
		reason = fmt.Sprintf("clean: nothing to do — %s is already on the index; a released version is immutable, bump it to publish anew", what)
	}
	if len(unguarded) > 0 {
		reason += fmt.Sprintf("; the already-released check did NOT run for %s (the index did not answer the probe), so uv's --check-url was the only guard", strings.Join(unguarded, ", "))
	}
	return buildlane.Clean, reason
}

// probe asks the hosted index for a project's simple page, and answers the
// page and whether the index answered at all. A 404 is an answer — nothing of
// that name is released — so the page is empty and the check ran. Anything
// else that is not a 200 leaves the check unrun. That is not fatal, because
// uv's --check-url still refuses a real collision, but it is never silent.
func (l *publishLane) probe(ctx context.Context, py *dagger.Container, name string) (string, bool) {
	url := strings.TrimSuffix(l.checkURL, "/") + "/" + name + "/"
	out, code, err := output(ctx, py.
		WithEnvVariable("PUBLISH_PROBED_AT", l.stamp).
		WithExec([]string{"curl", "-sL", "--retry", "2", "--retry-delay", "2", "--max-time", "60", "-w", "\n%{http_code}", url}, anyExit))
	if err != nil {
		publishSay("could not ask %s: %v — the already-released check did not run", url, err)
		return "", false
	}
	if code != 0 {
		publishSay("could not read %s (curl exit %d) — the already-released check did not run", url, code)
		return "", false
	}
	status, page, err := publishlane.Probe(out)
	if err != nil {
		publishSay("%v — the already-released check did not run", err)
		return "", false
	}
	if status == 404 {
		publishSay("%s answers 404: nothing of that name is released", url)
		return "", true
	}
	if status != 200 {
		publishSay("%s answered HTTP %d — the already-released check did not run", url, status)
		return "", false
	}
	return page, true
}
