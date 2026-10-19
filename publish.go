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
	// The index password: the fleet's publisher login on devpi
	// (ca-publish-lane's PYPI_TOKEN). Required unless --dry-run.
	// +optional
	token *dagger.Secret,
	// The upload endpoint: devpi's fleet/prod index at packages.notusmi.com
	// (infra flux/infrastructure/packages.yaml), where Nexus's pypi-hosted
	// moved (master-plan "Transparent Cache — Nexus Retired", F7).
	// +optional
	// +default="https://packages.notusmi.com/pypi/fleet/prod/"
	publishURL string,
	// The fleet index's simple root, which the probe asks and uv's --check-url
	// reads. THE FLEET'S INDEX ALONE, NEVER A MERGE WITH pypi.org: 8 of the 13
	// names the fleet publishes also exist there, and a merged view would read
	// a public release as ours (infra ca-recipe-env has the measurement).
	// fleet/prod has no bases, so its simple pages hold only what the fleet
	// published.
	// +optional
	// +default="https://packages.notusmi.com/pypi/fleet/prod/+simple/"
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
	// publisher's (ca-publish-lane's NPM_TOKEN) on Verdaccio. Required unless
	// --dry-run.
	// +optional
	npmToken *dagger.Secret,
	// The npm registry a package.json tree is probed on and published to:
	// Verdaccio at packages.notusmi.com/npm (infra packages.yaml), where
	// Nexus's npm-hosted moved. A package whose publishConfig.registry names
	// anywhere else is refused.
	// +optional
	// +default="https://packages.notusmi.com/npm/"
	npmRegistry string,
	// Build and ask the index for real; upload nothing, and need no --token.
	// +optional
	dryRun bool,
	// The SPIRE agent's workload socket, forwarded by the calling pod. With
	// --publish-svid it is where the index password is fetched; without, it is
	// accepted and unused, so every lane Job can pass the same flags.
	// +optional
	spire *dagger.Socket,
	// Log in to the indexes with a JWT-SVID instead of --token / --npm-token:
	// audience packages-pypi for the upload, packages-npm for the npm publish,
	// fetched through --spire right before each. Needs --spire; without it the
	// static tokens apply exactly as before.
	// +optional
	publishSvid bool,
	// The run's record token (CA_RECORD_TOKEN), which authorises posting this
	// run's record to the door it fetched the tree from. Absent and the lane
	// posts nothing and settles on its exit code, exactly as it did before.
	// A SECRET, NOT A STRING, for the reason cast's own parameter states.
	// +optional
	recordToken *dagger.Secret,
) error {
	l := &publishLane{
		m: m, token: token, npmToken: npmToken, spire: spire, publishSvid: publishSvid,
		publishURL: publishURL, checkURL: checkURL, user: user, indexURL: indexURL, npmRegistry: npmRegistry, dryRun: dryRun,
		stamp:  strconv.FormatInt(time.Now().UnixNano(), 10),
		phases: newPhases(publishGroup, publishPhases("")),
	}
	code, reason := l.run(ctx)
	l.post(ctx, recordToken, code, reason)
	return settle(ctx, code, "publish: "+reason)
}

type publishLane struct {
	m                                                 *FoundryTools
	token, npmToken                                   *dagger.Secret
	spire                                             *dagger.Socket
	publishSvid                                       bool
	publishURL, checkURL, user, indexURL, npmRegistry string
	dryRun                                            bool
	// stamp is this run's, on the probe and on every upload: what the index
	// holds is a fact about now, not about the tree, and an engine that
	// answered either from its cache would answer last run's.
	stamp string
	// phases records what each step answered, for the run record (publish_record.go).
	phases
	// kind is the half the tree took, "python" or "ts"; halted is set when the
	// build step ended the run, so the upload reads as never reached.
	kind   string
	halted bool
	// packSealed is set once the build row is filed; packFailures are the npm
	// builds that failed while their package was being published.
	packSealed   bool
	packFailures []string
	packFindings bool
}

