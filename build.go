package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/pins"
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
// THE LANE ASKS FOR NO PERMIT (Scheduler Redistribution Part II, Phase 13).
// It used to end a tip by calling hades's forge_mold as the calling pod, which
// verified CI's signature, re-signed and stamped :stable. Nothing reads a
// star's :stable now — Flux's image automation rolls a star from its stamp tag,
// and the stand-down compares against that stamp (detect) — so a published,
// scanned, signed tip IS the release. --spire, --hades and --hades-id are
// still accepted, because every lane Job passes them, and are unused.

// Build builds the commit the module was constructed on and settles the
// build lane. A pull builds the image and publishes nothing; a tip publishes
// it under the g-pin and the stamp tag Flux reads (publishTip) and attaches
// its SBOM, UNSIGNED (D11: nothing verifies a star image since the permit
// retired); a base is also signed (build_bases.go). A commit whose every change since the last published tip (its
// newest stamp tag) is inert builds nothing.
func (m *FoundryTools) Build(
	ctx context.Context,
	// The default branch's tip: publish and attach the SBOM (a base is also
	// signed). Without it
	// the lane settles on the build alone.
	// +optional
	tip bool,
	// The SPIRE agent's workload socket, forwarded by the calling pod. UNUSED
	// since the permit retired (Phase 13); accepted so no lane Job's call
	// breaks.
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
	// Build even when the last published tip already carries this commit's source — the
	// PERIODIC RESCAN path. The stand-down asks whether the SOURCE changed,
	// which is the right question for a landing and the wrong one for a
	// rebuild: the python and bun bases run `apt-get update && apt-get
	// upgrade`, so the same tree built a week later picks up every Debian
	// security update published since. Nothing else is skipped — the scan,
	// the signature and the tag move exactly as on any other build.
	// +optional
	force bool,
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
	l := &buildLane{
		m: m, tip: tip, force: force,
		registryAuth: registryAuth, cosignKey: cosignKey, cosignPassphrase: cosignPassphrase,
		indexURL: indexURL, registry: registry, sourceBase: sourceBase,
		stamp:  strconv.FormatInt(time.Now().UnixNano(), 10),
		phases: phases{group: buildGroup, order: buildPhases},
	}
	code, reason := l.run(ctx)
	// THE RECORD EXPLAINS THE VERDICT; IT DOES NOT DECIDE IT. settle() below is
	// unchanged and the exit code is still what the door settles this lane on —
	// the record is additive until `build` is named in ourea's record_lanes,
	// which is a separate landing with its own evidence. Naming it there before
	// records are observed arriving would settle every build in the fleet
	// could-not-run at once.
	//
	// THE POST CANNOT FAIL THE RUN. postRecord answers nothing and swallows
	// everything, exactly as it does for the gate: a lane's verdict must not
	// turn on whether an HTTP request succeeded.
	//
	// AND THE MARSHAL ERROR IS DROPPED RATHER THAN BRANCHED ON, for the reason
	// GateFile's own comment gives: Record() fails only if json.Marshal does,
	// and StageResult is strings, ints and slices of the same, so the error arm
	// is unreachable. `err == nil` survived negation here exactly as it did
	// there (build.go:123 LIVED, 2026-09-28). A branch no test can take is a
	// branch that should not exist; an empty record is refused downstream by
	// sendRecord, so the impossible case is still handled.
	record, _ := l.record("build", code).Record()
	postRecord(ctx, m.Repo, recordToken, record)
	return settle(ctx, code, "build: "+reason)
}

type buildLane struct {
	m   *FoundryTools
	tip bool
	// force skips the stand-down: the tree is built whether or not the last
	// published tip already carries its source. See Build's own doc.
	force                                     bool
	registryAuth, cosignKey, cosignPassphrase *dagger.Secret
	indexURL, registry, sourceBase            string
	// stamp is this run's, on every step that must happen again on a rerun of
	// the same commit: signing and attesting are acts, not results.
	stamp string
	// phases records what each step of the lane answered. See lanerecord.go.
	phases
	// published accumulates what this run pushed, declared ONCE at the end of
	// the lane rather than at each push. See declarePins.
	published []pins.Pin
	// consumed is what this tree DEPENDS ON — the internal-registry digests its
	// Dockerfiles name. Read once at the top of the run, so a stand-down or a
	// could-not-run still declares them: a dependency is a fact about the TREE
	// and does not need the build to have succeeded.
	consumed []pins.Pin
}

