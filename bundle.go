package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE BUNDLE LANE, AS ONE FUNCTION. The door's bundle Job runs `dagger call …
// bundle` as its only process. What it replaces: foundry-dies' ci/bundle.sh
// and the bundle half of infra's ca-recipe. The decisions live in
// internal/bundlelane and in internal/checks' dies judgements, which the gate
// atoms already use; this file is the chain.
//
// TWO DIES, BUILT AND GATED ON EVERY LANDING:
//
//	foundry.notusmi.com/policy/ouranos-bundle   the admission policy  (policy/)
//	foundry.notusmi.com/data/fleet-bundle       the fleet roster      (fleet/)
//
// Each is built twice: plain, and a signed twin whose .signatures.json OPA
// verifies. The twin is a separate tag because there is no tolerant
// intermediate state: measured 2026-08-28 on OPA's remote service path, a
// signed bundle without a keyid, or an unsigned one with, answers 200 with an
// EMPTY document and every health check green. ouranos polls :signed.
//
// THE PATH FILTERS DECIDE ONLY WHAT IS PUSHED. The retired workflows' paths
// filters are reproduced against the previous tip (bundlelane.Publishes),
// because a die's revision is the fleet's notification primitive; every gate
// runs regardless, because a gate that only runs when the paths line up is not
// a record.
//
// IDEMPOTENT PER COMMIT. The pin g<short> is immutable: a pin that already
// resolves is never re-pushed, and a rebuild whose bytes differ (gzip is not
// deterministic) leaves the gated original standing. The channel is re-pointed
// regardless, which is what makes a retry converge, and cosign signs only a
// digest whose signature does not already verify.
//
// THE VERDICT IS THE EXIT CODE, through settle(): 0 gated and published (or
// nothing to publish), 1 a gate said no — a claim about the tree — and 2 the
// lane could not run.

// Bundle builds, gates, publishes and signs foundry-dies' two dies at the
// commit the module was constructed on.
func (m *FoundryTools) Bundle(
	ctx context.Context,
	// The ES256 key the signed twins are built with: base64 of its PEM
	// (ca-bundle-lane's OPA_BUNDLE_SIGNING_KEY). Required unless --dry-run,
	// which mints a throwaway one so the twins are still built and gated.
	// +optional
	opaSigningKey *dagger.Secret,
	// The token that logs in to foundry.notusmi.com as rob (REGISTRY_TOKEN).
	// Required unless --dry-run.
	// +optional
	registryToken *dagger.Secret,
	// The fleet cosign key the dies are signed with: base64 of its PEM
	// (COSIGN_PRIVATE_KEY). Required unless --dry-run.
	// +optional
	cosignKey *dagger.Secret,
	// The cosign key's passphrase (COSIGN_PASSWORD). Required unless --dry-run.
	// +optional
	cosignPassphrase *dagger.Secret,
	// Build and gate everything for real; push, tag and sign nothing.
	// +optional
	dryRun bool,
) error {
	l := &bundleLane{
		m: m, opaSigningKey: opaSigningKey, registryToken: registryToken,
		cosignKey: cosignKey, cosignPassphrase: cosignPassphrase, dryRun: dryRun,
		stamp: strconv.FormatInt(time.Now().UnixNano(), 10),
	}
	code, reason := l.run(ctx)
	return settle(ctx, code, "bundle: "+reason)
}

type bundleLane struct {
	m                                                         *FoundryTools
	opaSigningKey, registryToken, cosignKey, cosignPassphrase *dagger.Secret
	dryRun                                                    bool
	// stamp is this run's, on every registry read and write: what a registry
	// holds is a fact about now, and a push or a signature is an act.
	stamp string
}

// workDir is where the bundles are built, outside the mounted tree.
const workDir = "/work"

func bundleSay(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "bundle: "+format+"\n", args...)
}

// gateResult is one step's verdict: a gate that could not run is
// could-not-run, a gate that ran and refused is findings — a claim about the
// tree — and a step that passed is clean.
type gateResult struct {
	code   int
	reason string
}

func clean() gateResult              { return gateResult{buildlane.Clean, ""} }
func findings(why string) gateResult { return gateResult{buildlane.Findings, "findings: " + why} }
func couldNotRun(format string, a ...any) gateResult {
	return gateResult{buildlane.CouldNotRun, "could not run: " + fmt.Sprintf(format, a...)}
}

