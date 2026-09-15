package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE BUILD LANE, AS ONE FUNCTION. The door's build Job runs `dagger call …
// build` as its only process. What it replaces, whole: infra's ca-build
// tools.sh (a dagger and kubectl download) and build.py (a clone, an engine
// pod lookup, phase-by-phase subprocesses), and foundry-stocks'
// ci/lib/build/build.sh (detect, publish, sign, verdict) and permit.py. The
// decisions those made live in internal/buildlane as pure functions; this file is
// the chain.
//
// THE VERDICT IS THE EXIT CODE. Every path ends on settle(), whose last exec
// exits 0, 1 or 2, and `dagger call` exits with that code (measured
// 2026-09-14: an exec error's code survives a module function, bare or
// wrapped; a plain Go error exits 1). So nothing here returns a plain error
// for a verdict — a could-not-run can never reach the door as a finding.
//
// THE PERMIT IS ASKED AS THE CALLING POD. `spire` is the pod's SPIRE agent
// socket, forwarded by the CLI; the agent attests the CLI's pod, so hadescall
// holds the build lane's own SVID (ci/default/ca-build) exactly as permit.py
// did in that pod.

// Build builds the commit the module was constructed on and settles the
// build lane. A pull builds the image and publishes nothing; a tip publishes
// it under the g-pin, signs it, attests its SBOM and asks hades for the
// permit. A commit whose every change since the last permitted build (:stable)
// is inert builds nothing.
func (m *FoundryTools) Build(
	ctx context.Context,
	// The default branch's tip: publish, sign, attest and permit. Without it
	// the lane settles on the build alone.
	// +optional
	tip bool,
	// The SPIRE agent's workload socket, forwarded by the calling pod — the
	// identity the permit is asked as. Required with --tip.
	// +optional
	spire *dagger.Socket,
	// The registry credential, a docker config JSON (ca-build-registry's
	// REGISTRY_AUTH_JSON). Required with --tip.
	// +optional
	registryAuth *dagger.Secret,
	// The CI signing key: base64 of the cosign private key
	// (CI_COSIGN_PRIVATE_KEY). Required with --tip.
	// +optional
	cosignKey *dagger.Secret,
	// The CI signing key's passphrase (CI_COSIGN_PASSWORD). Required with --tip.
	// +optional
	cosignPassphrase *dagger.Secret,
	// The python index a Dockerfile RUN reads as UV_INDEX_URL.
	// +optional
	indexURL string,
	// The registry the image is pushed to.
	// +optional
	// +default="registry.notusmi.com"
	registry string,
	// Where org.opencontainers.image.source points: <sourceBase>/<star>.
	// +optional
	// +default="https://forgejo.notusmi.com/rob"
	sourceBase string,
	// hades' mTLS address.
	// +optional
	// +default="https://hades.default.svc.cluster.local:8102"
	hades string,
	// The SPIFFE id hades must present.
	// +optional
	// +default="spiffe://notusmi.com/star/hades"
	hadesID string,
) error {
	l := &buildLane{
		m: m, tip: tip, spire: spire,
		registryAuth: registryAuth, cosignKey: cosignKey, cosignPassphrase: cosignPassphrase,
		indexURL: indexURL, registry: registry, sourceBase: sourceBase, hades: hades, hadesID: hadesID,
		stamp: strconv.FormatInt(time.Now().UnixNano(), 10),
	}
	code, reason := l.run(ctx)
	return settle(ctx, code, "build: "+reason)
}

type buildLane struct {
	m                                         *FoundryTools
	tip                                       bool
	spire                                     *dagger.Socket
	registryAuth, cosignKey, cosignPassphrase *dagger.Secret
	indexURL, registry, sourceBase            string
	hades, hadesID                            string
	// stamp is this run's, on every step that must happen again on a rerun of
	// the same commit: signing, attesting and the permit are acts, not results.
	stamp string
}