func say(format string, args ...any) { sayLine(sprintf(format, args...)) }

// sayLine is the build lane's one writer to stderr. ONE WRITER, TWO READERS:
// the pod log a person tails while the lane runs, and the record the door folds
// into a verdict. A second formatting path would let the two drift, and the log
// is the evidence for the record.
func sayLine(line string) { fmt.Fprintln(os.Stderr, "build: "+line) }

// sprintf is fmt.Sprintf under a name that says why it is here: a lane formats
// ONCE and then decides who gets the line.
func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// starOf is the star a repository URL or custody key names:
// http://ourea…:8215/rob/ares.git and rob/ares are both ares.
func starOf(repo string) string { return path.Base(strings.TrimSuffix(repo, ".git")) }

// withheld answers whether a tip was handed less than it publishes and signs
// with: every secret.
func withheld(secrets ...*dagger.Secret) bool {
	for _, s := range secrets {
		if s == nil {
			return true
		}
	}
	return false
}

func (l *buildLane) run(ctx context.Context) (int, string) {
	// LAST, WHATEVER HAPPENS. Deferred here rather than called at the end so
	// every return — a stand-down, a could-not-run, a findings verdict after
	// something was already pushed — still declares what this run published.
	defer l.declarePins()
	m := l.m
	if m.Repo == "" || m.Sha == "" {
		return l.stop("build:preflight", buildlane.CouldNotRun, "the build lane builds a commit the engine fetched — construct the module with --repo and --sha")
	}
	if l.tip && withheld(l.registryAuth, l.cosignKey, l.cosignPassphrase) {
		return l.stop("build:preflight", buildlane.CouldNotRun, "a tip build publishes and signs: --registry-auth, --cosign-key and --cosign-passphrase are all required")
	}
	star := starOf(m.Repo)
	l.say("%s for %s at %.12s", map[bool]string{true: "tip build", false: "pull-time build (publishes nothing)"}[l.tip], star, m.Sha)
	l.seal("build:preflight", buildlane.Clean, "the lane has its commit and its credentials")

	// WHAT THIS TREE DEPENDS ON, read before anything can return. The reaper
	// keeps `inuse-<digest12>` and nothing else durably — every build tag is
	// pushedWithin 24h plus mostRecentlyPushedCount 2, and deleteUntagged is
	// true on a 24h delay — so a base survives only while a movable tag still
	// points at it. Recording only what we BUILT keeps the star's own image and
	// lets the base under it be reaped, which is incident #25: 24 Go stars red
	// at once on stellar_core:go-runtime@9649a761, gone from the registry.
	//
	// IT CANNOT FAIL THE BUILD. A tree whose Dockerfiles could not be read is
	// said on stderr and the lane carries on with an empty dependency set — a
	// build that works is not held up by bookkeeping about it.
	if deps, err := l.dependencies(ctx); err != nil {
		// NOT A FAILURE, and the record has to say so as plainly as the code
		// does: this phase is bookkeeping about the build, and a build that
		// works is not held up by it. It seals CLEAN with the fault as its
		// reason, so the fact is on the record without voting on the verdict.
		l.say("the tree's dependencies could not be read, so this build declares none: %v", err)
		l.seal("build:dependencies", buildlane.Clean, "declared none: the tree's dependencies could not be read")
	} else {
		l.consumed = deps
		l.seal("build:dependencies", buildlane.Clean, fmt.Sprintf("declared %d", len(deps)))
	}

	bases, err := l.bases(ctx)
	if err != nil {
		return l.stop("build:detect", buildlane.CouldNotRun, fmt.Sprintf("could not run: the tree's bases could not be read: %v", err))
	}
	if len(bases) > 0 {
		return l.runBases(ctx, star, bases)
	}

	compose := ""
	for _, f := range buildlane.ComposeFiles {
		body, ok, err := fileIn(ctx, m.Source, f)
		if err != nil {
			return l.stop("build:detect", buildlane.CouldNotRun, fmt.Sprintf("could not read %s: %v", f, err))
		}
		if ok {
			compose = body
			break
		}
	}
	pushRepo := buildlane.PushRepo(l.registry, buildlane.DeclaredImage(compose, star))

	needed, why, failed := l.detect(ctx, pushRepo)
	if failed != nil {
		l.say("%s", failed.why)
		return l.stop("build:detect", failed.code, failed.why)
	}
	l.say("%s", why)
	if !needed {
		// A STAND-DOWN IS A CLEAN RUN THAT BUILT NOTHING, and the phases after
		// it never ran rather than passing — which is exactly what Unreached
		// is for, and why this returns through stop() like a failure does.
		return l.stop("build:detect", buildlane.Clean, "stood down: "+why)
	}
	l.seal("build:detect", buildlane.Clean, why)

	args, err := l.buildArgs(ctx)
	if err != nil {
		return l.stop("build:image", buildlane.CouldNotRun, err.Error())
	}
	img, err := m.Image(ctx, m.Sha, l.sourceBase+"/"+star, star, args, "")
	if err != nil {
		return l.stop("build:image", buildlane.CouldNotRun, err.Error())
	}
	if code, why := l.stageRelease(ctx, img); code != buildlane.Clean {
		return l.stop("build:release", code, why)
	}
	l.seal("build:release", buildlane.Clean, "")
	// The build is forced here, on both paths, so a Dockerfile that does not
	// build is classified as a build (findings, or could-not-run on a network
	// fault) — not as a scan that could not run, which is what the tarball
	// read below would have made of it.
	if _, err := img.Container().Sync(ctx); err != nil {
		code, why := buildlane.Failed("the image build", err.Error())
		return l.stop("build:image", code, why)
	}
	l.seal("build:image", buildlane.Clean, "built "+star+" at "+shortSha(m.Sha))

	// NOTHING IS PUBLISHED THAT THE SCAN DID NOT PASS (CA master-plan F14).
	// Verify's trivy atom runs here on the image the engine just built — the
	// same chain the publish pushes — and a fixable HIGH or CRITICAL in the
	// star's OWN layer settles the lane as findings before any push: the
	// registry never holds it, so nothing downstream has to be told not to
	// deploy it. The base's findings are named and not counted (they are the
	// bases lane's), and a scan that could not run is could-not-run, never a
	// pass. A pull reports the same scan, so the finding is on the pull, not
	// on the landing. What Verify records — the SBOM — Build attaches at
	// attest time, below; the standalone Verify stage stays for the door's
	// own run of the chain (F16).
	if code, why := l.verify(ctx, img); code != buildlane.Clean {
		return l.stop("build:verify", code, why)
	}
	l.seal("build:verify", buildlane.Clean, "no fixable HIGH or CRITICAL in the star's own layer")

	if !l.tip {
		// A PULL ENDS HERE, CLEAN, and publish/sign are UNREACHED rather
		// than passed. The landing does them; saying they held would claim this
		// run proved something it never ran.
		return l.stop("", buildlane.Clean, fmt.Sprintf("clean: built %s at %.12s and its scan passed — nothing published or signed; the landing does that", star, m.Sha))
	}

	ref, stamp, failed := l.publishTip(ctx, img, pushRepo, star)
	if failed != nil {
		return l.stop("build:publish", failed.code, failed.why)
	}
	l.seal("build:publish", buildlane.Clean, "pushed "+ref+" as "+stamp)
	// UNSIGNED (D11): the SBOM is attached; no CI signature, no signed pointer.
	if failed := l.publishSBOM(ctx, img, ref); failed != nil {
		return l.stop("build:sbom", failed.code, failed.why)
	}
	// The last phase seals with the run's own verdict: what it says IS the
	// lane's answer, so the two can never read differently.
	return l.stop("build:sbom", buildlane.Clean, fmt.Sprintf("clean: published %s as %s with its SBOM, unsigned — Flux's image automation rolls the star from that tag", ref, stamp))
}