func (l *bundleLane) run(ctx context.Context) (int, string) {
	m := l.m
	if m.Repo == "" || m.Sha == "" {
		return buildlane.CouldNotRun, "the bundle lane publishes a commit the engine fetched — construct the module with --repo and --sha"
	}
	if !l.dryRun && (l.opaSigningKey == nil || l.registryToken == nil || l.cosignKey == nil || l.cosignPassphrase == nil) {
		return buildlane.CouldNotRun, "a bundle publishes and signs: --opa-signing-key, --registry-token, --cosign-key and --cosign-passphrase are all required (--dry-run needs none)"
	}
	pin, err := bundlelane.Pin(m.Sha)
	if err != nil {
		return buildlane.CouldNotRun, err.Error()
	}
	mode := ""
	if l.dryRun {
		mode = " — dry run: every gate and both builds run for real; nothing is pushed, tagged or signed"
	}
	bundleSay("%s at %.12s%s", starOf(m.Repo), m.Sha, mode)

	r := newRun(m.Source, m.Repo, "")
	opa, err := r.opaClient(ctx)
	if err != nil {
		return buildlane.CouldNotRun, "could not run: " + err.Error()
	}
	signingKey, g := l.signingKey(ctx)
	if g.code != buildlane.Clean {
		return g.code, g.reason
	}
	tools := opa.
		WithMountedSecret("/run/opa/sign.key", signingKey).
		WithDirectory(workDir, dag.Directory())

	changed, g := l.changed(ctx, r, tools)
	if g.code != buildlane.Clean {
		return g.code, g.reason
	}
	publishPolicy, publishFleet := bundlelane.Publishes(changed)

	// EVERY GATE RUNS ON EVERY LANDING.
	policy, g := l.policy(ctx, tools)
	if g.code != buildlane.Clean {
		return g.code, g.reason
	}
	fleet, g := l.fleet(ctx, policy)
	if g.code != buildlane.Clean {
		return g.code, g.reason
	}

	if !publishPolicy && !publishFleet {
		return buildlane.Clean, "clean: both dies gated clean and neither has anything to publish for this landing (nothing outside **.md, .forgejo/ and schema/ moved, and nothing under fleet/)"
	}
	if l.dryRun {
		return buildlane.Clean, fmt.Sprintf("clean: both dies gated clean — dry run, nothing pushed, tagged or signed (a landing would publish policy=%v fleet=%v at %s)", publishPolicy, publishFleet, pin)
	}

	var published []string
	if publishPolicy {
		refs, g := l.publishDie(ctx, fleet, bundlelane.PolicyDie, pin, "bundle.tar.gz", "bundle-signed.tar.gz")
		if g.code != buildlane.Clean {
			return g.code, g.reason
		}
		published = append(published, refs...)
	} else {
		bundleSay("policy: nothing outside **.md, .forgejo/ and schema/ changed — :stable stays where it is")
	}
	if publishFleet {
		refs, g := l.publishDie(ctx, fleet, bundlelane.FleetDie, pin, "fleet-bundle.tar.gz", "fleet-bundle-signed.tar.gz")
		if g.code != buildlane.Clean {
			return g.code, g.reason
		}
		published = append(published, refs...)
	} else {
		bundleSay("fleet: nothing under fleet/ (nor policy/.manifest, policy/admission/stubs.rego) changed — the roster's revision keeps meaning 'changed'")
	}
	return buildlane.Clean, "clean: gated both dies; published and signed " + strings.Join(published, ", ")
}

// signingKey answers the ES256 key the twins are built with, as a secret.
func (l *bundleLane) signingKey(ctx context.Context) (*dagger.Secret, gateResult) {
	if l.opaSigningKey == nil {
		key, err := bundlelane.EphemeralKey()
		if err != nil {
			return nil, couldNotRun("no OPA signing key, and an ephemeral one could not be minted: %v", err)
		}
		bundleSay("dry run: minted an EPHEMERAL P-256 signing key; the twins are built and gated for real, their signatures are throwaway")
		return dag.SetSecret("bundle-opa-ephemeral-key-"+l.stamp, key), clean()
	}
	encoded, err := l.opaSigningKey.Plaintext(ctx)
	if err != nil {
		return nil, couldNotRun("the OPA signing key did not read: %v", err)
	}
	key, err := bundlelane.DecodeKey("OPA_BUNDLE_SIGNING_KEY", encoded)
	if err != nil {
		return nil, couldNotRun("%v", err)
	}
	return dag.SetSecret("bundle-opa-signing-key", key), clean()
}