// distDir is where uv build writes: outside the mounted tree, so nothing the
// tree carries under dist/ can be mistaken for this build's output.
const distDir = "/dist"

// The audiences the package indexes' svid-auth plugins validate.
const (
	pypiAudience = "packages-pypi"
	npmAudience  = "packages-npm"
	// svidUser is the Basic username sent with a JWT-SVID password. The SVID's
	// sub (spiffe://...) holds a colon, which splits a Basic credential in the
	// wrong place, and both plugins ignore the username.
	svidUser = "svid"
)

// svidOn says whether the indexes are logged in to with the workload identity:
// the flag AND a socket. Either missing leaves the static tokens as they were.
func (l *publishLane) svidOn() bool { return l.publishSvid && l.spire != nil }

// svid fetches a JWT-SVID for audience now, through the forwarded socket, and
// answers the Basic login: a fixed username and the token as a secret. It is
// called right before the upload or publish it authenticates, never earlier,
// so the 15-minute token is fresh and sits in no cached layer, env var or log.
func (l *publishLane) svid(ctx context.Context, audience, host string) (string, *dagger.Secret, error) {
	name := fmt.Sprintf("publish-svid-%s-%d", audience, time.Now().UnixNano())
	_, secret, err := svidLogin{audience, l.spire}.token(ctx, host, name)
	if err != nil {
		return "", nil, fmt.Errorf("the %s JWT-SVID did not fetch: %w", audience, err)
	}
	return svidUser, secret, nil
}

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
	if !l.dryRun && l.token == nil && !l.svidOn() {
		return buildlane.CouldNotRun, "a publish uploads: --token is required (--dry-run uploads nothing and needs none)"
	}
	mode := ""
	if l.dryRun {
		mode = " — dry run, nothing is uploaded"
	}
	publishSay("%s at %.12s%s", starOf(m.Repo), m.Sha, mode)

	l.begin("python")
	py := newRun(m.Source, m.Repo, "").lane(checks.ImagePython)
	if l.indexURL != "" {
		py = py.WithEnvVariable("UV_INDEX_URL", l.indexURL)
	}
	built := py.WithExec([]string{"uv", "build", "--out-dir", distDir}, anyExit)
	out, code, err := outputBoth(ctx, built)
	if err != nil {
		return l.stopArtifacts(buildlane.CouldNotRun, fmt.Sprintf("could not run: uv build never ran: %v", err))
	}
	fmt.Fprintln(os.Stderr, out)
	if code != 0 {
		return l.stopArtifacts(buildlane.ToolFailed("uv build", out))
	}
	files, err := built.Directory(distDir).Entries(ctx)
	if err != nil {
		return l.stopArtifacts(buildlane.CouldNotRun, fmt.Sprintf("could not run: what uv build wrote could not be read: %v", err))
	}
	var artifacts []publishlane.Artifact
	for _, f := range files {
		if a, ok := publishlane.ArtifactOf(f); ok {
			artifacts = append(artifacts, a)
		}
	}
	if len(artifacts) == 0 {
		return l.stopArtifacts(buildlane.Findings, fmt.Sprintf("findings: uv build exited clean and wrote no wheel or sdist to %s (it wrote: %s)", distDir, strings.Join(files, ", ")))
	}
	l.sealArtifacts(buildlane.Clean, "uv build wrote "+strings.Join(files, ", "))

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
		user, password := l.user, l.token
		if l.svidOn() {
			var err error
			if user, password, err = l.svid(ctx, pypiAudience, l.publishURL); err != nil {
				return buildlane.CouldNotRun, fmt.Sprintf("could not run: %v", err)
			}
		}
		up := built.
			WithEnvVariable("UV_PUBLISH_USERNAME", user).
			WithSecretVariable("UV_PUBLISH_PASSWORD", password).
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