// stageRelease hands the image the artifact the Gate compiled, when its
// Dockerfile asks for it (CA master-plan F14/F17: "Build - Copy the binary
// from the previous complex run"; FROM base + COPY). A Dockerfile that copies
// from release/ gets the Release directory — the go:release atom's own exec,
// so the engine answers it from the cache the push paid for — staged at
// that path in the build context, and compiles nothing itself. A Dockerfile
// that still carries its own build stage copies nothing from release/ and is
// built exactly as before, which is what lets the fleet flip one star at a
// time (buildlane.CopiesRelease is the contract).
//
// A release that cannot be built is a build that failed: findings when the
// compiler said no, could-not-run on a fault — the same classification the
// Dockerfile's own build stage got, because it is the same compile.
func (l *buildLane) stageRelease(ctx context.Context, img *Image) (int, string) {
	body, _, err := fileIn(ctx, l.m.Source, img.dockerfilePath())
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: the Dockerfile could not be read: %v", err)
	}
	if !buildlane.CopiesRelease(body) {
		return buildlane.Clean, ""
	}
	release, err := l.m.Release(ctx)
	if err != nil {
		// The error IS the release build's log: say it, and carry its last
		// line in the verdict, or the door's reason reads "read its log
		// above" over a log that says nothing (calliope #46, 2026-09-19).
		say("release: %v", err)
		code, why := buildlane.Failed("the release build", err.Error())
		return code, why + ": " + lastLine(err.Error())
	}
	img.Source = l.m.Source.WithDirectory(buildlane.ReleaseDir, release)
	say("release: the Dockerfile copies from %s/ — the Gate's artifact is staged there; the image compiles nothing", buildlane.ReleaseDir)
	return buildlane.Clean, ""
}

