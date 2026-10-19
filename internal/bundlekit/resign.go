package bundlekit

import (
	"context"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/bundleresign"
)

// What the kms-sign capability leaves beside a twin's signing input.
const (
	// SigSuffix ends the file holding the KMS's signature over the signing input:
	// the base64 DER cosign sign-blob prints.
	SigSuffix = ".sig"
	// OpaPubFile is the KMS key's public half, which the twin is verified under.
	OpaPubFile = "opa-kms.pub"
)

// Resign replaces each twin's throwaway signature with the KMS's and proves the
// result loads the way ouranos loads it (bundle_opakms.go resignOpa):
//
//	bundleresign apply   DER -> r||s, the JWS rebuilt, written into the tarball
//	                     with every other member where it was
//	opa run              verified with the KMS key's PUBLIC half under the key id
//	                     ouranos pins — the gate
//
// then the twin is gated against its plain bundle. A twin that does not verify
// fails the lane: ouranos would load nothing from it and answer 200 with an
// empty document.
func Resign(ctx context.Context, r Run) *Fault {
	pubPath := filepath.Join(OutDir(), OpaPubFile)
	pub, bad := ReadFile(pubPath)
	if bad != nil {
		return bad
	}
	if err := bundlelane.CheckOpaPublicKey(string(pub)); err != nil {
		return CouldNotRun("the OPA KMS key: %v", err)
	}
	signed := 0
	for _, name := range []string{"policy", "fleet"} {
		meta, err := ReadMeta(name)
		if err != nil {
			return CouldNotRun("the %s die was not built: %v", name, err)
		}
		for _, file := range meta.Signing {
			if bad := r.resignOne(ctx, pubPath, file); bad != nil {
				return bad
			}
			signed++
		}
	}
	Say("resign: %d twin(s) signed by the KMS and verified under key id %s", signed, bundlelane.OPAKeyID)
	return nil
}

// resignOne applies the KMS signature to one twin and gates it.
func (r Run) resignOne(ctx context.Context, pubPath, file string) *Fault {
	path := filepath.Join(OutDir(), file)
	tgz, bad := ReadFile(path)
	if bad != nil {
		return bad
	}
	sig, bad := ReadFile(path + SigSuffix)
	if bad != nil {
		return bad
	}
	applied, err := bundleresign.Apply(tgz, bundlelane.OPAKeyID, strings.TrimSpace(string(sig)))
	if err == nil {
		err = osWrite(path, applied, 0o644)
	}
	if err != nil {
		return CouldNotRun("bundleresign apply %s failed, so the twin is not signed by the KMS key: %v", file, err)
	}
	// THE GATE: load it the way ouranos does. stdin is one query; a bundle whose
	// signature does not verify fails to load and opa run exits 1.
	argv := bundlelane.VerifyArgs(pubPath, path)
	out, code, bad := r.Exec(ctx, "opa run verification of "+file, r.In.Root, "true\n", argv[0], argv[1:]...)
	if bad != nil {
		return bad
	}
	if code != 0 {
		return CouldNotRun("opa run verification of %s under key id %s failed (exit %d), so the twin is not signed by the KMS key:\n%s", file, bundlelane.OPAKeyID, code, out)
	}
	plain := strings.Replace(file, "-signed", "", 1)
	return Twin(filepath.Join(OutDir(), plain), path, "the signed "+strings.TrimSuffix(plain, ".tar.gz"))
}
