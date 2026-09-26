package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/castlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/pins"
)

// THE CAST LANE, AS ONE FUNCTION. The door's cast Job runs `dagger call …
// cast` as its only process. What it replaces: infra's ca-cast script, which a
// binary repo reached through a one-line ci/cast.sh, and the Python caster it
// pip-installed from a hephaestus pin 580 commits behind main. The decisions
// live in internal/castlane; this file is the chain.
//
// THE RECORD NAMES WHAT SHIPS. tools.cast (foundry-dies#226) carries the two
// facts ci/cast.sh passed as arguments; the channel is app/<meta.name>:stable
// for every binary repo. A repo whose record has no tools.cast is a finding,
// not a fall-back to its tree.
//
// MOLD MINTS AND SIGNS. The lane stages the payload unsigned under
// {registry}/staging/, which grants nothing, and asks hades for forge_mold as
// the calling pod. Mold pulls the staged tree, re-derives its pin (a mismatch
// is tamper and refuses), allocates the channel's next index and signs. Then
// the lane verifies the landed digest against the repo's OWN cosign.pub —
// the step that makes the cast a claim rather than a hope.

// Cast builds a binary repo's release binaries at the commit the module was
// constructed on and casts them as the bundle its record names.
func (m *FoundryTools) Cast(
	ctx context.Context,
	// The SPIRE agent's workload socket, forwarded by the calling pod: the
	// identity forge_mold is asked as. Required unless --dry-run.
	// +optional
	spire *dagger.Socket,
	// The token that logs in to the bundle registry (REGISTRY_TOKEN). Required
	// unless --dry-run.
	// +optional
	registryToken *dagger.Secret,
	// Where the release is announced, best effort: the redpanda REST topic
	// hephaestus.releases. Empty rings nothing.
	// +optional
	doorbellURL string,
	// hades' mTLS address. BARE NAME: it pinned the `default` namespace until
	// 2026-09-25 and the fleet now runs in `prime`. A caller that needs another
	// address passes one — this parameter is the configuration path.
	// +optional
	// +default="https://hades:8102"
	hades string,
	// The SPIFFE id hades must present.
	// +optional
	// +default="spiffe://notusmi.com/star/hades"
	hadesID string,
	// Build, assemble and pin the payload for real; stage, mint and verify
	// nothing.
	// +optional
	dryRun bool,
) error {
	l := &castLane{
		m: m, spire: spire, registryToken: registryToken, doorbellURL: doorbellURL,
		hades: hades, hadesID: hadesID, dryRun: dryRun,
		stamp: strconv.FormatInt(time.Now().UnixNano(), 10),
	}
	code, reason := l.run(ctx)
	return settle(ctx, code, "cast: "+reason)
}

type castLane struct {
	m              *FoundryTools
	spire          *dagger.Socket
	registryToken  *dagger.Secret
	doorbellURL    string
	hades, hadesID string
	dryRun         bool
	registryConfig *dagger.Secret
	// stamp is this run's, on every act — the push, the mint, the verify and
	// the bell — and never on the build, which is a result.
	stamp string
}

// castTarget is where the cast's release build writes: in the container's own
// filesystem, so the binaries can be read back out.
const castTarget = "/work/target"

func castSay(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "cast: "+format+"\n", args...)
}