// verify is the verify stage's scan, as a step of the build lane: the image's
// fixable HIGH and CRITICAL findings less its base's. Its reason carries the
// stage's own prefix so the door's log reads the same whichever lane ran it.
func (l *buildLane) verify(ctx context.Context, img *Image) (int, string) {
	v := &verifyLane{m: l.m, indexURL: l.indexURL, sourceBase: l.sourceBase, stamp: l.stamp}
	code, why := v.scan(ctx, img)
	say("%s", why)
	if code == buildlane.Findings {
		why += "\nnothing published: fix the star's own layer; the base's findings are its lane's"
	}
	return code, "verify: " + why
}

// detect answers whether this commit needs building. The question is whether
// the star's last PUBLISHED tip already carries its source, not whether this
// push changed any. The last published tip is the newest stamp tag on the push
// repository (buildlane.NewestStamp) — what Flux's ImagePolicy rolls the star
// to — and the image under it names the commit it was built from. So the
// change set is taken since THAT commit: every change inert stands down,
// anything else builds.
//
// NOT :stable. :stable was the permit's output (hephaestus' mold stamped it),
// and once the permit retires nothing moves a star's :stable: a change set
// taken since it would grow with every landing until a README rebuilt and
// rolled the star. The stamp tag moves on every tip that publishes and on
// nothing else, which is exactly "the last build", and zot keeps the two most
// recently pushed stamps of every repository (flux foundry/zot-config.json),
// so the newest survives however long a star sits still.
//
// THE PARENT WAS THE WRONG BEFORE-REF, measured 2026-09-14 on athena. a446514
// changed source and failed at sign, so nothing was permitted; 570be49,
// quickstart.md alone on top of it, diffed inert against its parent and stood
// down, leaving main two landings ahead of :stable with no build coming until
// someone changed source again. build.sh's detect had the same rule.
//
// Anything that leaves the last published source unknown builds: tags that
// do not list, no stamp tag, a stamp whose image names no commit, a published
// commit outside this history.
//
// A HISTORY THAT CANNOT BE READ IS NEITHER ANSWER, and it is the only thing
// that answers a laneStop: success carries no code of its own to get wrong
// (publishTip's convention).
func (l *buildLane) detect(ctx context.Context, pushRepo string) (needed bool, why string, failed *laneStop) {
	tag, why := l.newestStamp(ctx, pushRepo)
	if tag == "" {
		return true, why, nil
	}
	return l.detectWhere(ctx, pushRepo, tag, nil)
}

