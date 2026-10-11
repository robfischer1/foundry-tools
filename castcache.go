package main

import (
	"context"
	"encoding/json"
	"fmt"

	"dagger/foundry-tools/internal/castcache"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE CAST'S TARGET ARCHIVE. The release target lives in an engine-local cache
// volume (checks.ReleaseCacheFor) that does not survive the hours between
// production casts, so every cast compiled every crate (cerberus ~234 crates,
// ~580 CPU-s). Around the build the lane now restores the target from, and
// saves it to, one SeaweedFS object per repo, toolchain and triple
// (internal/castcache). The engine's volume is KEPT: a warm engine is still
// the fastest start, the restore leaves a warm volume alone, and the archive
// is what a cold one is filled from.
//
// THE CACHE NEVER CHANGES A VERDICT. Every step here answers a log line and
// no error. A refusal, a timeout or a missing object is "cast-cache: restore
// skipped: <why>" and a cold build, or "save skipped" and a cast that held.
//
// CREDENTIALS ARE DAGGER SECRETS, NEVER ENV OR ARGUMENTS. A container that holds
// the lane's forwarded SPIRE socket and nothing else trades its JWT-SVID
// (audience seaweedfs) for temporary STS credentials and leaves them in a file,
// not on stdout, which dagger logs. The module reads that file into three
// secrets, mounted as files in a container that runs only casttarget. The
// build container never sees them: cargo runs the tree's build scripts, and
// they are the tree's code.

// castTargetPath is where casttarget sits in the lane's containers.
const castTargetPath = "/usr/local/bin/casttarget"

// targetArchive is one cast's credentials for the archive, or the reason it
// has none.
type targetArchive struct {
	keyID, secret, token *dagger.Secret
	skip                 string
}

// archiveCredentials trades the cast's SPIRE identity for archive credentials.
func (l *castLane) archiveCredentials(ctx context.Context) *targetArchive {
	if l.spire == nil {
		return &targetArchive{skip: "no SPIRE socket was forwarded (--spire), so there is no identity to ask SeaweedFS for credentials as"}
	}
	// THE STAMP KEEPS THE ENGINE FROM ANSWERING THIS EXEC FROM ITS CACHE: the
	// credentials expire, and a cached exec would hand back last cast's.
	fetch := dag.Container().From(checks.ImageStatic).
		WithFile(castTargetPath, helperBinary("casttarget")).
		WithUnixSocket("/run/spire/agent.sock", l.spire, dagger.ContainerWithUnixSocketOpts{Owner: "65532:65532"}).
		WithEnvVariable("CASTCACHE_SOCKET", "unix:///run/spire/agent.sock").
		WithEnvVariable("CASTCACHE_STAMP", l.stamp).
		WithExec([]string{castTargetPath, "creds"}, anyExit)
	out, code, err := output(ctx, fetch)
	if err != nil {
		return &targetArchive{skip: fmt.Sprintf("the credential fetch did not run: %.200v", err)}
	}
	if code != 0 {
		return &targetArchive{skip: fmt.Sprintf("%.240s", out)}
	}
	body, err := fetch.File(castcache.CredsOut).Contents(ctx)
	if err != nil {
		return &targetArchive{skip: fmt.Sprintf("the credentials could not be read back: %.200v", err)}
	}
	var c castcache.Creds
	if err := json.Unmarshal([]byte(body), &c); err != nil || c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return &targetArchive{skip: "the credential fetch left no usable credentials"}
	}
	return &targetArchive{
		keyID:  dag.SetSecret("cast-cache-key-id-"+l.stamp, c.AccessKeyID),
		secret: dag.SetSecret("cast-cache-secret-key-"+l.stamp, c.SecretAccessKey),
		token:  dag.SetSecret("cast-cache-session-token-"+l.stamp, c.SessionToken),
	}
}

// archiveStep runs casttarget's restore or save over the repository's release
// volume and answers the line to log. It runs in a container of its own on
// the static base, as root (the volume's files are cargo's, root's), beside
// the same SHARED volume the build mounts.
func (l *castLane) archiveStep(ctx context.Context, r *run, verb string) string {
	a := l.cache
	if a == nil || a.skip != "" {
		why := "no archive credentials"
		if a != nil {
			why = a.skip
		}
		return "cast-cache: " + verb + " skipped: " + why
	}
	// THE STAMP, again: a restore or a save is an act on a volume, and an
	// exec the engine answered from its cache would not have acted.
	step := r.withReleaseVolume(dag.Container().From(checks.ImageStatic)).
		WithUser("0").
		WithFile(castTargetPath, helperBinary("casttarget")).
		WithMountedSecret(castcache.AccessKeyFile, a.keyID).
		WithMountedSecret(castcache.SecretKeyFile, a.secret).
		WithMountedSecret(castcache.TokenFile, a.token).
		WithEnvVariable("CASTCACHE_STAMP", l.stamp).
		WithExec([]string{castTargetPath, verb,
			"--repo", l.m.Repo,
			"--toolchain", castcache.ToolchainKey(checks.ImageRust),
			"--dir", checks.RustReleaseTarget}, anyExit)
	out, code, err := output(ctx, step)
	switch {
	case err != nil:
		return fmt.Sprintf("cast-cache: %s skipped: the step did not run: %.200v", verb, err)
	case code != 0:
		return fmt.Sprintf("cast-cache: %s skipped: casttarget exited %d: %.200s", verb, code, out)
	case out == "":
		return "cast-cache: " + verb + " skipped: casttarget answered nothing"
	}
	return out
}

// restoreTarget fetches the credentials for the cast and fills a cold release
// volume from the archive, before the build.
func (l *castLane) restoreTarget(ctx context.Context, r *run) {
	l.cache = l.archiveCredentials(ctx)
	l.say("%s", l.archiveStep(ctx, r, "restore"))
}

// saveTarget archives the release volume after a cast that held. A cast with
// no cargo release (the Go lane) restored nothing and saves nothing.
func (l *castLane) saveTarget(ctx context.Context) {
	if l.cache == nil {
		return
	}
	castSay("%s", l.archiveStep(ctx, newRun(l.m.Source, l.m.Repo, ""), "save"))
}