func (l *castLane) run(ctx context.Context) (int, string) {
	m := l.m
	if m.Repo == "" || m.Sha == "" {
		return buildlane.CouldNotRun, "could not run: the cast lane casts a commit the engine fetched — construct the module with --repo and --sha"
	}
	if !l.dryRun && (l.spire == nil || l.registryToken == nil) {
		return buildlane.CouldNotRun, "could not run: a cast stages, mints and verifies: --spire and --registry-token are both required (--dry-run needs neither)"
	}
	star := starOf(m.Repo)
	shard := "fleet/stars/" + star + "/slag.json"
	slag, err := dag.Git(checks.DiesRepo).Ref(checks.DiesRef).Tree().File(shard).Contents(ctx)
	if err != nil || strings.TrimSpace(slag) == "" {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: no record for %s could be read at foundry-dies %s, so nothing says what it ships (%v)", star, shard, err)
	}
	c, err := castlane.FromRecord(slag)
	if err != nil {
		return buildlane.Findings, "findings: " + err.Error()
	}
	mode := ""
	if l.dryRun {
		mode = " — dry run: the build and the pin run for real; nothing is staged, minted or verified"
	}
	castSay("%s at %.12s, binaries %v, payload_extra %v%s", c.Artifact(), m.Sha, c.Binaries, c.PayloadExtra, mode)

	if _, ok, err := fileIn(ctx, m.Source, "cosign.pub"); err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: the tree could not be read for cosign.pub: %v", err)
	} else if !ok {
		return buildlane.Findings, "findings: this repo carries no cosign.pub, so a landed digest could not be verified — nothing was built"
	}
	payload, code, why := l.payload(ctx, c)
	if code != buildlane.Clean {
		return code, why
	}
	pin, files, code, why := l.pin(ctx, payload)
	if code != buildlane.Clean {
		return code, why
	}
	castSay("payload pins to %s over %d file(s): %s", pin, len(files), strings.Join(files, ", "))
	if l.dryRun {
		return buildlane.Clean, fmt.Sprintf("clean: dry run — %d file(s) pin to %s for %s; nothing was staged, minted or verified", len(files), pin, c.Artifact())
	}

	token, err := l.registryToken.Plaintext(ctx)
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: the registry token did not read: %v", err)
	}
	l.registryConfig = dag.SetSecret("cast-registry-config", bundlelane.DockerConfig(bundlelane.RegistryHost, bundlelane.RegistryUser, strings.TrimSpace(token)))

	ref := c.Stage(bundlelane.RegistryHost, pin)
	digest, code, why := l.stage(ctx, payload, ref, files)
	if code != buildlane.Clean {
		return code, why
	}
	castSay("staged %s@%s", ref, digest)
	r, code, why := l.mint(ctx, c, ref+"@"+digest, pin)
	if code != buildlane.Clean {
		return code, why
	}
	castSay("mold minted %s at index %d (%s, %s)", r.Channel, r.Index, r.Pin, r.Digest)
	if code, why := l.verify(ctx, c, r); code != buildlane.Clean {
		return code, why
	}
	// DECLARED AFTER VERIFY AND BEFORE THE BELL. After, because a pin is a claim
	// that this digest is the artifact — unverified is exactly the thing not to
	// put in a reaper's keep-set. Before, because ring is best-effort and a
	// doorbell that does not answer must not cost the declaration.
	//
	// WHY THE CAST DECLARES AT ALL. erebus.build_artifacts held only what the
	// BUILD lane declared, so /custody/keepset — the door's answer to "what must
	// retention keep" — could name images and not one bundle. Measured
	// 2026-09-26 against the live endpoint: 183 rows, 44 artifacts, 40 repos,
	// every row lane=build kind=image, and nothing under app/. Meanwhile the
	// census that marks inuse-* reads git pins and the cluster, and a bundle is
	// consumed by tongs on a workstation where no pod runs it. So app/* was
	// invisible to BOTH halves at once, and zot's retention keeps it today only
	// because a `.*` catch-all keeps every tag unconditionally.
	//
	// The tag is the PIN, not `stable`. stable moves; the keep-set is latest
	// plus one rollback, and a rollback names a version.
	declare(castSay, pins.Bundle(bundlelane.RegistryHost+"/app/"+c.Name+":"+r.Pin, r.Digest))
	bell := l.ring(ctx, c, r)
	noop := ""
	if r.NoOp {
		noop = " (the channel's head already carried this pin; mold re-signed it and allocated no index)"
	}
	return buildlane.Clean, fmt.Sprintf("clean: cast %s at index %d (%s, %s) — staged, minted and signed by mold, verified against cosign.pub%s%s", c.Artifact(), r.Index, r.Pin, r.Digest, noop, bell)
}