// newestStamp answers the newest stamp tag on pushRepo, or "" and why there is
// none to compare against. The read is anonymous (the fleet's registry answers
// reads without a login, and a pull carries no credential) and is run fresh on
// every lane: a cached listing would compare against a build that is no longer
// the newest.
func (l *buildLane) newestStamp(ctx context.Context, pushRepo string) (tag, why string) {
	oras, err := orasIn(ctx, nil)
	if err != nil {
		return "", fmt.Sprintf("no published build to compare against: %v — building to be safe", err)
	}
	listed, rc, err := output(ctx, orasRead(oras, l.stamp, "repo", "tags", pushRepo))
	if err != nil || rc != 0 {
		return "", fmt.Sprintf("no published build to compare against: %s's tags did not list (exit %d): %v %.200s — building to be safe", pushRepo, rc, err, listed)
	}
	tag, ok := buildlane.NewestStamp(listed)
	if !ok {
		return "", fmt.Sprintf("no published build to compare against: %s carries no stamp tag — building to be safe", pushRepo)
	}
	return tag, ""
}

// detectWhere is detect against one tag of pushRepo, with the change set
// narrowed first: a base in a repo of several counts only the changes that
// reach it (buildlane.BaseChanges). A star compares against its newest stamp;
// a base against its :stable, which the base lane moves itself.
func (l *buildLane) detectWhere(ctx context.Context, pushRepo, tag string, relevant func(changed string) string) (needed bool, why string, failed *laneStop) {
	label, err := dag.Container().From(pushRepo+":"+tag).Label(ctx, "org.opencontainers.image.revision")
	if err != nil {
		return true, fmt.Sprintf("no published build to compare against: %s:%s did not read (%.200s) — building to be safe", pushRepo, tag, err.Error()), nil
	}
	last := strings.TrimSpace(label)
	if !buildlane.IsCommit(last) {
		return true, fmt.Sprintf("no published build to compare against: %s:%s names no commit (revision %q) — building to be safe", pushRepo, tag, last), nil
	}
	r := newRun(l.m.Source, l.m.Repo, "")
	git := r.gitReady(ctx, r.lane(checks.ImageFleet))
	// ASK WHETHER THIS CHECKOUT CARRIES THE BUILD BEFORE ASKING ABOUT ANCESTRY.
	// A pull branched before the published build's commit does not carry it, and `git
	// merge-base --is-ancestor` on a commit it cannot name exits 128 — and
	// Expect ANY covers exit codes 0-127 and 192-255 only (the SDK's own
	// ReturnTypeAny), so the engine reports 128 as an ERROR, the lane filed it
	// could-not-run, and automerge re-asked forever. Measured 2026-09-15 on
	// terpsichore bf13591, hephaestus e353d2b, nyx 27945a7, hades 3d74db7 and
	// ourea 8e7e113. `rev-parse --verify --quiet` answers the same question
	// with a quiet exit 1.
	_, present, err := output(ctx, git.WithExec([]string{"git", "rev-parse", "--verify", "--quiet", last + "^{commit}"}, anyExit))
	if err != nil {
		return false, "", &laneStop{buildlane.CouldNotRun, fmt.Sprintf("could not run: the history could not be read: %v", err)}
	}
	if present != 0 {
		return true, fmt.Sprintf("the last published build %.12s is not in this checkout (a branch older than that build) — building", last), nil
	}
	_, ancestry, err := output(ctx, git.WithExec([]string{"git", "merge-base", "--is-ancestor", last, "HEAD"}, anyExit))
	if err != nil {
		return false, "", &laneStop{buildlane.CouldNotRun, fmt.Sprintf("could not run: the history could not be read: %v", err)}
	}
	if ancestry != 0 {
		return true, fmt.Sprintf("the last published build %.12s is not in this commit's history (git exit %d) — building", last, ancestry), nil
	}
	changed, rc, err := output(ctx, git.WithExec([]string{"git", "diff", "--name-only", last, "HEAD"}, anyExit))
	if err != nil || rc != 0 {
		return false, "", &laneStop{buildlane.CouldNotRun, fmt.Sprintf("could not run: the change set could not be read (exit %d): %v %s", rc, err, changed)}
	}
	if relevant != nil {
		changed = relevant(changed)
	}
	needed, why = buildlane.Standing(last, tag, changed)
	if !needed && l.force {
		return true, "forced: " + why + " — building anyway, because a rebuild of the same tree is not the same image (the apt layers move under it)", nil
	}
	return needed, why, nil
}