// changed answers the paths this landing touched since the previous tip, or
// nil when the previous tip cannot be read — and cannot-tell publishes both.
// On a merge, HEAD^1 is main before the merge, so the diff is exactly what the
// landing introduced.
func (l *bundleLane) changed(ctx context.Context, r *run, tools *dagger.Container) ([]string, gateResult) {
	git := r.gitReady(ctx, tools)
	_, parent, err := output(ctx, git.WithExec([]string{"git", "rev-parse", "--verify", "-q", l.m.Sha + "^1"}, anyExit))
	if err != nil {
		return nil, couldNotRun("the history could not be read: %v", err)
	}
	if parent != 0 {
		bundleSay("the previous tip could not be read — both dies publish")
		return nil, clean()
	}
	out, code, err := output(ctx, git.WithExec([]string{"git", "diff", "--name-only", l.m.Sha + "^1", l.m.Sha}, anyExit))
	if err != nil || code != 0 {
		return nil, couldNotRun("the change set could not be read (exit %d): %v %s", code, err, out)
	}
	paths := bundlelane.Changed(out)
	bundleSay("this landing touched %d path(s) since %.12s^1", len(paths), l.m.Sha)
	return paths, clean()
}

// exec runs a gate's tool and answers what it printed, its code, and — when
// the engine never ran it — the could-not-run.
func exec(ctx context.Context, ctr *dagger.Container, step string, args ...string) (string, int, gateResult) {
	out, code, err := output(ctx, ctr.WithExec(args, anyExit))
	if err != nil {
		return "", 0, couldNotRun("%s never ran: %v", step, err)
	}
	return out, code, clean()
}

// member reads one file out of a built bundle.
func member(ctx context.Context, ctr *dagger.Container, bundle, file string) (string, gateResult) {
	out, code, g := exec(ctx, ctr, "reading "+file+" out of "+bundle, "tar", "xzOf", workDir+"/"+bundle, file)
	if g.code != buildlane.Clean {
		return "", g
	}
	if code != 0 {
		return "", findings(fmt.Sprintf("%s carries no %s: %s", bundle, file, out))
	}
	return out, clean()
}

