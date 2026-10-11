package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/castcache"
	"dagger/foundry-tools/internal/checks"
)

const (
	credsNeedle   = `"/usr/local/bin/casttarget","creds"`
	restoreNeedle = `"/usr/local/bin/casttarget","restore"`
	saveNeedle    = `"/usr/local/bin/casttarget","save"`

	cacheKeyID  = "ASIAEXAMPLEKEYID"
	cacheSecret = "exampleSecretAccessKey0123456789"
	cacheToken  = "exampleSessionToken0123456789"
)

// scriptTheCredentials is what the credential fetch leaves in its file.
func scriptTheCredentials() {
	engine.contents(castcache.CredsOut, `{"access_key_id":"`+cacheKeyID+`","secret_access_key":"`+cacheSecret+`","session_token":"`+cacheToken+`"}`)
}

// indexOfChain is the position of the first recorded query holding every
// needle, or -1: the order the lane asked things of the engine.
func indexOfChain(needles ...string) int {
	for i, q := range engine.chains() {
		ok := true
		for _, n := range needles {
			ok = ok && strings.Contains(q, n)
		}
		if ok {
			return i
		}
	}
	return -1
}

// THE ARCHIVE'S COORDINATES: the release volume the build uses, the repo, the
// toolchain key derived from the lane's own image, and the release target.
// Restore runs before the build and save after the cast, each in a container
// of its own that mounts the same SHARED volume.
func TestACastRestoresTheTargetBeforeTheBuildAndSavesAfter(t *testing.T) {
	m := castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	scriptTheCredentials()
	casts(t, m)
	settledOn(t, "0", "clean: cast app/tongs:stable")

	toolchain := castcache.ToolchainKey(checks.ImageRust)
	if !strings.HasPrefix(toolchain, "rust-1.97.0-bookworm-") {
		t.Fatalf("toolchain key %q", toolchain)
	}
	for _, needle := range []string{restoreNeedle, saveNeedle} {
		chain := engine.chain(needle)
		if chain == "" {
			t.Fatalf("no %s step reached the engine", needle)
		}
		wantCalls(t, chain,
			[]string{"withMountedCache", `path:"/cache/cargo-release"`, `sharing:SHARED`},
			[]string{"withUser", `"0"`},
			[]string{"withMountedSecret", `path:"/run/castcache/access-key-id"`},
			[]string{"withMountedSecret", `path:"/run/castcache/secret-access-key"`},
			[]string{"withMountedSecret", `path:"/run/castcache/session-token"`},
			[]string{"withExec", `"--repo","http://door:8215/rob/tongs.git"`, `"--toolchain","` + toolchain + `"`, `"--dir","/cache/cargo-release"`},
		)
	}
	restore, build, save := indexOfChain(restoreNeedle), indexOfChain(cargoNeedle), indexOfChain(saveNeedle)
	if restore < 0 || build < 0 || save < 0 || !(restore < build && build < save) {
		t.Errorf("restore at %d, build at %d, save at %d: want restore, then the build, then save", restore, build, save)
	}
	// The build container is the tree's code's: it holds no credential and
	// no archive step.
	if c := engine.chain(cargoNeedle); strings.Contains(c, "withMountedSecret") || strings.Contains(c, "casttarget") {
		t.Errorf("the build container carries the archive's secrets or helper:\n%s", c)
	}
}

// CREDENTIALS ARE DAGGER SECRETS: the plaintext reaches the engine in a
// setSecret and nowhere else, never in an env variable or an argument.
func TestTheArchiveCredentialsTravelAsDaggerSecretsOnly(t *testing.T) {
	m := castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	scriptTheCredentials()
	casts(t, m)

	secrets := 0
	for _, q := range engine.chains() {
		for _, plain := range []string{cacheKeyID, cacheSecret, cacheToken} {
			if !strings.Contains(q, plain) {
				continue
			}
			secrets++
			if !strings.Contains(q, "setSecret(") || strings.Contains(q, "withExec") || strings.Contains(q, "withEnvVariable") {
				t.Errorf("a credential travelled outside a setSecret:\n%s", q)
			}
		}
	}
	if secrets < 3 {
		t.Errorf("%d of the 3 credentials were made secrets", secrets)
	}
	// The fetch container holds the socket and the helper, and it leaves the
	// credentials in a file: its stdout is a progress log.
	fetch := engine.chain(credsNeedle)
	wantCalls(t, fetch,
		[]string{"withUnixSocket", `path:"/run/spire/agent.sock"`, `owner:"65532:65532"`},
		[]string{"withEnvVariable", `"CASTCACHE_STAMP"`},
	)
	if engine.chain(credsNeedle, `file(path:"`+castcache.CredsOut+`")`) == "" {
		t.Error("the credentials were not read back from their file")
	}
}