// publish pushes the image under the g-pin and answers `<repo>@<digest>`.
func (l *buildLane) publish(ctx context.Context, img *Image, pushRepo, star string) (string, int, string) {
	gpin, err := buildlane.GPin(pushRepo, l.m.Sha)
	if err != nil {
		return "", buildlane.CouldNotRun, err.Error()
	}
	return l.push(ctx, img, pushRepo, gpin, star)
}

// publishTip is publish for a star's tip: the g-pin, and beside it the stamp
// tag Flux's ImagePolicy reads (D7, buildlane.StampTag). The commit time is
// read BEFORE anything is pushed, so a history that cannot be read leaves the
// registry as it was rather than holding a g-pin no policy can see. Bases do
// not come through here: no ImagePolicy follows a base.
//
// THE RETENTION IS THE REAPER'S, NOT THIS LANE'S. zot keeps a stamp tag the
// way it keeps a g-pin — 24h, or the two most recently pushed (flux
// infrastructure/zot-config.json) — which is the current build and its
// rollback. A revert a week later does not need the TAG: the manifest it
// restores pins the digest, and forge-inuse holds the digest a pin named
// before its last change as inuse-<digest12>.
func (l *buildLane) publishTip(ctx context.Context, img *Image, pushRepo, star string) (ref, stamp string, failed *laneStop) {
	r := newRun(l.m.Source, l.m.Repo, "")
	committed, rc, err := output(ctx, r.gitReady(ctx, r.lane(checks.ImageFleet)).WithExec([]string{"git", "log", "-1", "--format=%ct", "HEAD"}, anyExit))
	if err != nil || rc != 0 {
		return "", "", &laneStop{buildlane.CouldNotRun, fmt.Sprintf("could not run: the commit time could not be read (exit %d): %v %.200s", rc, err, committed)}
	}
	stamp, err = buildlane.StampTag(pushRepo, l.m.Sha, committed)
	if err != nil {
		return "", "", &laneStop{buildlane.CouldNotRun, "could not run: " + err.Error()}
	}
	ref, code, why := l.publish(ctx, img, pushRepo, star)
	if code != buildlane.Clean {
		return "", "", &laneStop{code, why}
	}
	if _, code, why := l.push(ctx, img, pushRepo, stamp, star); code != buildlane.Clean {
		return "", "", &laneStop{code, why}
	}
	return ref, stamp, nil
}

// laneStop is a step's verdict when it stops the lane: the code and the
// reason stop() records. A step that answers one only on failure returns nil
// for success, so success carries no code of its own to get wrong.
type laneStop struct {
	code int
	why  string
}

// push pushes the image under target, a tag of pushRepo, and answers
// `<repo>@<digest>`.
func (l *buildLane) push(ctx context.Context, img *Image, pushRepo, target, star string) (string, int, string) {
	host, _, _ := strings.Cut(pushRepo, "/")
	auth, err := l.registryAuth.Plaintext(ctx)
	if err != nil {
		return "", buildlane.CouldNotRun, fmt.Sprintf("could not run: the registry credential did not read: %v", err)
	}
	user, password, err := buildlane.RegistryLogin(auth, host)
	if err != nil {
		return "", buildlane.CouldNotRun, "could not run: " + err.Error()
	}
	out, err := img.Publish(ctx, target, user, dag.SetSecret("build-registry-password-"+star, password))
	if err != nil {
		code, why := buildlane.Failed("publish", err.Error())
		return "", code, why
	}
	digest := buildlane.DigestOf(out)
	if digest == "" {
		return "", buildlane.CouldNotRun, fmt.Sprintf("could not run: the registry minted no digest for %s: %.200s", target, out)
	}
	ref := pushRepo + "@" + digest
	say("published %s = %s", target, ref)
	l.published = append(l.published, pins.Image(target, digest))
	return ref, buildlane.Clean, ""
}