// payload builds the release binaries and assembles what ships: each binary at
// the payload's root, and each payload_extra under its basename. A binary the
// build did not leave, or an extra the checkout does not carry, is refused
// rather than skipped: a bundle missing its hooks verifies clean and fails only
// at runtime.
func (l *castLane) payload(ctx context.Context, c castlane.Cast) (*dagger.Directory, int, string) {
	built, dir, code, why := l.release(ctx, c)
	if code != buildlane.Clean {
		return nil, code, why
	}
	payload := dag.Directory()
	for _, b := range c.Binaries {
		f := built.File(path.Join(dir, b))
		if _, err := f.Size(ctx); err != nil {
			return nil, buildlane.Findings, fmt.Sprintf("findings: the release build left no %q in %s", b, dir)
		}
		payload = payload.WithFile(b, f)
	}
	for _, p := range c.PayloadExtra {
		under, err := l.m.Source.Glob(ctx, path.Join(p, "**"))
		if err != nil {
			return nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: the tree could not be read for %s: %v", p, err)
		}
		if len(under) > 0 {
			payload = payload.WithDirectory(castlane.Target(p), l.m.Source.Directory(p))
			continue
		}
		_, ok, err := fileIn(ctx, l.m.Source, p)
		if err != nil {
			return nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: the tree could not be read for %s: %v", p, err)
		}
		if !ok {
			return nil, buildlane.Findings, fmt.Sprintf("findings: tools.cast.payload_extra names %s, which this checkout does not carry", p)
		}
		payload = payload.WithFile(castlane.Target(p), l.m.Source.File(p))
	}
	return payload, buildlane.Clean, ""
}

// release compiles tools.cast.binaries in the lane the tree declares and
// answers the container holding them and the directory they were written to.
//
// THE LANE IS A FACT ABOUT THE TREE — never the record, never the copier
// template — which is the rule every atom in this module already follows
// (checks/lane.go says why). Rust is a Cargo.toml at the root; Go is a go.mod
// at the root, read through the same module walk run.plan uses. A record that
// says binary over a tree that declares neither is refused BY NAME: the lane
// used to fall through to cargo unconditionally, which is how argus, a Go
// repo, spent a week settling `could not find Cargo.toml` on every landing
// (foundry-tools #9839) — a red about a toolchain nobody asked for, hidden
// under six "automerge on green" landings whose gate was green and whose cast
// was not. A tree declaring both is refused too: two toolchains that each
// build a `tongs` would ship whichever ran last.
func (l *castLane) release(ctx context.Context, c castlane.Cast) (*dagger.Container, string, int, string) {
	r := newRun(l.m.Source, l.m.Repo, "")
	entries, err := l.m.Source.Entries(ctx)
	if err != nil {
		return nil, "", buildlane.CouldNotRun, fmt.Sprintf("could not run: the repository root could not be read: %v", err)
	}
	mods, err := r.goModuleDirs(ctx)
	if err != nil {
		return nil, "", buildlane.CouldNotRun, fmt.Sprintf("could not run: the tree's Go modules could not be enumerated: %v", err)
	}
	rust := checks.DeclaresLane(entries, checks.LaneRust)
	goRoot := slices.Contains(mods, ".")
	switch {
	case rust && goRoot:
		return nil, "", buildlane.Findings, "findings: the tree declares both a Cargo.toml and a root go.mod, so the cast cannot tell which toolchain builds tools.cast.binaries"
	case rust:
		return l.cargoRelease(ctx, r)
	case goRoot:
		return l.goRelease(ctx, r, c, slices.Contains(entries, "vendor/"))
	}
	return nil, "", buildlane.Findings, "findings: the record produces binary, but the tree declares no lane that builds one — no Cargo.toml and no go.mod at the root — so nothing can compile tools.cast.binaries"
}

// cargoRelease is the rust lane's release build: one `cargo build --release`
// over the workspace, the binaries read back out of target/release.
func (l *castLane) cargoRelease(ctx context.Context, r *run) (*dagger.Container, string, int, string) {
	// THE TARGET DIRECTORY IS THE CONTAINER'S, NOT THE LANE'S CACHE. The rust
	// lane points CARGO_TARGET_DIR at a cache volume so the gate's builds stay
	// warm, and a file in a cache mount is not in the container's filesystem:
	// the release binaries could never be read back out of it. The registry
	// cache stays mounted, so a cast re-downloads nothing; it recompiles, as
	// ca-cast always did.
	built := r.lane(checks.ImageRust).
		WithEnvVariable("CARGO_TARGET_DIR", castTarget).
		WithExec([]string{"cargo", "build", "--release", "--locked"}, anyExit)
	out, code, err := output(ctx, built)
	if err != nil {
		return nil, "", buildlane.CouldNotRun, fmt.Sprintf("could not run: the release build did not run: %v", err)
	}
	if code != 0 {
		cls, why := buildlane.ToolFailed("cargo build", out)
		return nil, "", cls, why
	}
	return built, castTarget + "/release", buildlane.Clean, ""
}