func say(format string, args ...any) { fmt.Fprintf(os.Stderr, "build: "+format+"\n", args...) }

// starOf is the star a repository URL or custody key names:
// http://ourea…:8215/rob/ares.git and rob/ares are both ares.
func starOf(repo string) string { return path.Base(strings.TrimSuffix(repo, ".git")) }

// withheld answers whether a tip was handed less than it publishes, signs and
// permits with: the socket and every secret.
func withheld(spire *dagger.Socket, secrets ...*dagger.Secret) bool {
	if spire == nil {
		return true
	}
	for _, s := range secrets {
		if s == nil {
			return true
		}
	}
	return false
}

func (l *buildLane) run(ctx context.Context) (int, string) {
	m := l.m
	if m.Repo == "" || m.Sha == "" {
		return buildlane.CouldNotRun, "the build lane builds a commit the engine fetched — construct the module with --repo and --sha"
	}
	if l.tip && withheld(l.spire, l.registryAuth, l.cosignKey, l.cosignPassphrase) {
		return buildlane.CouldNotRun, "a tip build publishes, signs and permits: --spire, --registry-auth, --cosign-key and --cosign-passphrase are all required"
	}
	star := starOf(m.Repo)
	say("%s for %s at %.12s", map[bool]string{true: "tip build", false: "pull-time build (publishes nothing)"}[l.tip], star, m.Sha)

	compose := ""
	for _, f := range buildlane.ComposeFiles {
		body, ok, err := fileIn(ctx, m.Source, f)
		if err != nil {
			return buildlane.CouldNotRun, fmt.Sprintf("could not read %s: %v", f, err)
		}
		if ok {
			compose = body
			break
		}
	}
	pushRepo := buildlane.PushRepo(l.registry, buildlane.DeclaredImage(compose, star))

	needed, why, code := l.detect(ctx, pushRepo)
	say("%s", why)
	if code != buildlane.Clean {
		return code, why
	}
	if !needed {
		return buildlane.Clean, "stood down: " + why
	}

	var args []string
	if env, ok, err := fileIn(ctx, m.Source, ".forgejo/build-args.env"); err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not read .forgejo/build-args.env: %v", err)
	} else if ok {
		args = buildlane.BuildArgs(env)
		say("build args: %d from .forgejo/build-args.env", len(args))
	}
	if l.indexURL != "" {
		args = append(args, "UV_INDEX_URL="+l.indexURL)
	}
	img, err := m.Image(m.Sha, l.sourceBase+"/"+star, star, args)
	if err != nil {
		return buildlane.CouldNotRun, err.Error()
	}

	if !l.tip {
		if _, err := img.Container().Sync(ctx); err != nil {
			return buildlane.Failed("the image build", err.Error())
		}
		return buildlane.Clean, fmt.Sprintf("clean: built %s at %.12s — nothing published, signed or permitted; the landing does that", star, m.Sha)
	}

	ref, code, why := l.publish(ctx, img, pushRepo, star)
	if code != buildlane.Clean {
		return code, why
	}
	if code, why := l.sign(ctx, img, ref, star); code != buildlane.Clean {
		return code, why
	}
	code, why = l.permit(ctx, star)
	if code != buildlane.Clean {
		return code, why
	}
	return buildlane.Clean, fmt.Sprintf("clean: published and signed %s; %s", ref, why)
}