// policy re-gates the policy, builds it plain and signed, and gates the
// artifact. It answers the container the bundles were built in.
func (l *bundleLane) policy(ctx context.Context, tools *dagger.Container) (*dagger.Container, gateResult) {
	bundleSay("── policy: re-gate before publishing ──")
	out, code, g := exec(ctx, tools, "opa test", "opa", "test", "policy/", "-v")
	if g.code != buildlane.Clean {
		return nil, g
	}
	if state, why := checks.OpaTestState(code, out); state == buildlane.Findings {
		return nil, findings("opa test policy/ failed — the policy does not pass its own suite. " + why + "\n" + out)
	} else if state == buildlane.CouldNotRun {
		return nil, couldNotRun("opa test policy/: %s", why)
	}
	out, code, g = exec(ctx, tools, "the admission dogfood", "opa", "eval", "-d", "policy/admission", "-i", "tests/fixtures/ouranos-self.json", "--format", "json", "data.admission.deny")
	if g.code != buildlane.Clean {
		return nil, g
	}
	if code != 0 {
		return nil, findings("opa eval over policy/admission for ouranos-self.json did not evaluate:\n" + out)
	}
	denies, err := checks.OpaDenySet(out)
	if err != nil {
		return nil, couldNotRun("the admission dogfood answered a shape this lane cannot read: %v", err)
	}
	if len(denies) != 0 {
		return nil, findings(fmt.Sprintf("ouranos-self.json produces %d admission denial(s) — this repo's own star would not admit: %v", len(denies), denies))
	}
	bundleSay("policy: opa test clean, ouranos-self admits with 0 denials")

	bundleSay("── policy: build the bundle and its SIGNED twin ──")
	built := tools
	for _, b := range [][]string{
		{"opa", "build", "-b", "policy/", "-o", workDir + "/bundle.tar.gz", "--revision", l.m.Sha, "--ignore", "*_test.rego"},
		{"opa", "build", "-b", "policy/", "-o", workDir + "/bundle-signed.tar.gz", "--revision", l.m.Sha, "--ignore", "*_test.rego", "--signing-key", "/run/opa/sign.key", "--signing-alg", "ES256"},
	} {
		built = built.WithExec(b, anyExit)
		out, code, err := output(ctx, built)
		if err != nil {
			return nil, couldNotRun("opa build never ran: %v", err)
		}
		if code != 0 {
			return nil, findings("opa build over policy/ failed:\n" + out)
		}
	}
	if g := twin(ctx, built, "bundle.tar.gz", "bundle-signed.tar.gz", "the signed bundle"); g.code != buildlane.Clean {
		return nil, g
	}

	bundleSay("── policy GATE: the artifact carries its data and still hides what it should ──")
	data, g := member(ctx, built, "bundle.tar.gz", "/data.json")
	if g.code != buildlane.Clean {
		return nil, g
	}
	if _, stars, err := checks.DiesDataKeys(data); err != nil {
		return nil, findings(fmt.Sprintf("the built bundle's data.json is not JSON: %v", err))
	} else if stars == 0 {
		return nil, findings("the built bundle has an empty star_only — publishing it would make every verb visible to every principal")
	}
	canary := checks.DiesCanary(data)
	if canary == "" {
		return nil, findings("chaos has no star_only row in the built bundle — the roster did not survive the build")
	}
	probe := workDir + "/probe-session.json"
	out, code, g = exec(ctx, built.WithNewFile(probe, fmt.Sprintf(`{"principal":{"type":"session","subject":"s"},"verbs":["search",%q]}`, canary)),
		"the session-principal probe", "opa", "eval", "-b", workDir+"/bundle.tar.gz", "-i", probe, "--format", "json", "data.authz.visible.allowed")
	if g.code != buildlane.Clean {
		return nil, g
	}
	if code != 0 {
		return nil, findings("the session-principal probe did not evaluate against the built bundle:\n" + out)
	}
	allowed, err := checks.OpaAllowed(out)
	if err != nil {
		return nil, couldNotRun("the session-principal probe answered a shape this lane cannot read: %v", err)
	}
	if len(allowed) != 1 || allowed[0] != "search" {
		return nil, findings(fmt.Sprintf("curated verb %s is visible to a session principal in the artifact: %v", canary, allowed))
	}
	bundleSay("policy: artifact gate clean (canary %s stays hidden from a session principal)", canary)
	return built, clean()
}

// twin gates a signed twin against its plain bundle: the signature is there,
// and the two carry the same data.json, because only the signature may
// differ — a data.json that drifted means the gates graded one artifact while
// the fleet runs the other.
func twin(ctx context.Context, built *dagger.Container, plain, signed, what string) gateResult {
	listing, code, g := exec(ctx, built, "listing "+signed, "tar", "tzf", workDir+"/"+signed)
	if g.code != buildlane.Clean {
		return g
	}
	if code != 0 || !bundlelane.Signed(listing) {
		return findings(what + " carries no .signatures.json — OPA would refuse it and serve nothing")
	}
	a, g := member(ctx, built, plain, "/data.json")
	if g.code != buildlane.Clean {
		return g
	}
	b, g := member(ctx, built, signed, "/data.json")
	if g.code != buildlane.Clean {
		return g
	}
	if a != b {
		return findings(fmt.Sprintf("%s and %s carry different data.json — the gates would grade the wrong artifact", plain, signed))
	}
	return clean()
}

