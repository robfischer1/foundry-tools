package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/castlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/governlane"
	"dagger/foundry-tools/internal/pins"
)

// THE GOVERNANCE CAST, AS ONE FUNCTION (Nomos F6). Given a foundry-stocks
// landing, each consumer's render — the verbatim subtree renders/<consumer>/
// <provider> Nomos assembled and an inscription landed — is staged unsigned
// under {registry}/staging/, minted by hephaestus as a SIGNED runtime-gov
// bundle and verified against the repo's own cosign.pub: the cast lane's chain,
// step for step, over a different payload and a different kind.
//
// WHY IT IS NOT THE CAST LANE. The door asks the cast lane for a repo whose
// record says it produces a binary; foundry-stocks has no record (a record is
// poured, not authored, and the pour derives what a repo produces from its tree,
// where the bases/ directory here would earn it an image build on every
// landing). So nothing asks this, and a CronJob in flux does (flux
// forge/governance-cast.yaml): it resolves the newest landing tag and constructs
// this call on that commit, under the cast lane's own account.
//
// IDEMPOTENT, BECAUSE THE TRIGGER POLLS. A render the channel's head already
// carries pins to the head's own pin, so hephaestus re-signs it, allocates no
// version index and answers noop: unchanged renders keep their digest, and a
// host's tongs has nothing to do.
//
// THE HOLD. governance/home carries personal data and is not approved to enter
// the registry. It is refused by name in governlane, not left out of a default,
// so no argument to this function casts it.

// Govern casts each consumer's governance render at the commit the module was
// constructed on as the runtime-gov bundle governance.<consumer>.
func (m *FoundryTools) Govern(
	ctx context.Context,
	// The SPIRE agent's workload socket, forwarded by the calling pod: the
	// identity layer_cast is asked as. Required unless --dry-run.
	// +optional
	spire *dagger.Socket,
	// The token that logs in to the bundle registry (REGISTRY_TOKEN). Required
	// unless --dry-run.
	// +optional
	registryToken *dagger.Secret,
	// The consumers whose governance is cast, one die each. Empty casts
	// forge-root and vault. A held consumer (home) is refused.
	// +optional
	consumers []string,
	// hades' mTLS address. BARE NAME, as the cast lane's is.
	// +optional
	// +default="https://hades:8102"
	hades string,
	// The SPIFFE id hades must present.
	// +optional
	// +default="spiffe://notusmi.com/star/hades"
	hadesID string,
	// Pin the payloads for real; stage, mint and verify nothing.
	// +optional
	dryRun bool,
) error {
	l := &castLane{
		m: m, spire: spire, registryToken: registryToken,
		hades: hades, hadesID: hadesID, dryRun: dryRun,
		stamp: strconv.FormatInt(time.Now().UnixNano(), 10),
	}
	code, reason := l.govern(ctx, consumers)
	return settle(ctx, code, "govern: "+reason)
}

// govern is the lane: preflight, the plan, then one die at a time.
func (l *castLane) govern(ctx context.Context, consumers []string) (int, string) {
	m := l.m
	if m.Repo == "" || m.Sha == "" {
		return 2, "could not run: the governance cast casts a commit the engine fetched — construct the module with --repo and --sha"
	}
	if !l.dryRun && (l.spire == nil || l.registryToken == nil) {
		return 2, "could not run: a cast stages, mints and verifies: --spire and --registry-token are both required (--dry-run needs neither)"
	}
	if len(consumers) == 0 {
		consumers = governlane.DefaultConsumers
	}
	diesToml, ok, err := fileIn(ctx, m.Source, "dies.toml")
	if err != nil {
		return 2, fmt.Sprintf("could not run: the tree could not be read for dies.toml: %v", err)
	}
	if !ok {
		return 1, "findings: this tree carries no dies.toml, so nothing says what a governance die is"
	}
	dies, err := governlane.Plan(diesToml, consumers, m.Repo)
	if err != nil {
		return 1, "findings: " + err.Error()
	}
	if _, ok, err := fileIn(ctx, m.Source, "cosign.pub"); err != nil {
		return 2, fmt.Sprintf("could not run: the tree could not be read for cosign.pub: %v", err)
	} else if !ok {
		return 1, "findings: this repo carries no cosign.pub, so a landed digest could not be verified — nothing was cast"
	}
	if !l.dryRun {
		token, err := l.registryToken.Plaintext(ctx)
		if err != nil {
			return 2, fmt.Sprintf("could not run: the registry token did not read: %v", err)
		}
		l.registryConfig = dag.SetSecret("govern-registry-config", bundlelane.DockerConfig(bundlelane.RegistryHost, bundlelane.RegistryUser, strings.TrimSpace(token)))
	}
	outs := make([]governlane.Outcome, 0, len(dies))
	for _, d := range dies {
		code, why := l.governOne(ctx, d)
		outs = append(outs, governlane.Outcome{Die: d.Name(), Code: code, Reason: why})
	}
	return governlane.Fold(outs)
}

