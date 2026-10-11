package main

import (
	"context"
	"encoding/json"
	"errors"
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
// THE TREE NAMES WHAT SHIPS. The binaries are every bin target the tree
// builds (cargo metadata's default members, or every main under cmd/), the
// payload is the tree's payload/ directory, and the channel is
// app/<meta.name>:stable for every binary repo. The record supplies the name
// and nothing else; it used to carry the binaries and the extras
// (tools.cast, foundry-dies#226), a second copy of what the tree says.
//
// HEPHAESTUS CASTS AND SIGNS, ON PURPOSE (Scheduler Redistribution Part II,
// D12). The lane stages the payload unsigned under {registry}/staging/, which
// grants nothing, and asks hades for layer_cast with sign=true as the
// calling pod — a bundle is signed because every consumer verifies it, and the
// lane says so rather than inheriting it from a verb. Hephaestus pulls the
// staged tree, re-derives its pin (a mismatch is tamper and refuses),
// allocates the channel's next index and signs. It used to be forge_mold,
// which routed a bundle name into the same mint (D16). Then
// the lane verifies the landed digest against the repo's OWN cosign.pub —
// the step that makes the cast a claim rather than a hope.

// Cast builds a binary repo's release binaries at the commit the module was
// constructed on and casts them as the bundle its record names.
func (m *FoundryTools) Cast(
	ctx context.Context,
	// The SPIRE agent's workload socket, forwarded by the calling pod: the
	// identity layer_cast is asked as. Required unless --dry-run.
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
	// The run's record token (CA_RECORD_TOKEN), which authorises posting this
	// run's record to the door it fetched the tree from. Absent and the lane
	// posts nothing and settles on its exit code, exactly as it did before.
	//
	// A SECRET, NOT A STRING, for the reason GateFile's own token states at
	// length: dagger echoes call arguments verbatim into plain-progress
	// narration (dagger/dagger#14363), so a token passed as a string would be
	// printed into the pod log and shipped to Loki. A Secret is masked.
	// +optional
	recordToken *dagger.Secret,
) error {
	l := &castLane{
		m: m, spire: spire, registryToken: registryToken, doorbellURL: doorbellURL,
		hades: hades, hadesID: hadesID, dryRun: dryRun,
		stamp:  strconv.FormatInt(time.Now().UnixNano(), 10),
		phases: phases{group: castGroup, order: castPhases},
	}
	code, reason := l.run(ctx)
	// THE RECORD EXPLAINS THE VERDICT; IT DOES NOT DECIDE IT. settle() is
	// unchanged and the exit code is still what the door settles this lane on.
	//
	// THE MARSHAL ERROR IS DROPPED rather than branched on, for the reason
	// GateFile's own comment gives: StageResult is strings, ints and slices, so
	// json.Marshal cannot fail on it and the error arm is unreachable. A branch
	// no test can take is a branch that should not exist.
	record, _ := l.record("cast", code).Record()
	postRecord(ctx, m.Repo, recordToken, record)
	return settle(ctx, code, "cast: "+reason)
}

type castLane struct {
	// phases records what each step of the lane answered. See lanerecord.go.
	phases
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

func castSay(format string, args ...any) { castSayLine(sprintf(format, args...)) }

// castSayLine is the cast lane's one writer to stderr, for the reason build's
// own says at length: a lane formats once and then decides who gets the line —
// the pod log a person tails, and the record the door folds into a verdict.
func castSayLine(line string) { fmt.Fprintln(os.Stderr, "cast: "+line) }

func (l *castLane) run(ctx context.Context) (int, string) {
	m := l.m
	if m.Repo == "" || m.Sha == "" {
		return l.stop("cast:preflight", buildlane.CouldNotRun, "could not run: the cast lane casts a commit the engine fetched — construct the module with --repo and --sha")
	}
	if !l.dryRun && (l.spire == nil || l.registryToken == nil) {
		return l.stop("cast:preflight", buildlane.CouldNotRun, "could not run: a cast stages, mints and verifies: --spire and --registry-token are both required (--dry-run needs neither)")
	}
	l.seal("cast:preflight", buildlane.Clean, "the lane has its commit and its credentials")
	star := starOf(m.Repo)
	shard := "fleet/stars/" + star + "/slag.json"
	slag, err := dag.Git(checks.DiesRepo).Ref(checks.DiesRef).Tree().File(shard).Contents(ctx)
	if err != nil || strings.TrimSpace(slag) == "" {
		return l.stop("cast:record", buildlane.CouldNotRun, fmt.Sprintf("could not run: no record for %s could be read at foundry-dies %s, so nothing says what it ships (%v)", star, shard, err))
	}
	c, err := castlane.FromRecord(slag)
	if err != nil {
		return l.stop("cast:record", buildlane.Findings, "findings: "+err.Error())
	}
	l.seal("cast:record", buildlane.Clean, "foundry-dies holds a record for "+star+"; its binaries and payload are the tree's")
	mode := ""
	if l.dryRun {
		mode = " — dry run: the build and the pin run for real; nothing is staged, minted or verified"
	}
	castSay("%s at %.12s%s", c.Artifact(), m.Sha, mode)

	if _, ok, err := fileIn(ctx, m.Source, "cosign.pub"); err != nil {
		return l.stop("cast:cosign", buildlane.CouldNotRun, fmt.Sprintf("could not run: the tree could not be read for cosign.pub: %v", err))
	} else if !ok {
		return l.stop("cast:cosign", buildlane.Findings, "findings: this repo carries no cosign.pub, so a landed digest could not be verified — nothing was built")
	}
	l.seal("cast:cosign", buildlane.Clean, "the tree carries cosign.pub, so a landed digest can be verified")
	payload, binaries, code, why := l.payload(ctx, c)
	if code != buildlane.Clean {
		return l.stop("cast:payload", code, why)
	}
	l.say("binaries from the tree: %s", strings.Join(binaries, ", "))
	l.seal("cast:payload", buildlane.Clean, "built and assembled what "+c.Artifact()+" ships: "+strings.Join(binaries, ", "))
	pin, files, code, why := l.pin(ctx, payload)
	if code != buildlane.Clean {
		return l.stop("cast:pin", code, why)
	}
	l.say("payload pins to %s over %d file(s): %s", pin, len(files), strings.Join(files, ", "))
	l.seal("cast:pin", buildlane.Clean, fmt.Sprintf("%d file(s) pin to %s", len(files), pin))
	if l.dryRun {
		// A DRY RUN STOPS HERE, CLEAN, and the stage, the mint and the
		// signature are UNREACHED rather than passed. Reporting them as held
		// would claim the one thing this mode deliberately does not do.
		return l.stop("", buildlane.Clean, fmt.Sprintf("clean: dry run — %d file(s) pin to %s for %s; nothing was staged, minted or verified", len(files), pin, c.Artifact()))
	}

	token, err := l.registryToken.Plaintext(ctx)
	if err != nil {
		return l.stop("cast:stage", buildlane.CouldNotRun, fmt.Sprintf("could not run: the registry token did not read: %v", err))
	}
	l.registryConfig = dag.SetSecret("cast-registry-config", bundlelane.DockerConfig(bundlelane.RegistryHost, bundlelane.RegistryUser, strings.TrimSpace(token)))

	ref := c.Stage(bundlelane.RegistryHost, pin)
	digest, code, why := l.stage(ctx, payload, ref, files)
	if code != buildlane.Clean {
		return l.stop("cast:stage", code, why)
	}
	l.say("staged %s@%s", ref, digest)
	l.seal("cast:stage", buildlane.Clean, "staged "+ref+"@"+digest)
	r, code, why := l.mint(ctx, c, ref+"@"+digest, pin)
	if code != buildlane.Clean {
		return l.stop("cast:mint", code, why)
	}
	l.say("hephaestus minted %s at index %d (%s, %s)", r.Channel, r.Index, r.Pin, r.Digest)
	l.seal("cast:mint", buildlane.Clean, fmt.Sprintf("hephaestus minted %s at index %d (%s, %s)", r.Channel, r.Index, r.Pin, r.Digest))
	if code, why := l.verify(ctx, c, r); code != buildlane.Clean {
		return l.stop("cast:verify", code, why)
	}
	l.seal("cast:verify", buildlane.Clean, "verified "+r.Digest+" against cosign.pub")
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
	// THE BELL CANNOT FAIL THE CAST — ring answers a string and never a code,
	// because the hosts' delivery timer delivers whether or not the doorbell
	// answered. It seals CLEAN with whatever it reported, so the fact is on
	// the record without voting on the verdict.
	bell := l.ring(ctx, c, r)
	l.seal("cast:ring", buildlane.Clean, strings.TrimPrefix(strings.TrimSpace(bell), "; "))
	noop := ""
	if r.NoOp {
		noop = " (the channel's head already carried this pin; hephaestus re-signed it and allocated no index)"
	}
	// 0 is buildlane.Clean, spelled as the literal: the constant's name in a
	// return slot is a RETURN_ZERO mutant that rewrites it to itself.
	return 0, fmt.Sprintf("clean: cast %s at index %d (%s, %s) — staged, minted and signed by hephaestus, verified against cosign.pub%s%s", c.Artifact(), r.Index, r.Pin, r.Digest, noop, bell)
}

// payload builds the release binaries and assembles what ships: each binary
// at the payload's root, and the repo's payload/ directory verbatim beside
// them. A payload/x lands as x and a payload/d/ as d/. A name claimed twice is
// refused rather than shipped as whichever was written last.
//
// A tree with no payload/ ships its binaries alone.
// 0 is buildlane.Clean, spelled as the literal in the success returns below:
// the constant's name in a return slot is a RETURN_ZERO mutant that rewrites it
// to itself.
func (l *castLane) payload(ctx context.Context, c castlane.Cast) (*dagger.Directory, []string, int, string) {
	built, code, why := l.release(ctx)
	if code != buildlane.Clean {
		return nil, nil, code, why
	}
	havePayload, err := l.m.Source.Exists(ctx, castlane.PayloadDir, dagger.DirectoryExistsOpts{ExpectedType: dagger.ExistsTypeDirectoryType})
	if err != nil {
		return nil, nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: the tree could not be read for %s/: %v", castlane.PayloadDir, err)
	}
	var top []string
	if havePayload {
		entries, err := l.m.Source.Directory(castlane.PayloadDir).Entries(ctx)
		if err != nil {
			return nil, nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: %s/ could not be listed: %v", castlane.PayloadDir, err)
		}
		for _, e := range entries {
			top = append(top, strings.TrimSuffix(e, "/"))
		}
	}
	if err := castlane.Claims(built.binaries, top); err != nil {
		return nil, nil, buildlane.Findings, "findings: " + err.Error()
	}
	payload := dag.Directory()
	for _, b := range built.binaries {
		f := built.ctr.File(path.Join(built.dir, b))
		if _, err := f.Size(ctx); err != nil {
			return nil, nil, buildlane.Findings, fmt.Sprintf("findings: the release build left no %q in %s", b, built.dir)
		}
		payload = payload.WithFile(b, f)
	}
	if havePayload {
		return payload.WithDirectory(".", l.m.Source.Directory(castlane.PayloadDir)), built.binaries, 0, ""
	}
	return payload, built.binaries, 0, ""
}

// builtRelease is a cast's compile: the container holding the binaries, the
// directory they were written to, and their names.
type builtRelease struct {
	ctr      *dagger.Container
	dir      string
	binaries []string
}

// release compiles every binary the tree builds in the lane the tree
// declares and answers them.
//
// THE BINARIES ARE THE TREE'S: every bin target cargo says the workspace's
// default members build, or every main package under cmd/. A binary repo's
// binaries are its product (an image's are an implementation detail, which is
// why the build lane reads the Dockerfile instead). Nothing is declared.
//
// THE LANE IS A FACT ABOUT THE TREE — never the record, never the copier
// template — which is the rule every atom in this module already follows
// (checks/lane.go says why). Rust is a Cargo.toml at the root; Go is a go.mod
// at the root, read through the same module walk run.plan uses. A tree that
// declares neither is refused BY NAME: the lane used to fall through to cargo
// unconditionally, which is how argus, a Go repo, spent a week settling `could
// not find Cargo.toml` on every landing (foundry-tools #9839). A tree declaring
// both is refused too: two toolchains that each build a `tongs` would ship
// whichever ran last.
func (l *castLane) release(ctx context.Context) (builtRelease, int, string) {
	r := newRun(l.m.Source, l.m.Repo, "")
	entries, err := l.m.Source.Entries(ctx)
	if err != nil {
		return builtRelease{}, buildlane.CouldNotRun, fmt.Sprintf("could not run: the repository root could not be read: %v", err)
	}
	mods, err := r.goModuleDirs(ctx)
	if err != nil {
		return builtRelease{}, buildlane.CouldNotRun, fmt.Sprintf("could not run: the tree's Go modules could not be enumerated: %v", err)
	}
	rust := checks.DeclaresLane(entries, checks.LaneRust)
	goRoot := slices.Contains(mods, ".")
	switch {
	case rust && goRoot:
		return builtRelease{}, buildlane.Findings, "findings: the tree declares both a Cargo.toml and a root go.mod, so the cast cannot tell which toolchain builds its binaries"
	case rust:
		return l.cargoRelease(ctx, r)
	case goRoot:
		return l.goRelease(ctx, r, slices.Contains(entries, "vendor/"))
	}
	return builtRelease{}, buildlane.Findings, "findings: the tree is cast as a binary repo and declares no lane that builds one — no Cargo.toml and no go.mod at the root — so nothing can compile its binaries"
}

// cargoRelease is the rust lane's release build: the binaries are the bin
// targets cargo metadata names on the workspace's default members, and one
// `cargo build --release` over the workspace builds them, read back out of
// target/release.
func (l *castLane) cargoRelease(ctx context.Context, r *run) (builtRelease, int, string) {
	// THE TARGET DIRECTORY IS THE REPOSITORY'S RELEASE VOLUME, not the gate's
	// debug cargo-target and not the container's filesystem. It was the
	// container's, because a file in a cache mount is not in the container and
	// the binaries could never be read back out of it — and so every cast
	// compiled every crate (cerberus: 234 crates, 0 Fresh, 470-560 CPU-s, ~8
	// casts a day; checks.ReleaseCacheFor). Now the build copies its binaries to
	// checks.ReleaseOut IN THE SAME EXEC (internal/copyout says why it cannot be
	// a second one) and the cast reads them there.
	//
	// UNDER THE UNSTALE STAMP, as the gate's cached target is: every tree of
	// the repo mounts at /src, so without it cargo would call the workspace's
	// crates Fresh and link what an older commit built. The stamp rebuilds them;
	// the registry crates stay built.
	lane := r.withReleaseCache(r.lane(checks.ImageRust)).
		WithEnvVariable("CARGO_TARGET_DIR", checks.RustReleaseTarget).
		WithExec(checks.Unstale())
	// --no-deps and --locked: the workspace's own members only, against the
	// committed lock. cargo answers on stdout, so stderr's progress chatter is
	// kept out of the JSON by reading stdout alone.
	meta := lane.WithExec([]string{"cargo", "metadata", "--no-deps", "--format-version", "1", "--locked"}, anyExit)
	out, code, err := output(ctx, meta)
	if err != nil {
		return builtRelease{}, buildlane.CouldNotRun, fmt.Sprintf("could not run: cargo metadata did not run: %v", err)
	}
	if code != 0 {
		cls, why := buildlane.ToolFailed("cargo metadata", out)
		return builtRelease{}, cls, why
	}
	binaries, err := castlane.BinariesFromCargoMetadata(out)
	if err != nil {
		return builtRelease{}, buildlane.Findings, "findings: " + err.Error()
	}
	var carried []string
	for _, b := range binaries {
		carried = append(carried, path.Join(checks.RustReleaseTarget, "release", b)+"="+path.Join(checks.ReleaseOut, b))
	}
	built := lane.WithExec(copiedOut([]string{"cargo", "build", "--release", "--locked"}, carried...), anyExit)
	out, code, err = output(ctx, built)
	if err != nil {
		return builtRelease{}, buildlane.CouldNotRun, fmt.Sprintf("could not run: the release build did not run: %v", err)
	}
	if code != 0 {
		cls, why := buildlane.ToolFailed("cargo build", out)
		return builtRelease{}, cls, why
	}
	return builtRelease{ctr: built, dir: checks.ReleaseOut, binaries: binaries}, 0, ""
}

// goRelease is the go lane's release build: the binaries are every main
// package under cmd/ (`go list -find`, which resolves no dependencies), one
// exec each from ./cmd/<name>, with the flag set every Go image in the fleet
// is built with (checks.ReleaseFlags — measured identical across all 29, no
// exceptions) and -mod=vendor exactly when the module vendors, into
// checks.ReleaseOut. A binary repo ships all of cmd/: its binaries are its
// product, where an image's are only what its Dockerfile copies.
//
// THE EXIT IS READ AFTER EACH EXEC, not once at the end. The chain runs on
// under ReturnTypeAny, so with one read a first binary that failed to compile
// would be reported by a second that did not, and the finding would arrive
// later as "left no <bin>" with the compiler's own words gone.
func (l *castLane) goRelease(ctx context.Context, r *run, vendored bool) (builtRelease, int, string) {
	ctr := r.goModules(".").WithEnvVariable("CGO_ENABLED", "0")
	list := ctr.WithExec([]string{"go", "list", "-find", "-f", "{{.Name}} {{.ImportPath}}", "./cmd/..."}, anyExit)
	listed, code, err := output(ctx, list)
	if err != nil {
		return builtRelease{}, buildlane.CouldNotRun, fmt.Sprintf("could not run: go list did not run: %v", err)
	}
	if code != 0 {
		cls, why := buildlane.ToolFailed("go list ./cmd/...", listed)
		return builtRelease{}, cls, why
	}
	binaries, err := castlane.BinariesFromGoList(listed)
	if err != nil {
		return builtRelease{}, buildlane.Findings, "findings: " + err.Error()
	}
	for _, name := range binaries {
		b := checks.ReleaseBinary{Name: name, Package: "./cmd/" + name}
		ctr = ctr.WithExec(checks.GoReleaseArgs(b, vendored), anyExit)
		out, code, err := output(ctx, ctr)
		if err != nil {
			return builtRelease{}, buildlane.CouldNotRun, fmt.Sprintf("could not run: the release build of %s did not run: %v", b.Package, err)
		}
		if code != 0 {
			cls, why := buildlane.ToolFailed("go build "+b.Package, out)
			return builtRelease{}, cls, why
		}
	}
	return builtRelease{ctr: ctr, dir: checks.ReleaseOut, binaries: binaries}, 0, ""
}

// pin runs castpin, built from this module's own source, over the payload:
// the content pin mold re-derives, and the files in the order they are pushed.
// It runs in a container so the binaries' bytes never cross the engine's API.
func (l *castLane) pin(ctx context.Context, payload *dagger.Directory) (string, []string, int, string) {
	out, code, err := output(ctx, dag.Container().From(checks.ImageStatic).
		WithFile("/usr/local/bin/castpin", helperBinary("castpin")).
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

// mint asks hades for castlane.Verb on the channel with sign=true, as the
// calling pod, with the staged payload and the commit it was built from. Any
// answer, failure included, is settled as it came.
func (l *castLane) mint(ctx context.Context, c castlane.Cast, payloadRef, pin string) (castlane.Result, int, string) {
	// Strings and a bool always marshal.
	args, _ := json.Marshal(map[string]any{"ref": c.Artifact(), "sign": true, "payload_ref": payloadRef, "source_sha": l.m.Sha})
	status, body, err := l.ask(ctx, castlane.Verb, string(args))
	if err != nil {
		return castlane.Result{}, buildlane.CouldNotRun, err.Error()
	}
	castSay("hades answered %s HTTP %d", castlane.Verb, status)
	return castlane.Minted(status, body, pin)
}

// ask execs hadescall for one verb and answers hades's status and body; an
// error is a could-not-run, already worded as one.
func (l *castLane) ask(ctx context.Context, verb, args string) (int, string, error) {
	out, code, err := output(ctx, hadesCaller(l.spire, l.hades, l.hadesID, l.stamp).
		WithExec([]string{"/usr/local/bin/hadescall", verb, args}, anyExit))
	if err != nil {
		return 0, "", fmt.Errorf("could not run: could not ask hades: %v", err)
	}
	if code != 0 {
		return 0, "", errors.New("could not run: could not ask hades: " + out)
	}
	status, body, err := buildlane.ParseCall(out)
	if err != nil {
		return 0, "", errors.New("could not run: " + err.Error())
	}
	return status, body, nil
}

// verify checks the digest hephaestus landed against the repo's own cosign.pub.
// Key only: hephaestus's signature carries no transparency-log entry (foundry-tools
// #61 chose key-only verification), so the check is told not to demand one.
func (l *castLane) verify(ctx context.Context, c castlane.Cast, r castlane.Result) (int, string) {
	ref := c.Repo(bundlelane.RegistryHost) + "@" + r.Digest
	nonroot := dagger.ContainerWithMountedSecretOpts{Owner: "65532:65532"}
	out, code, err := output(ctx, cosignIn().
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