// fleet gates the two tiers, builds the roster plain and signed from a staged
// tree, and gates the artifact and its composition with the policy. It answers
// the container every bundle was built in.
func (l *bundleLane) fleet(ctx context.Context, built *dagger.Container) (*dagger.Container, gateResult) {
	src := l.m.Source
	bundleSay("── fleet GATE: the two tiers agree before anything is built ──")
	data, ok, err := fileIn(ctx, src, "fleet/data.json")
	if err != nil {
		return nil, couldNotRun("fleet/data.json could not be read: %v", err)
	}
	if !ok {
		return nil, findings("fleet/data.json is absent — the fleet tier (map, topics, wiring) is not in the repo")
	}
	shards, err := src.Glob(ctx, "fleet/stars/*/slag.json")
	if err != nil {
		return nil, couldNotRun("the slag shards could not be listed: %v", err)
	}
	orphans, mapped, err := bundlelane.Orphans(shards, data)
	if err != nil {
		return nil, findings(err.Error())
	}
	if len(orphans) > 0 {
		return nil, findings("slag shards with no fleet/data.json map entry: " + strings.Join(orphans, " "))
	}
	bundleSay("fleet: tier gate — %d shards, all present in a %d-star map", len(shards), mapped)

	bundleSay("── fleet: build the roster and its SIGNED twin from a staged tree ──")
	// The v2 records (fleet/stars/<n>/<n>.slag) are not bundle data: opa build
	// reads data.json, and a stray file in the stage is one more thing a future
	// builder could trip on.
	stage := dag.Directory().
		WithDirectory("fleet", src.Directory("fleet"), dagger.DirectoryWithDirectoryOpts{Exclude: []string{"**/*.slag"}}).
		WithNewFile(".manifest", bundlelane.FleetManifest)
	built = built.WithDirectory(workDir+"/stage", stage)
	for _, b := range [][]string{
		{"opa", "build", "-b", workDir + "/stage", "-o", workDir + "/fleet-bundle.tar.gz", "--revision", l.m.Sha},
		{"opa", "build", "-b", workDir + "/stage", "-o", workDir + "/fleet-bundle-signed.tar.gz", "--revision", l.m.Sha, "--signing-key", "/run/opa/sign.key", "--signing-alg", "ES256"},
	} {
		built = built.WithExec(b, anyExit)
		out, code, err := output(ctx, built)
		if err != nil {
			return nil, couldNotRun("opa build never ran: %v", err)
		}
		if code != 0 {
			return nil, findings("opa build over the staged roster failed:\n" + out)
		}
	}
	if g := twin(ctx, built, "fleet-bundle.tar.gz", "fleet-bundle-signed.tar.gz", "the signed roster"); g.code != buildlane.Clean {
		return nil, g
	}
	manifest, g := member(ctx, built, "fleet-bundle-signed.tar.gz", "/.manifest")
	if g.code != buildlane.Clean {
		return nil, g
	}
	if !bundlelane.RootsAreFleet(manifest) {
		return nil, findings("the signed roster's manifest no longer declares exactly [fleet]: " + manifest)
	}

	bundleSay("── fleet GATE: the artifact carries the roster it claims to ──")
	roster, g := member(ctx, built, "fleet-bundle.tar.gz", "/data.json")
	if g.code != buildlane.Clean {
		return nil, g
	}
	rows, stars, topics, problem, err := bundlelane.Roster(roster)
	if err != nil {
		return nil, findings(err.Error())
	}
	if problem != "" {
		return nil, findings(problem)
	}
	bundleSay("fleet: roster — %d stars mapped, %d lean rows, %d topics", rows, stars, topics)

	bundleSay("── fleet GATE: it composes with the policy bundle, and the guards FIRE ──")
	composed := built.WithExec([]string{"opa", "build", "-b", "policy/", "-o", workDir + "/policy.tar.gz", "--revision", "gate", "--ignore", "*_test.rego"}, anyExit)
	if out, code, err := output(ctx, composed); err != nil {
		return nil, couldNotRun("opa build never ran: %v", err)
	} else if code != 0 {
		return nil, findings("opa build over policy/ for the composition gate failed:\n" + out)
	}
	probe := workDir + "/planted-seams.json"
	out, code, g := exec(ctx, composed.WithNewFile(probe, bundlelane.PlantedSeams), "the composition probe",
		"opa", "eval", "-b", workDir+"/policy.tar.gz", "-b", workDir+"/fleet-bundle.tar.gz", "-i", probe, "--format", "json", "data.admission.deny")
	if g.code != buildlane.Clean {
		return nil, g
	}
	if code != 0 {
		return nil, findings("the roster does not compose with the policy bundle — opa refused to load both:\n" + out)
	}
	denies, err := checks.OpaDenySet(out)
	if err != nil {
		return nil, couldNotRun("the composition probe answered a shape this lane cannot read: %v", err)
	}
	if n := bundlelane.RosterDenials(denies); n != 2 {
		return nil, findings(fmt.Sprintf("planted seam violations produced %d/2 roster denials — data.fleet did not compose, or the guards stopped matching", n))
	}
	bundleSay("fleet: composition gate — 2/2 planted seam violations denied")
	return built, clean()
}