// governOne casts one die: pin its render, stage it, have hephaestus mint it,
// verify what landed and declare it.
func (l *castLane) governOne(ctx context.Context, d governlane.Die) (int, string) {
	c := d.Cast()
	ok, err := l.m.Source.Exists(ctx, d.Path, dagger.DirectoryExistsOpts{ExpectedType: dagger.ExistsTypeDirectoryType})
	if err != nil {
		return 2, fmt.Sprintf("could not run: the tree could not be read for %s: %v", d.Path, err)
	}
	if !ok {
		return 1, fmt.Sprintf("findings: this commit holds no %s — Nomos has not rendered %s, so there is nothing to cast", d.Path, d.Consumer)
	}
	payload := l.m.Source.Directory(d.Path)
	pin, files, code, why := l.pin(ctx, payload)
	if code != 0 {
		return code, why
	}
	if l.dryRun {
		return 0, fmt.Sprintf("clean: dry run — %d file(s) pin to %s for %s; nothing was staged, minted or verified", len(files), pin, c.Artifact())
	}
	if head := l.current(ctx, c, pin); head != "" {
		if code, _ := l.verify(ctx, c, castlane.Result{Digest: head}); code == 0 {
			declare(castSay, pins.Bundle(c.Repo(bundlelane.RegistryHost)+":"+pin, head))
			return 0, fmt.Sprintf("clean: %s already carries %s (%s), signed — nothing was staged, minted or re-signed", c.Artifact(), pin, head)
		}
	}
	ref := c.Stage(bundlelane.RegistryHost, pin)
	digest, code, why := l.stage(ctx, payload, ref, files)
	if code != 0 {
		return code, why
	}
	r, code, why := l.mint(ctx, c, ref+"@"+digest, pin)
	if code != 0 {
		return code, why
	}
	if code, why := l.verify(ctx, c, r); code != 0 {
		return code, why
	}
	declare(castSay, pins.Bundle(c.Repo(bundlelane.RegistryHost)+":"+r.Pin, r.Digest))
	noop := ""
	if r.NoOp {
		noop = " (the channel's head already carried this pin; hephaestus re-signed it and allocated no index)"
	}
	return 0, fmt.Sprintf("clean: cast %s at index %d (%s, %s)%s", c.Artifact(), r.Index, r.Pin, r.Digest, noop)
}

// current answers the channel head's digest when the head is this render's own
// manifest (governlane.Current): the channel tag and the pin tag resolved in one
// go. Every failure answers "" — the full cast then runs and reports it.
func (l *castLane) current(ctx context.Context, c castlane.Cast, pin string) string {
	tarball, err := fetchTool(ctx, checks.OrasURL)
	if err != nil {
		return ""
	}
	repo := c.Repo(bundlelane.RegistryHost)
	out, _, _ := output(ctx, dag.Container().From(checks.ImageFleet).
		WithFile("/tmp/oras.tgz", tarball).
		WithExec([]string{"tar", "-xzf", "/tmp/oras.tgz", "-C", "/usr/local/bin", "oras"}).
		WithMountedSecret("/run/docker/config.json", l.registryConfig).
		WithEnvVariable("CAST_RUN", l.stamp).
		WithExec([]string{"sh", "-c",
			`oras resolve --registry-config /run/docker/config.json "$1" && oras resolve --registry-config /run/docker/config.json "$2"`,
			"resolve", repo + ":stable", repo + ":" + pin}, anyExit))
	return governlane.Current(out)
}