// detect answers whether this commit needs building. The question is whether
// :stable already carries its source, not whether this push changed any.
// :stable is the permit's own output (hephaestus' mold stamps it), and the
// image under it names the commit it was built from. So the change set is
// taken since THAT commit: every change inert stands down, anything else
// builds.
//
// THE PARENT WAS THE WRONG BEFORE-REF, measured 2026-09-14 on athena. a446514
// changed source and failed at sign, so nothing was permitted; 570be49,
// quickstart.md alone on top of it, diffed inert against its parent and stood
// down, leaving main two landings ahead of :stable with no build coming until
// someone changed source again. build.sh's detect had the same rule.
//
// Anything that leaves the permitted source unknown builds: no :stable, a
// :stable whose image names no commit, a permitted commit outside this
// history.
func (l *buildLane) detect(ctx context.Context, pushRepo string) (needed bool, why string, code int) {
	label, err := dag.Container().From(pushRepo+":stable").Label(ctx, "org.opencontainers.image.revision")
	if err != nil {
		return true, fmt.Sprintf("no permitted build to compare against: %s:stable did not read (%.200s) — building to be safe", pushRepo, err.Error()), buildlane.Clean
	}
	permitted := strings.TrimSpace(label)
	if !buildlane.IsCommit(permitted) {
		return true, fmt.Sprintf("no permitted build to compare against: %s:stable names no commit (revision %q) — building to be safe", pushRepo, permitted), buildlane.Clean
	}
	r := newRun(l.m.Source, l.m.Repo, "")
	git := r.gitReady(ctx, r.lane(checks.ImageFleet))
	// ASK WHETHER THIS CHECKOUT CARRIES THE PERMIT BEFORE ASKING ABOUT ANCESTRY.
	// A pull branched before :stable's commit does not carry it, and `git
	// merge-base --is-ancestor` on a commit it cannot name exits 128 — and
	// Expect ANY covers exit codes 0-127 and 192-255 only (the SDK's own
	// ReturnTypeAny), so the engine reports 128 as an ERROR, the lane filed it
	// could-not-run, and automerge re-asked forever. Measured 2026-09-15 on
	// terpsichore bf13591, hephaestus e353d2b, nyx 27945a7, hades 3d74db7 and
	// ourea 8e7e113. `rev-parse --verify --quiet` answers the same question
	// with a quiet exit 1.
	_, present, err := output(ctx, git.WithExec([]string{"git", "rev-parse", "--verify", "--quiet", permitted + "^{commit}"}, anyExit))
	if err != nil {
		return false, fmt.Sprintf("could not run: the history could not be read: %v", err), buildlane.CouldNotRun
	}
	if present != 0 {
		return true, fmt.Sprintf("the last permitted build %.12s is not in this checkout (a branch older than the permit) — building", permitted), buildlane.Clean
	}
	_, ancestry, err := output(ctx, git.WithExec([]string{"git", "merge-base", "--is-ancestor", permitted, "HEAD"}, anyExit))
	if err != nil {
		return false, fmt.Sprintf("could not run: the history could not be read: %v", err), buildlane.CouldNotRun
	}
	if ancestry != 0 {
		return true, fmt.Sprintf("the last permitted build %.12s is not in this commit's history (git exit %d) — building", permitted, ancestry), buildlane.Clean
	}
	changed, rc, err := output(ctx, git.WithExec([]string{"git", "diff", "--name-only", permitted, "HEAD"}, anyExit))
	if err != nil || rc != 0 {
		return false, fmt.Sprintf("could not run: the change set could not be read (exit %d): %v %s", rc, err, changed), buildlane.CouldNotRun
	}
	needed, why = buildlane.Standing(permitted, changed)
	return needed, why, buildlane.Clean
}

// publish pushes the image under the g-pin and answers `<repo>@<digest>`.
func (l *buildLane) publish(ctx context.Context, img *Image, pushRepo, star string) (string, int, string) {
	gpin, err := buildlane.GPin(pushRepo, l.m.Sha)
	if err != nil {
		return "", buildlane.CouldNotRun, err.Error()
	}
	host, _, _ := strings.Cut(pushRepo, "/")
	auth, err := l.registryAuth.Plaintext(ctx)
	if err != nil {
		return "", buildlane.CouldNotRun, fmt.Sprintf("could not run: the registry credential did not read: %v", err)
	}
	user, password, err := buildlane.RegistryLogin(auth, host)
	if err != nil {
		return "", buildlane.CouldNotRun, "could not run: " + err.Error()
	}
	out, err := img.Publish(ctx, gpin, user, dag.SetSecret("build-registry-password-"+star, password))
	if err != nil {
		code, why := buildlane.Failed("publish", err.Error())
		return "", code, why
	}
	digest := buildlane.DigestOf(out)
	if digest == "" {
		return "", buildlane.CouldNotRun, fmt.Sprintf("could not run: the registry minted no digest for %s: %.200s", gpin, out)
	}
	ref := pushRepo + "@" + digest
	say("published %s = %s", gpin, ref)
	return ref, buildlane.Clean, ""
}