// publishDie pushes one die's two artifacts — the immutable pin, then its
// channel — and signs both by digest.
func (l *bundleLane) publishDie(ctx context.Context, built *dagger.Container, die, pin, plain, signed string) ([]string, gateResult) {
	bundleSay("── %s: publish — immutable pin first, then move the channel ──", die)
	oras, g := l.oras(ctx, built)
	if g.code != buildlane.Clean {
		return nil, g
	}
	digest, g := l.pushPin(ctx, oras, die, pin, plain, "stable")
	if g.code != buildlane.Clean {
		return nil, g
	}
	signedDigest, g := l.pushPin(ctx, oras, die, pin+"-signed", signed, "signed")
	if g.code != buildlane.Clean {
		return nil, g
	}
	bundleSay("── %s: sign + verify by digest ──", die)
	cosign, g := l.cosign(ctx)
	if g.code != buildlane.Clean {
		return nil, g
	}
	for _, ref := range []string{digest, signedDigest} {
		if g := signVerify(ctx, cosign, ref); g.code != buildlane.Clean {
			return nil, g
		}
	}
	return []string{digest, signedDigest}, clean()
}

// registryConfig is the docker config that logs rob in to foundry.notusmi.com.
func (l *bundleLane) registryConfig(ctx context.Context) (*dagger.Secret, gateResult) {
	token, err := l.registryToken.Plaintext(ctx)
	if err != nil {
		return nil, couldNotRun("the registry token did not read: %v", err)
	}
	return dag.SetSecret("bundle-registry-config", bundlelane.DockerConfig(bundlelane.RegistryHost, bundlelane.RegistryUser, strings.TrimSpace(token))), clean()
}

// oras is the build container with oras on PATH and the registry login
// mounted, working in workDir so each pushed layer is titled by its filename,
// as bundle.sh's were.
func (l *bundleLane) oras(ctx context.Context, built *dagger.Container) (*dagger.Container, gateResult) {
	tarball, err := fetchTool(ctx, checks.OrasURL)
	if err != nil {
		return nil, couldNotRun("oras could not be provisioned: %v", err)
	}
	cfg, g := l.registryConfig(ctx)
	if g.code != buildlane.Clean {
		return nil, g
	}
	return built.
		WithFile("/tmp/oras.tgz", tarball).
		WithExec([]string{"tar", "-xzf", "/tmp/oras.tgz", "-C", "/usr/local/bin", "oras"}).
		WithMountedSecret("/run/docker/config.json", cfg).
		WithEnvVariable("BUNDLE_RUN", l.stamp).
		WithWorkdir(workDir), clean()
}

// pushPin pushes file under die:tag unless that pin already resolves, points
// channel at the pin, and answers die@digest.
func (l *bundleLane) pushPin(ctx context.Context, oras *dagger.Container, die, tag, file, channel string) (string, gateResult) {
	ref := die + ":" + tag
	cfg := []string{"--registry-config", "/run/docker/config.json"}
	sum, code, g := exec(ctx, oras, "sha256sum "+file, "sha256sum", file)
	if g.code != buildlane.Clean {
		return "", g
	}
	local, err := bundlelane.FileDigest(sum)
	if code != 0 || err != nil {
		return "", couldNotRun("the built %s could not be hashed: %v %s", file, err, sum)
	}
	manifest, code, g := exec(ctx, oras, "oras manifest fetch "+ref, append([]string{"oras", "manifest", "fetch"}, append(cfg, ref)...)...)
	if g.code != buildlane.Clean {
		return "", g
	}
	if remote := bundlelane.PublishedLayer(manifest); code != 0 || remote == "" {
		out, code, g := exec(ctx, oras, "oras push "+ref, append([]string{"oras", "push"}, append(cfg,
			ref, "--artifact-type", bundlelane.BundleMediaType,
			"--annotation", "org.opencontainers.image.revision="+l.m.Sha,
			file+":"+bundlelane.LayerMediaType)...)...)
		if g.code != buildlane.Clean {
			return "", g
		}
		if code != 0 {
			c, why := buildlane.ToolFailed("oras push "+ref, out)
			return "", gateResult{c, why}
		}
		bundleSay("pushed %s", ref)
	} else {
		// A rebuild of one commit whose bytes differ is gzip non-determinism,
		// not a changed tree, and the published pin is the one that was gated.
		bundleSay("%s already stands (published layer %s, this build %s) — a pin is immutable, not re-pushing", ref, remote, local)
	}
	out, code, g := exec(ctx, oras, "oras tag "+ref, append([]string{"oras", "tag"}, append(cfg, ref, channel)...)...)
	if g.code != buildlane.Clean {
		return "", g
	}
	if code != 0 {
		c, why := buildlane.ToolFailed("oras tag "+ref+" "+channel, out)
		return "", gateResult{c, why}
	}
	out, code, g = exec(ctx, oras, "oras resolve "+ref, append([]string{"oras", "resolve"}, append(cfg, ref)...)...)
	if g.code != buildlane.Clean {
		return "", g
	}
	digest := buildlane.DigestOf(out)
	if code != 0 || digest == "" {
		return "", couldNotRun("%s did not resolve to a digest after its push (exit %d): %s", ref, code, out)
	}
	return die + "@" + digest, clean()
}

