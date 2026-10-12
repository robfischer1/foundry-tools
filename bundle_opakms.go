package main

import (
	"context"
	"strings"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/dagger"
)

// THE OPA TWINS, SIGNED BY KMS (--opa-kms). The long-lived key that signed them
// lived in a CI secret (ca-bundle-lane's OPA_BUNDLE_SIGNING_KEY); a KMS key
// cannot leave the KMS, and OPA's `--signing-key` takes only a key it can read.
// So the twin is built exactly as before, with a throwaway key — OPA computes
// the file hashes and the canonical payload itself — and then the throwaway
// signature is replaced:
//
//	1. bundleresign input   header.payload from the built twin, kid = ouranos_bundle
//	2. cosign sign-blob     the KMS signs it (the plugin sends the message, not a
//	                        digest: Infisical v0.163.0 needs isDigest:false)
//	3. bundleresign apply   DER -> r||s, rebuild the JWS, write it into the
//	                        tarball with every other member where it was
//	4. opa run              verify with the KMS key's PUBLIC half under the key
//	                        id ouranos pins — the gate
//
// A twin that does not verify here fails the build: ouranos would load nothing
// from it and answer 200 with an empty document (bundle.go's header).
//
// The signing is an act on the KMS, so a dry run builds and gates the twin with
// its throwaway signature and does not call it.

// resignOpa replaces the throwaway signature on /work/<file> with the KMS's and
// proves the result verifies. Without --opa-kms it is the identity.
func (l *bundleLane) resignOpa(ctx context.Context, built *dagger.Container, file string) (*dagger.Container, gateResult) {
	if !l.opaKmsOn {
		return built, clean()
	}
	if l.dryRun {
		bundleSay("%s: dry run — keeping the throwaway signature, not calling %s", file, l.opaKms.KeyRef)
		return built, clean()
	}
	bundleSay("── %s: signature, %s in project %s ──", file, l.opaKms.KeyRef, l.opaKms.Project)
	path := workDir + "/" + file
	built = built.WithFile("/usr/local/bin/bundleresign", helperBinary("bundleresign"), dagger.ContainerWithFileOpts{Permissions: 0o755})

	input, code, g := exec(ctx, built, "bundleresign input "+file, "bundleresign", "input", path, bundlelane.OPAKeyID)
	if g.code != buildlane.Clean {
		return nil, g
	}
	if code != 0 {
		c, why := buildlane.ToolFailed("bundleresign input "+file, input)
		return nil, gateResult{c, why}
	}

	cosign, g := l.cosign(ctx)
	if g.code != buildlane.Clean {
		return nil, g
	}
	signer := l.kmsSignerFor(cosign, l.opaKms)
	// stdout is the base64 DER signature and nothing else; the log is stderr.
	sig, code, err := output(ctx, signer.
		WithNewFile("/in/signing-input", input).
		WithExec([]string{"sign-blob", "--key", l.opaKms.KeyRef, "--yes", "--use-signing-config=false", "/in/signing-input"}, entrypointAnyExit))
	if err != nil {
		return nil, couldNotRun("cosign sign-blob never ran for %s: %v", file, err)
	}
	if code != 0 {
		c, why := buildlane.ToolFailed("cosign sign-blob "+l.opaKms.KeyRef, sig)
		return nil, gateResult{c, why}
	}
	pub, code, err := output(ctx, signer.WithExec([]string{"public-key", "--key", l.opaKms.KeyRef}, entrypointAnyExit))
	if err != nil {
		return nil, couldNotRun("cosign public-key never ran for %s: %v", l.opaKms.KeyRef, err)
	}
	if code != 0 {
		c, why := buildlane.ToolFailed("cosign public-key "+l.opaKms.KeyRef, pub)
		return nil, gateResult{c, why}
	}
	if err := bundlelane.CheckOpaPublicKey(pub); err != nil {
		return nil, couldNotRun("%s: %v", l.opaKms.KeyRef, err)
	}

	applied := built.
		WithNewFile(workDir+"/opa-kms.sig", sig).
		WithExec([]string{"bundleresign", "apply", path, bundlelane.OPAKeyID, workDir + "/opa-kms.sig"}, anyExit)
	out, code, err := output(ctx, applied)
	if err != nil {
		return nil, couldNotRun("bundleresign apply never ran for %s: %v", file, err)
	}
	if code != 0 {
		c, why := buildlane.ToolFailed("bundleresign apply "+file, out)
		return nil, gateResult{c, why}
	}

	// THE GATE: load it the way ouranos does. stdin is one query; a bundle whose
	// signature does not verify fails to load and opa run exits 1.
	verify := applied.
		WithNewFile(workDir+"/opa-kms.pub", pub+"\n").
		WithExec(bundlelane.VerifyArgs(workDir+"/opa-kms.pub", path), dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny, Stdin: "true\n"})
	out, code, err = outputBoth(ctx, verify)
	if err != nil {
		return nil, couldNotRun("opa run never verified %s: %v", file, err)
	}
	if code != 0 {
		return nil, couldNotRun("%s carries a KMS signature that does not verify under key id %s — ouranos would load nothing from it:\n%s",
			file, bundlelane.OPAKeyID, strings.TrimSpace(out))
	}
	bundleSay("%s: signed by %s and verified under key id %s", file, l.opaKms.KeyRef, bundlelane.OPAKeyID)
	return applied, clean()
}