// sign signs the published image with the CI key, reads its SBOM (the builder
// stage's dependencies folded in when the Dockerfile names one), attaches the
// SBOM and attests a pointer to it (attestSBOM), and verifies the signature
// against the key's public half. A signature or pointer that is already there
// verifies instead of failing.
func (l *buildLane) sign(ctx context.Context, img *Image, ref, star string) (int, string) {
	encoded, err := l.cosignKey.Plaintext(ctx)
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: the CI signing key did not read: %v", err)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return buildlane.CouldNotRun, "could not run: the CI signing key is not base64"
	}
	cosign := dag.Container().From(checks.ImageCosign).
		WithMountedTemp("/tmp").
		WithEnvVariable("HOME", "/tmp").
		WithMountedSecret("/run/cosign/key", dag.SetSecret("build-cosign-key-"+star, string(key)), dagger.ContainerWithMountedSecretOpts{Owner: "65532:65532"}).
		WithSecretVariable("COSIGN_PASSWORD", l.cosignPassphrase).
		WithMountedSecret("/run/docker/config.json", l.registryAuth, dagger.ContainerWithMountedSecretOpts{Owner: "65532:65532"}).
		WithEnvVariable("DOCKER_CONFIG", "/run/docker").
		WithEnvVariable("BUILD_RUN", l.stamp)

	pub, code, err := output(ctx, cosign.WithExec([]string{"public-key", "--key", "/run/cosign/key"}, entrypointAnyExit))
	if err != nil || code != 0 {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: cosign could not derive the public key — the key or its password is wrong (exit %d): %v", code, err)
	}
	cosign = cosign.WithNewFile("/run/cosign/key.pub", pub+"\n")

	if code, why := orVerify(ctx, "sign",
		cosign.WithExec([]string{"sign", "--key", "/run/cosign/key", "--yes", "--tlog-upload=false", "--use-signing-config=false", ref}, entrypointAnyExit),
		cosign.WithExec([]string{"verify", "--key", "/run/cosign/key.pub", "--insecure-ignore-tlog=true", ref}, entrypointAnyExit),
	); code != buildlane.Clean {
		return code, why
	}
	say("signed %s with the CI key — the fleet signature is mold's to write, at permit", ref)

	sbom, code, why := l.sbom(ctx, img, ref)
	if code != buildlane.Clean {
		return code, why
	}
	if code, why := l.attestSBOM(ctx, cosign, ref, sbom); code != buildlane.Clean {
		return code, why
	}
	out, code, err := output(ctx, cosign.WithExec([]string{"verify", "--key", "/run/cosign/key.pub", "--insecure-ignore-tlog=true", ref}, entrypointAnyExit))
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: the signature check did not run: %v", err)
	}
	if code != 0 {
		return buildlane.ToolFailed("sign (verify)", out)
	}
	say("DEPLOY_DIGEST=%s", ref)
	return buildlane.Clean, ""
}

// entrypointAnyExit runs a tool image's own entrypoint and lets its exit code
// reach the caller.
var entrypointAnyExit = dagger.ContainerWithExecOpts{UseEntrypoint: true, Expect: dagger.ReturnTypeAny}