// cosign is the pinned cosign image holding the fleet key, its passphrase,
// the registry login and the repo's own cosign.pub.
func (l *bundleLane) cosign(ctx context.Context) (*dagger.Container, gateResult) {
	encoded, err := l.cosignKey.Plaintext(ctx)
	if err != nil {
		return nil, couldNotRun("the cosign key did not read: %v", err)
	}
	key, err := bundlelane.DecodeKey("COSIGN_PRIVATE_KEY", encoded)
	if err != nil {
		return nil, couldNotRun("%v", err)
	}
	cfg, g := l.registryConfig(ctx)
	if g.code != buildlane.Clean {
		return nil, g
	}
	nonroot := dagger.ContainerWithMountedSecretOpts{Owner: "65532:65532"}
	return cosignIn().
		WithMountedTemp("/tmp").
		WithEnvVariable("HOME", "/tmp").
		WithMountedSecret("/run/cosign/key", dag.SetSecret("bundle-cosign-key", key), nonroot).
		WithSecretVariable("COSIGN_PASSWORD", l.cosignPassphrase).
		WithMountedSecret("/run/docker/config.json", cfg, nonroot).
		WithEnvVariable("DOCKER_CONFIG", "/run/docker").
		WithFile("/run/cosign/cosign.pub", l.m.Source.File("cosign.pub")).
		WithEnvVariable("BUNDLE_RUN", l.stamp), clean()
}

// signVerify signs a digest unless its signature already verifies, then
// verifies it. cosign attests WHO published the artifact; the twin's
// .signatures.json answers whether the policy was altered in transit, and
// neither replaces the other.
func signVerify(ctx context.Context, cosign *dagger.Container, ref string) gateResult {
	verify := []string{"verify", "--key", "/run/cosign/cosign.pub", ref}
	_, code, err := output(ctx, cosign.WithExec(verify, entrypointAnyExit))
	if err != nil {
		return couldNotRun("cosign verify never ran for %s: %v", ref, err)
	}
	if code == 0 {
		bundleSay("%s already carries a signature this key verifies — not stacking a second one", ref)
		return clean()
	}
	out, code, err := output(ctx, cosign.WithExec([]string{"sign", "--key", "/run/cosign/key", "--yes", ref}, entrypointAnyExit))
	if err != nil {
		return couldNotRun("cosign sign never ran for %s: %v", ref, err)
	}
	if code != 0 {
		c, why := buildlane.ToolFailed("cosign sign "+ref, out)
		return gateResult{c, why}
	}
	// The check after signing is its own query: the same verify as the one
	// before it would be answered from that run, which failed.
	out, code, err = output(ctx, cosign.WithEnvVariable("BUNDLE_CHECK", "after-sign").WithExec(verify, entrypointAnyExit))
	if err != nil {
		return couldNotRun("cosign verify never ran for %s: %v", ref, err)
	}
	if code != 0 {
		c, why := buildlane.ToolFailed("cosign verify "+ref, out)
		return gateResult{c, why}
	}
	bundleSay("signed and verified %s", ref)
	return clean()
}