// declarePins prints the one line ourea's settle reads back out of this pod's
// log and turns into rows in erebus.build_artifacts — what this build MADE
// (role=built) and what it USED (role=dep), in one declaration because one
// build knows both at one commit.
//
// IT MUST BE THE LAST THING THE LANE SAYS, and that is not a preference. The
// door reads pins out of the LOG TAIL the settle already fetched: the last
// tap.LogTailBytes (64 KiB) of the last 400 lines. MEASURED 2026-09-22 on
// ourea's tip 0585814, when this was declared at push time instead:
//
//	total build log          277,219 bytes / 804 lines
//	after the declaration    252,946 bytes / 343 lines
//	the tail window           65,536 bytes / 400 lines
//	marker inside the tail?   NO — outside by ~3.9x
//
// The line count was fine and irrelevant; the BYTES were not. A build
// publishes and then SIGNS, composes and attests an SBOM, VERIFIES and
// PERMITS — a quarter of a megabyte of engine output after the push. The
// lane declared correctly, the door was live and listening, and
// erebus.build_pins stayed empty with nothing logged anywhere, because
// recordPins returns silently when the marker is not in the tail.
//
// Widening the window was the other option and is worse: it moves an
// invisible cliff, and the failure mode is a row that quietly never appears.
// Declaring last makes the bound genuinely free, which is what the door's
// own test already claimed.
//
// ONE LINE FOR THE WHOLE RUN. push is the only place a ref becomes a digest —
// the star image, a base image, and the :stable move all pass through it — so
// every pin is in hand by the time this fires, each with its own target and
// therefore its own tag.
//
// IT CANNOT FAIL A BUILD. The artifacts are in the registry before this runs;
// the declaration is a fact about them, not a step of publishing them. A pin
// this refuses is named on stderr and nothing else happens — the next build
// of the same artifact restates it.
func (l *buildLane) declarePins() { declare(say, append(l.published, l.consumed...)...) }

// declare TAKES ITS EMITTER because two lanes declare now and each prefixes its
// own log lines — `build:` here, `cast:` in the cast lane. The door finds the
// marker by index within the line (tap.MarkerPayload), so any prefix reads; what
// would not survive forking is the refusal-reporting and the empty-line guard,
// which is the half worth keeping in one place.
func declare(emit func(string, ...any), in ...pins.Pin) {
	line, skipped := pins.Line(in...)
	for _, why := range skipped {
		emit("NOT declaring a pin — %s", why)
	}
	if line != "" {
		emit("%s", line)
	}
}

// sign signs the published image with the CI key, reads its composed SBOM
// (sbomOf: the builder stage's dependencies folded in, the base's document
// linked rather than copied), attaches the SBOM and attests a pointer to it
// (attestSBOM), and verifies the signature against the key's public half. A
// signature or pointer that is already there verifies instead of failing.
func (l *buildLane) sign(ctx context.Context, img *Image, ref, star string) (int, string) {
	encoded, err := l.cosignKey.Plaintext(ctx)
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: the CI signing key did not read: %v", err)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return buildlane.CouldNotRun, "could not run: the CI signing key is not base64"
	}
	cosign := cosignIn().
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

	oras, sbom, failed := l.composedSBOM(ctx, img)
	if failed != nil {
		return failed.code, failed.why
	}
	if code, why := l.attestSBOM(ctx, cosign, oras, ref, sbom); code != buildlane.Clean {
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

// dependencies answers the internal-registry digests this tree's Dockerfiles
// name, as dep pins.
//
// DOCKERFILES ONLY, DELIBERATELY. Every digest in a Dockerfile is something the
// image is made of; a digest anywhere else in a tree may be a fixture, a
// comment or a test table. forge-inuse greps the whole tree and then excludes
// *_test.go and testdata for exactly that reason, and its own notes record
// nearly holding an image forever "on the authority of a Go table test". Here
// the hazard does not arise rather than being filtered back out.
func (l *buildLane) dependencies(ctx context.Context) ([]pins.Pin, error) {
	files, err := l.m.Source.Glob(ctx, "**/Dockerfile*")
	if err != nil {
		return nil, err
	}
	var out []pins.Pin
	seen := map[string]bool{}
	for _, f := range files {
		body, err := l.m.Source.File(f).Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for _, p := range pins.Consumed(body) {
			key := p.Artifact + "\x00" + p.Tag + "\x00" + p.Digest
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, p)
		}
	}
	// NOTHING IS SAID HERE. The declaration at the end of the lane already
	// names every dependency by artifact and digest, so a count line is the
	// same fact twice — and its `len(out) > 0` guard is a branch whose only
	// effect is whether a redundant sentence prints, which no test should be
	// written to pin down.
	return out, nil
}