// orVerify runs act and, when it fails, check: an act whose result is already
// in place passes on the check.
func orVerify(ctx context.Context, step string, act, check *dagger.Container) (int, string) {
	out, code, err := output(ctx, act)
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: %s did not run: %v", step, err)
	}
	if code == 0 {
		return buildlane.Clean, ""
	}
	checked, ccode, cerr := output(ctx, check)
	if cerr == nil && ccode == 0 {
		return buildlane.Clean, ""
	}
	return buildlane.ToolFailed(step, out+"\n"+checked)
}

// sbom reads the published image's CycloneDX SBOM, folding in the builder
// stage's when the Dockerfile names one and it builds.
func (l *buildLane) sbom(ctx context.Context, img *Image, ref string) (string, int, string) {
	syft := dag.Container().From(checks.ImageSyft).
		WithMountedSecret("/run/docker/config.json", l.registryAuth).
		WithEnvVariable("DOCKER_CONFIG", "/run/docker")
	exclude := []string{"--exclude", "/root/.bun/**", "-o", "cyclonedx-json@1.6"}
	image, code, err := output(ctx, syft.WithExec(append([]string{"registry:" + ref}, exclude...), entrypointAnyExit))
	if err != nil {
		return "", buildlane.CouldNotRun, fmt.Sprintf("could not run: syft did not run: %v", err)
	}
	if code != 0 {
		c, why := buildlane.ToolFailed("sign (SBOM)", image)
		return "", c, why
	}
	dockerfile, _, err := fileIn(ctx, l.m.Source, "Dockerfile")
	if err != nil || !buildlane.HasBuilderStage(dockerfile) {
		say("no builder stage in this Dockerfile — the SBOM covers the runtime image only")
		return image, buildlane.Clean, ""
	}
	tar := img.Builder()
	if _, err := tar.Size(ctx); err != nil {
		say("WARNING the builder stage did not build — the SBOM covers the runtime image only: %v", err)
		return image, buildlane.Clean, ""
	}
	builder, code, err := output(ctx, syft.WithMountedFile("/in/builder.tar", tar).
		WithExec(append([]string{"oci-archive:/in/builder.tar"}, exclude...), entrypointAnyExit))
	if err != nil || code != 0 {
		say("WARNING the builder stage's SBOM did not read — the attestation covers the runtime image only")
		return image, buildlane.Clean, ""
	}
	merged, in, bn, mn, err := buildlane.MergeSBOM([]byte(image), []byte(builder))
	if err != nil {
		say("WARNING the SBOMs did not merge (%v) — the attestation covers the runtime image only", err)
		return image, buildlane.Clean, ""
	}
	say("SBOM components: image=%d builder=%d merged=%d", in, bn, mn)
	return string(merged), buildlane.Clean, ""
}

// permit asks hades for forge_mold on this star, as the calling pod, and folds
// the answer into the verdict.
func (l *buildLane) permit(ctx context.Context, star string) (int, string) {
	args, err := json.Marshal(map[string]string{"name": star})
	if err != nil {
		return buildlane.CouldNotRun, err.Error()
	}
	call := hadesCaller(l.spire, l.hades, l.hadesID, l.stamp).
		WithExec([]string{"/usr/local/bin/hadescall", "forge_mold", string(args)}, anyExit)
	out, code, err := output(ctx, call)
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: could not ask hades: %v", err)
	}
	if code != 0 {
		return buildlane.CouldNotRun, "could not run: could not ask hades: " + out
	}
	status, body, err := buildlane.ParseCall(out)
	if err != nil {
		return buildlane.CouldNotRun, "could not run: " + err.Error()
	}
	say("hades answered forge_mold HTTP %d", status)
	return buildlane.Permit(status, body, l.m.Sha)
}