// DEGRADE, NEVER FAIL. Whatever the cache does, the cast's verdict is the
// cast's own.
func TestTheArchiveDegradesWithoutChangingTheVerdict(t *testing.T) {
	cases := map[string]struct {
		script    func()
		restored  bool // the restore step reached the engine
		saved     bool
		wantBuild bool
	}{
		"credentials refused": {func() {
			engine.exitCode(credsNeedle, 1)
			engine.stdout(credsNeedle, "credentials unavailable: STS AssumeRoleWithWebIdentity on arn:aws:iam::role/CastCache answered HTTP 403 AccessDenied")
		}, false, false, true},
		"credentials never fetched": {func() { engine.fail(credsNeedle, "engine hiccup") }, false, false, true},
		"credentials file unreadable": {func() {
			engine.fail(castcache.CredsOut, "no such file or directory")
		}, false, false, true},
		"credentials malformed": {func() { engine.contents(castcache.CredsOut, `{}`) }, false, false, true},
		"restore did not run": {func() {
			scriptTheCredentials()
			engine.fail(restoreNeedle, "engine hiccup")
		}, true, true, true},
		"restore skipped by the helper": {func() {
			scriptTheCredentials()
			engine.stdout(restoreNeedle, "cast-cache: restore skipped: no archive yet")
		}, true, true, true},
		"restore exited non-zero": {func() {
			scriptTheCredentials()
			engine.exitCode(restoreNeedle, 2)
		}, true, true, true},
		"save did not run": {func() {
			scriptTheCredentials()
			engine.fail(saveNeedle, "engine hiccup")
		}, true, true, true},
		"save exited non-zero": {func() {
			scriptTheCredentials()
			engine.exitCode(saveNeedle, 1)
			engine.stdout(saveNeedle, "cast-cache: save skipped: the upload: PUT answered HTTP 500")
		}, true, true, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := castOn(t, nil)
			scriptACast(castPin + "\ntongs\n")
			c.script()
			casts(t, m)
			settledOn(t, "0", "clean: cast app/tongs:stable at index 7")
			if (engine.chain(restoreNeedle) != "") != c.restored {
				t.Errorf("restore reached the engine: %v, want %v", !c.restored, c.restored)
			}
			if (engine.chain(saveNeedle) != "") != c.saved {
				t.Errorf("save reached the engine: %v, want %v", !c.saved, c.saved)
			}
			if (engine.chain(cargoNeedle) != "") != c.wantBuild {
				t.Error("the release build did not run")
			}
		})
	}
}

// The lines the lane logs and records say why the cache stood aside.
func TestTheArchiveStepNamesWhyItWasSkipped(t *testing.T) {
	m := castOn(t, nil)
	r := newRun(m.Source, m.Repo, "")
	ctx := context.Background()

	// No --spire (a dry run): nothing to ask for credentials as.
	l := &castLane{m: m, stamp: "1"}
	l.restoreTarget(ctx, r)
	wantLine(t, l.lines, "cast-cache: restore skipped: no SPIRE socket was forwarded")
	if engine.chain(credsNeedle) != "" || engine.chain(restoreNeedle) != "" {
		t.Error("a lane with no identity asked for credentials")
	}

	// The credential fetch answered a refusal.
	engine.exitCode(credsNeedle, 1)
	engine.stdout(credsNeedle, "credentials unavailable: HTTP 403 AccessDenied")
	l = &castLane{m: m, stamp: "2", spire: dag.LoadSocketFromID("spire-agent-socket")}
	l.restoreTarget(ctx, r)
	wantLine(t, l.lines, "cast-cache: restore skipped: credentials unavailable: HTTP 403 AccessDenied")

	// The helper's own line is what is logged.
	engine.reset()
	scriptTheCredentials()
	engine.stdout(restoreNeedle, "cast-cache: restored s3://cast-cache/tongs/x/y.tar.gz: 812 files from a 90.0 MiB archive in 2.1s")
	l = &castLane{m: m, stamp: "3", spire: dag.LoadSocketFromID("spire-agent-socket")}
	l.restoreTarget(ctx, r)
	wantLine(t, l.lines, "cast-cache: restored s3://cast-cache/tongs/x/y.tar.gz: 812 files")
}

func wantLine(t *testing.T, lines []string, want string) {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, want) {
			return
		}
	}
	t.Errorf("no line holds %q in %q", want, lines)
}

// A dry run with no socket restores nothing and saves nothing, and passes.
func TestADryRunWithNoSocketSkipsTheArchiveAndPasses(t *testing.T) {
	m := castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	castWith(t, m, nil, nil, "", true)
	settledOn(t, "0", "clean: dry run")
	for _, needle := range []string{credsNeedle, restoreNeedle, saveNeedle} {
		if engine.chain(needle) != "" {
			t.Errorf("a dry run with no identity reached %s", needle)
		}
	}
	if engine.chain(cargoNeedle) == "" {
		t.Error("the dry run did not build")
	}
}

// A cast that fails saves nothing: the archive is what the next cast starts
// from, and a failed cast's target is not known good.
func TestAFailedCastSavesNothing(t *testing.T) {
	m := castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	scriptTheCredentials()
	engine.exitCode(mintNeedle, 2)
	casts(t, m)
	if engine.chain(restoreNeedle) == "" {
		t.Error("the restore did not run before the build")
	}
	if engine.chain(saveNeedle) != "" {
		t.Error("a cast that did not hold saved its target")
	}
}

// The Go lane has no cargo target: it asks for no credentials and archives
// nothing.
func TestAGoCastDoesNotTouchTheArchive(t *testing.T) {
	m := goCastOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	scriptTheCredentials()
	casts(t, m)
	for _, needle := range []string{credsNeedle, restoreNeedle, saveNeedle} {
		if engine.chain(needle) != "" {
			t.Errorf("a Go cast reached %s", needle)
		}
	}
}
