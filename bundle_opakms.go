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
//
// In dual mode (both keys given) file keeps the file key's signature and the
// KMS signs a copy of it, kmsTwin(file), which is what gets gated and published.
func (l *bundleLane) resignOpa(ctx context.Context, built *dagger.Container, file string) (*dagger.Container, gateResult) {
	if !l.opaKmsOn {
		return built, clean()
	}
	if l.dryRun {
		bundleSay("%s: dry run — not calling %s", file, l.opaKms.KeyRef)
		return built, clean()
	}
	if l.opaDual {
		copied := kmsTwinName(file)
		built = built.WithExec([]string{"cp", workDir + "/" + file, workDir + "/" + copied}, anyExit)
		if _, g := stepOut(ctx, built, "copy "+file+" to "+copied); g.code != buildlane.Clean {
			return nil, g
		}
		file = copied
	}
	bundleSay("── %s: signature, %s in project %s ──", file, l.opaKms.KeyRef, l.opaKms.Project)
	path := workDir + "/" + file
	built = built.WithFile("/usr/local/bin/bundleresign", helperBinary("bundleresign"), dagger.ContainerWithFileOpts{Permissions: 0o755})

	input, g := stepOut(ctx, built.WithExec([]string{"bundleresign", "input", path, bundlelane.OPAKeyID}, anyExit), "bundleresign input "+file)
	if g.code != buildlane.Clean {
		return nil, g
	}
	cosign, g := l.cosign(ctx)
	if g.code != buildlane.Clean {
		return nil, g
	}
	signer := l.kmsSignerFor(cosign, l.opaKms)
	// stdout is the base64 DER signature and nothing else; the log is stderr.
	sig, g := stepOut(ctx, signer.
		WithNewFile("/in/signing-input", input).
		WithExec([]string{"sign-blob", "--key", l.opaKms.KeyRef, "--yes", "--use-signing-config=false", "--new-bundle-format=false", "/in/signing-input"}, entrypointAnyExit),
		"cosign sign-blob "+l.opaKms.KeyRef)
	if g.code != buildlane.Clean {
		return nil, g
	}
	pub, g := stepOut(ctx, signer.WithExec([]string{"public-key", "--key", l.opaKms.KeyRef}, entrypointAnyExit), "cosign public-key "+l.opaKms.KeyRef)
	if g.code != buildlane.Clean {
		return nil, g
	}
	if err := bundlelane.CheckOpaPublicKey(pub); err != nil {
		return nil, couldNotRun("%s: %v", l.opaKms.KeyRef, err)
	}

	applied := built.
		WithNewFile(workDir+"/opa-kms.sig", sig).
		WithExec([]string{"bundleresign", "apply", path, bundlelane.OPAKeyID, workDir + "/opa-kms.sig"}, anyExit)
	if _, g := stepOut(ctx, applied, "bundleresign apply "+file); g.code != buildlane.Clean {
		return nil, g
	}

	// THE GATE: load it the way ouranos does. stdin is one query; a bundle whose
	// signature does not verify fails to load and opa run exits 1.
	verify := applied.
		WithNewFile(workDir+"/opa-kms.pub", pub+"\n").
		WithExec(bundlelane.VerifyArgs(workDir+"/opa-kms.pub", path), dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny, Stdin: "true\n"})
	if _, g := stepOut(ctx, verify, "opa run verification of "+file+" under key id "+bundlelane.OPAKeyID); g.code != buildlane.Clean {
		return nil, g
	}
	bundleSay("%s: signed by %s and verified under key id %s", file, l.opaKms.KeyRef, bundlelane.OPAKeyID)
	return applied, clean()
}

// stepOut runs one step of the re-sign and answers what it printed. A step that
// would not run or exited non-zero is could-not-run: nothing here is a claim
// about the tree, and a twin that was not signed or does not verify publishes
// nothing.
func stepOut(ctx context.Context, ctr *dagger.Container, what string) (string, gateResult) {
	out, code, err := output(ctx, ctr)
	if err != nil {
		return "", couldNotRun("%s never ran: %v", what, err)
	}
	if code != 0 {
		return "", couldNotRun("%s failed (exit %d), so the twin is not signed by the KMS key:\n%s", what, code, out)
	}
	return out, clean()
}

// kmsTwinName is the KMS-signed copy's file name: bundle-signed.tar.gz becomes
// bundle-signed-kms.tar.gz.
func kmsTwinName(file string) string {
	return strings.TrimSuffix(file, ".tar.gz") + "-kms.tar.gz"
}

// kmsTwin answers the file published under :signed-kms, or "" when none is.
//
// Dual mode: the KMS-signed COPY of the file-key twin. KMS-only mode: the twin
// itself, already KMS-signed in place, so :signed and :signed-kms carry the SAME
// bytes. ouranos reads :signed-kms, so dropping --opa-signing-key must not stop
// that channel; :signed keeps being published (KMS-signed, no longer file-key)
// for any reader still pinned to it. A dry run publishes nothing.
func (l *bundleLane) kmsTwin(file string) string {
	if l.dryRun {
		return ""
	}
	if l.opaDual {
		return kmsTwinName(file)
	}
	if l.opaKmsOn {
		return file
	}
	return ""
}