// hadesCaller is the container hadescall runs in: the binary built from this
// module's own source, on the empty static base, with the socket the caller
// forwarded.
//
// THE SOCKET IS OWNED BY THE EXEC'S USER. A forwarded socket appears in the
// container as srw------- root:root, but the static base runs as nonroot
// (65532), which cannot connect to it. go-spiffe retries the refused connect
// silently until its deadline. That is how the first tip permit through here
// failed: athena b66d46f, 2026-09-14, settled "no identity … within 2m0s"
// twice. Measured in a ca-build pod on llm01, the same exec fetched ca-build
// at once either as root or with the socket owned by 65532. F0's probe had
// passed only because spire-agent ran as root in its own image.
func hadesCaller(spire *dagger.Socket, hades, hadesID, stamp string) *dagger.Container {
	bin := goToolchain().
		WithMountedDirectory("/src", dag.CurrentModule().Source()).
		WithWorkdir("/src").
		WithExec([]string{"go", "build", "-o", "/out/hadescall", "./hadescall"}).
		File("/out/hadescall")
	return dag.Container().From(checks.ImageStatic).
		WithFile("/usr/local/bin/hadescall", bin).
		WithUnixSocket("/run/spire/agent.sock", spire, dagger.ContainerWithUnixSocketOpts{Owner: "65532:65532"}).
		WithEnvVariable("HADESCALL_SOCKET", "unix:///run/spire/agent.sock").
		WithEnvVariable("HADESCALL_HADES", hades).
		WithEnvVariable("HADESCALL_HADES_ID", hadesID).
		WithEnvVariable("BUILD_RUN", stamp)
}

// goToolchain is the Go image with the fleet's proxy coordinates and the Go
// lane's caches, for building the lane's own binaries.
func goToolchain() *dagger.Container {
	ctr := dag.Container().From(checks.ImageGo).
		WithEnvVariable("GOPROXY", checks.GoProxy).
		WithEnvVariable("GONOSUMDB", checks.GoNoSumDB).
		WithEnvVariable("GOPRIVATE", checks.GoPrivate).
		WithEnvVariable("CGO_ENABLED", "0")
	for _, c := range checks.CachesFor(checks.ImageGo) {
		opts := dagger.ContainerWithMountedCacheOpts{}
		if c.Seed {
			opts.Source = dag.Container().From(checks.ImageGo).Directory(c.Path)
		}
		ctr = ctr.WithMountedCache(c.Path, dag.CacheVolume(c.Key), opts)
		if c.EnvVar != "" {
			ctr = ctr.WithEnvVariable(c.EnvVar, c.Path)
		}
	}
	return ctr
}

// settle ends a lane on the verdict exec, so `dagger call` exits with the code
// the door settles on and prints the reason beside it. verdict is standard
// library only and is built with GOPROXY=off: it has to run when nothing else
// could be fetched (internal/verdict has the measurement).
func settle(ctx context.Context, code int, reason string) error {
	bin := dag.Container().From(checks.ImageGo).
		WithEnvVariable("CGO_ENABLED", "0").
		WithEnvVariable("GOTOOLCHAIN", "local").
		WithEnvVariable("GOPROXY", "off").
		WithMountedDirectory("/src", dag.CurrentModule().Source()).
		WithWorkdir("/src").
		WithExec([]string{"go", "build", "-o", "/out/verdict", "./verdict"}).
		File("/out/verdict")
	_, err := dag.Container().From(checks.ImageStatic).
		WithFile("/usr/local/bin/verdict", bin).
		WithEnvVariable("LANE_SETTLED_AT", strconv.FormatInt(time.Now().UnixNano(), 10)).
		WithExec([]string{"/usr/local/bin/verdict", strconv.Itoa(code), reason}).
		Sync(ctx)
	return err
}

// fileIn answers a file's contents when it exists in dir, and false when it
// does not; an error only when the tree could not be read.
func fileIn(ctx context.Context, dir *dagger.Directory, file string) (string, bool, error) {
	matches, err := dir.Glob(ctx, file)
	if err != nil {
		return "", false, err
	}
	if len(matches) == 0 {
		return "", false, nil
	}
	body, err := dir.File(file).Contents(ctx)
	if err != nil {
		return "", false, err
	}
	return body, true, nil
}