// goRelease is the go lane's release build: one exec per declared binary,
// each from ./cmd/<name>, with the flag set every Go image in the fleet is
// built with (checks.ReleaseFlags — measured identical across all 29, no
// exceptions) and -mod=vendor exactly when the module vendors, into
// checks.ReleaseOut. Nothing is derived from cmd/: helios and thalia carry
// four cmd/ directories between them that ship in no image, so the binaries
// are the record's and only the record's.
//
// THE EXIT IS READ AFTER EACH EXEC, not once at the end. The chain runs on
// under ReturnTypeAny, so with one read a first binary that failed to compile
// would be reported by a second that did not, and the finding would arrive
// later as "left no <bin>" with the compiler's own words gone.
func (l *castLane) goRelease(ctx context.Context, r *run, c castlane.Cast, vendored bool) (*dagger.Container, string, int, string) {
	ctr := r.goModules(".").WithEnvVariable("CGO_ENABLED", "0")
	for _, name := range c.Binaries {
		b := checks.ReleaseBinary{Name: name, Package: "./cmd/" + name}
		ctr = ctr.WithExec(checks.GoReleaseArgs(b, vendored), anyExit)
		out, code, err := output(ctx, ctr)
		if err != nil {
			return nil, "", buildlane.CouldNotRun, fmt.Sprintf("could not run: the release build of %s did not run: %v", b.Package, err)
		}
		if code != 0 {
			cls, why := buildlane.ToolFailed("go build "+b.Package, out)
			return nil, "", cls, why
		}
	}
	return ctr, checks.ReleaseOut, buildlane.Clean, ""
}

// pin runs castpin, built from this module's own source, over the payload:
// the content pin mold re-derives, and the files in the order they are pushed.
// It runs in a container so the binaries' bytes never cross the engine's API.
func (l *castLane) pin(ctx context.Context, payload *dagger.Directory) (string, []string, int, string) {
	bin := goToolchain().
		WithMountedDirectory("/src", dag.CurrentModule().Source()).
		WithWorkdir("/src").
		WithExec([]string{"go", "build", "-o", "/out/castpin", "./castpin"}).
		File("/out/castpin")
	out, code, err := output(ctx, dag.Container().From(checks.ImageStatic).
		WithFile("/usr/local/bin/castpin", bin).
		WithMountedDirectory("/payload", payload).
		WithExec([]string{"/usr/local/bin/castpin", "/payload"}, anyExit))
	if err != nil {
		return "", nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: the payload could not be pinned: %v", err)
	}
	if code != 0 {
		return "", nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: castpin exited %d: %.200s", code, out)
	}
	pin, files, err := castlane.ParseListing(out)
	if err != nil {
		return "", nil, buildlane.CouldNotRun, "could not run: " + err.Error()
	}
	return pin, files, buildlane.Clean, ""
}

// stage pushes the payload's files, by the names they carry in the tree, to
// the staging reference, and answers the pushed manifest's digest. Pushed from
// inside the payload, each layer is titled by its relative path, and oras pull
// lays it back under the same path: the tree mold pins is the tree the lane
// pinned.
func (l *castLane) stage(ctx context.Context, payload *dagger.Directory, ref string, files []string) (string, int, string) {
	tarball, err := fetchTool(ctx, checks.OrasURL)
	if err != nil {
		return "", buildlane.CouldNotRun, fmt.Sprintf("could not run: oras could not be provisioned: %v", err)
	}
	argv := append([]string{"oras", "push", "--registry-config", "/run/docker/config.json",
		"--format", "go-template", "--template", "{{.digest}}", ref}, files...)
	out, code, err := output(ctx, dag.Container().From(checks.ImageFleet).
		WithFile("/tmp/oras.tgz", tarball).
		WithExec([]string{"tar", "-xzf", "/tmp/oras.tgz", "-C", "/usr/local/bin", "oras"}).
		WithMountedSecret("/run/docker/config.json", l.registryConfig).
		WithMountedDirectory("/payload", payload).
		WithWorkdir("/payload").
		WithEnvVariable("CAST_RUN", l.stamp).
		WithExec(argv, anyExit))
	if err != nil {
		return "", buildlane.CouldNotRun, fmt.Sprintf("could not run: the staging push did not run: %v", err)
	}
	if code != 0 {
		c, why := buildlane.ToolFailed("staging push", out)
		return "", c, why
	}
	digest := buildlane.DigestOf(out)
	if digest == "" {
		return "", buildlane.CouldNotRun, fmt.Sprintf("could not run: oras pushed %s and answered no digest: %.200s", ref, out)
	}
	return digest, buildlane.Clean, ""
}

