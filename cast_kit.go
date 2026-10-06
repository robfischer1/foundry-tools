package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/castlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE KIT CAST — a bundle whose payload is a governance render, not a binary.
//
// WHY IT EXISTS. foundry-stocks's forge-user kit (hooks, env and scripts laid
// once at $HOME, exposed to Claude Code as a managed-settings drop-in) merged to
// main and reached no session: the only thing that moved a host's copy was a
// person running `furnace die --for ~` and `gavel order ~`. The fleet's other
// four rob02 deliveries all ride `app/<name>:stable` through this lane; the kit
// had no record, so it had no lane (foundry-stocks#14873).
//
// WHAT IT SHIPS. app/<kit>:stable carrying die/ (the kit's standalone render,
// byte for byte what `furnace die <kit> --dest` writes) and whatever
// payload_extra names beside it (units/, the unit that lays it). The host's
// delivery sweep stages and flips it like any app channel; the unit the
// subscription restarts on every flip runs `gavel order $HOME --from <live>/die`,
// so the lay keeps gavel's drift check, flatten and receipt.
//
// THE RENDER IS FURNACE'S, ASKED FOR AT THE COMMIT BEING CAST. furnace reads its
// billet as URL@ref from the door and a full sha is a ref (measured: the render
// at origin/main's sha is byte-identical to the render at main). The binary is
// not built here: the lane takes the SIGNED app/furnace:stable the hosts run,
// and verifies it against the repo's own cosign.pub before it executes a byte,
// the trust chain every consumer of that channel already uses.

// kitDoor is where furnace reads the billet from, the one address that works
// from every consumer and serves anonymous read (dies.toml, ADDRESSING).
const kitDoor = checks.OureaDoor

// furnaceChannel is the signed furnace the lane renders with.
const furnaceChannel = bundlelane.RegistryHost + "/app/furnace"

// kitDie renders the kit at the commit the lane was constructed on.
func (l *castLane) kitDie(ctx context.Context, c castlane.Cast) (*dagger.Directory, int, string) {
	furnace, code, why := l.furnaceBinary(ctx)
	if code != buildlane.Clean {
		return nil, code, why
	}
	billet := castlane.Billet(kitDoor, l.m.Repo, l.m.Sha)
	// ImageRust, not the fleet base: furnace shells out to git to read the
	// billet, and the python base carries none.
	rendered := dag.Container().From(checks.ImageRust).
		WithFile("/usr/local/bin/furnace", furnace, dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithEnvVariable("FURNACE_SOURCE", billet).
		WithExec([]string{"furnace", "die", c.Kit, "--provider", "claude", "--dest", "/die"}, anyExit)
	out, code, err := output(ctx, rendered)
	if err != nil {
		return nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: furnace die %s did not run: %v", c.Kit, err)
	}
	if code != 0 {
		cls, why := buildlane.ToolFailed("furnace die "+c.Kit, out)
		return nil, cls, why
	}
	castSay("furnace rendered %s at %.12s: %s", c.Kit, l.m.Sha, strings.TrimSpace(out))
	// AN EMPTY RENDER NEEDS NO CHECK OF ITS OWN: castpin refuses a payload that
	// pins no file, and furnace refuses a kit it does not know.
	return rendered.Directory("/die"), buildlane.Clean, ""
}

// furnaceBinary fetches the signed furnace the hosts run and verifies it
// against this repo's cosign.pub BEFORE the file is handed to anything that
// executes it. The digest is resolved once and both the verify and the pull
// are by that digest, so :stable moving between the two cannot swap the bytes
// that were checked for the bytes that run.
func (l *castLane) furnaceBinary(ctx context.Context) (*dagger.File, int, string) {
	tarball, err := fetchTool(ctx, checks.OrasURL)
	if err != nil {
		return nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: oras could not be provisioned: %v", err)
	}
	// A DRY RUN HOLDS NO REGISTRY TOKEN, so it reads anonymously: the registry
	// answers anonymous pulls, and a dry run that could not render would prove
	// nothing about the render.
	cfg, err := l.registry(ctx)
	if err != nil {
		return nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: the registry token did not read: %v", err)
	}
	if cfg == nil {
		cfg = dag.SetSecret("cast-anon-registry-config", "{}")
	}
	oras := dag.Container().From(checks.ImageFleet).
		WithFile("/tmp/oras.tgz", tarball).
		WithExec([]string{"tar", "-xzf", "/tmp/oras.tgz", "-C", "/usr/local/bin", "oras"}).
		WithMountedSecret("/run/docker/config.json", cfg).
		WithEnvVariable("CAST_RUN", l.stamp)
	out, code, err := output(ctx, oras.WithExec([]string{"oras", "resolve", "--registry-config", "/run/docker/config.json", furnaceChannel + ":stable"}, anyExit))
	if err != nil {
		return nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: %s:stable did not resolve: %v", furnaceChannel, err)
	}
	if code != 0 {
		cls, why := buildlane.ToolFailed("oras resolve "+furnaceChannel, out)
		return nil, cls, why
	}
	digest := buildlane.DigestOf(out)
	if digest == "" {
		return nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: %s:stable resolved to no digest: %.200s", furnaceChannel, out)
	}
	ref := furnaceChannel + "@" + digest

	// The repo's own key: every bundle this lane casts is verified against it,
	// and furnace is signed by the same fleet key.
	nonroot := dagger.ContainerWithMountedSecretOpts{Owner: "65532:65532"}
	vout, vcode, err := output(ctx, cosignIn().
		WithMountedTemp("/tmp").
		WithEnvVariable("HOME", "/tmp").
		WithMountedSecret("/run/docker/config.json", cfg, nonroot).
		WithEnvVariable("DOCKER_CONFIG", "/run/docker").
		WithFile("/run/cosign/cosign.pub", l.m.Source.File("cosign.pub")).
		WithEnvVariable("CAST_RUN", l.stamp).
		WithExec([]string{"verify", "--key", "/run/cosign/cosign.pub", "--insecure-ignore-tlog=true", ref}, entrypointAnyExit))
	if err != nil {
		return nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: the signature check on %s did not run: %v", ref, err)
	}
	if vcode != 0 {
		cls, why := buildlane.ToolFailed("cosign verify "+ref, vout)
		return nil, cls, why
	}
	castSay("verified %s against cosign.pub before rendering with it", ref)

	pulled := oras.WithExec([]string{"oras", "pull", "--registry-config", "/run/docker/config.json", "-o", "/furnace", ref}, anyExit)
	pout, pcode, err := output(ctx, pulled)
	if err != nil {
		return nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: %s did not pull: %v", ref, err)
	}
	if pcode != 0 {
		cls, why := buildlane.ToolFailed("oras pull "+ref, pout)
		return nil, cls, why
	}
	bin := pulled.File("/furnace/furnace")
	if _, err := bin.Size(ctx); err != nil {
		return nil, buildlane.Findings, fmt.Sprintf("findings: %s carries no furnace binary at its root", ref)
	}
	return bin, buildlane.Clean, ""
}