// mint asks hades for forge_mold on the channel, as the calling pod, with the
// staged payload and the commit it was built from.
func (l *castLane) mint(ctx context.Context, c castlane.Cast, payloadRef, pin string) (castlane.Result, int, string) {
	// A map of strings always marshals.
	args, _ := json.Marshal(map[string]string{"name": c.Artifact(), "payload_ref": payloadRef, "source_sha": l.m.Sha})
	out, code, err := output(ctx, hadesCaller(l.spire, l.hades, l.hadesID, l.stamp).
		WithExec([]string{"/usr/local/bin/hadescall", "forge_mold", string(args)}, anyExit))
	if err != nil {
		return castlane.Result{}, buildlane.CouldNotRun, fmt.Sprintf("could not run: could not ask hades: %v", err)
	}
	if code != 0 {
		return castlane.Result{}, buildlane.CouldNotRun, "could not run: could not ask hades: " + out
	}
	status, body, err := buildlane.ParseCall(out)
	if err != nil {
		return castlane.Result{}, buildlane.CouldNotRun, "could not run: " + err.Error()
	}
	castSay("hades answered forge_mold HTTP %d", status)
	return castlane.Minted(status, body, pin)
}

// verify checks the digest mold landed against the repo's own cosign.pub. Key
// only: mold's signature carries no transparency-log entry (foundry-tools
// #61 chose key-only verification), so the check is told not to demand one.
func (l *castLane) verify(ctx context.Context, c castlane.Cast, r castlane.Result) (int, string) {
	ref := bundlelane.RegistryHost + "/app/" + c.Name + "@" + r.Digest
	nonroot := dagger.ContainerWithMountedSecretOpts{Owner: "65532:65532"}
	out, code, err := output(ctx, dag.Container().From(checks.ImageCosign).
		WithMountedTemp("/tmp").
		WithEnvVariable("HOME", "/tmp").
		WithMountedSecret("/run/docker/config.json", l.registryConfig, nonroot).
		WithEnvVariable("DOCKER_CONFIG", "/run/docker").
		WithFile("/run/cosign/cosign.pub", l.m.Source.File("cosign.pub")).
		WithEnvVariable("CAST_RUN", l.stamp).
		WithExec([]string{"verify", "--key", "/run/cosign/cosign.pub", "--insecure-ignore-tlog=true", ref}, entrypointAnyExit))
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: the signature check did not run: %v", err)
	}
	if code != 0 {
		return buildlane.ToolFailed("cosign verify "+ref, out)
	}
	castSay("verified %s against cosign.pub", ref)
	return buildlane.Clean, ""
}

// ring announces the release on hephaestus.releases, best effort, and answers
// what happened for the settle to say. The hosts deliver on a one-minute timer
// regardless, so a bell that does not ring costs a slower deploy and never a
// broken release — and failing the cast on it would report a release that
// happened as one that did not.
func (l *castLane) ring(ctx context.Context, c castlane.Cast, r castlane.Result) string {
	if l.doorbellURL == "" {
		return ""
	}
	out, code, err := output(ctx, dag.Container().From(checks.ImageRust).
		WithEnvVariable("CAST_RUN", l.stamp).
		WithExec([]string{"curl", "-sS", "-m", "15", "-X", "POST",
			"-H", "Content-Type: application/vnd.kafka.json.v2+json",
			"-d", castlane.Doorbell(c, r), l.doorbellURL}, anyExit))
	if err != nil {
		return fmt.Sprintf("; the doorbell did not ring (%v) — the hosts' delivery timer still delivers", err)
	}
	if code != 0 {
		return fmt.Sprintf("; the doorbell did not ring (curl exited %d: %.120s) — the hosts' delivery timer still delivers", code, out)
	}
	return "; the doorbell rang"
}
