package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

const (
	kmsKeyRef = "infisical://fleet-cosign-1"
	kmsSpec   = kmsKeyRef + "?project=e4815550-9c4f-4e9b-91a9-4b65d4acc9df&identity=739c79f7-9f0b-4ee3-b0de-bc244c4d3418"
	saToken   = "sa-token-in-the-clear"
)

// bundlesWithKMS runs the lane the way the dual-sign window does: every secret,
// plus the KMS signer and the pod's ServiceAccount token.
func bundlesWithKMS(t *testing.T, m *FoundryTools, spec string, token *dagger.Secret) {
	t.Helper()
	if err := m.Bundle(context.Background(), dag.SetSecret("opa-key", pem64()), dag.SetSecret("registry-token", bundleToken),
		dag.SetSecret("cosign-key", pem64()), dag.SetSecret("cosign-password", "pw"), false, spec, token); err != nil {
		t.Fatalf("bundle: %v", err)
	}
}

// The dual-sign window: every digest the fleet key signs, the KMS key signs
// too, through the plugin from the signing carrier, logging in by
// kubernetes-auth with the mounted token and never holding a key.
func TestInTheDualSignWindowEveryDigestCarriesBothSignatures(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	bundlesWithKMS(t, m, kmsSpec, dag.SetSecret("kms-token", saToken))
	settledOn(t, "0", "published and signed")
	fleet := engine.chain(`"sign"`, `"/run/cosign/key"`, `"--yes"`)
	if fleet == "" {
		t.Fatal("the fleet key stopped signing inside the dual-sign window")
	}
	kms := engine.chain(`"sign"`, `"`+kmsKeyRef+`"`, `"--yes"`)
	if kms == "" {
		t.Fatal("the KMS key signed nothing")
	}
	wantCalls(t, kms,
		[]string{"withFile", `"/usr/local/bin/sigstore-kms-infisical"`},
		[]string{"withMountedSecret", `"/run/kms/token"`},
		[]string{"withEnvVariable", `"INFISICAL_AUTH_METHOD"`, `"kubernetes"`},
		[]string{"withEnvVariable", `"INFISICAL_MACHINE_IDENTITY_ID"`, `"739c79f7-9f0b-4ee3-b0de-bc244c4d3418"`},
		[]string{"withEnvVariable", `"INFISICAL_PROJECT_ID"`, `"e4815550-9c4f-4e9b-91a9-4b65d4acc9df"`},
		[]string{"withEnvVariable", `"INFISICAL_SITE_URL"`, `"https://calypso.notusmi.com"`},
		[]string{"withEnvVariable", `"INFISICAL_KUBERNETES_SERVICE_ACCOUNT_TOKEN_PATH"`, `"/run/kms/token"`},
		[]string{"withMountedSecret", `"/run/docker/config.json"`},
		[]string{"withEnvVariable", `"DOCKER_CONFIG"`, `"/run/docker"`},
		[]string{"withFile", "permissions:493", `"/usr/local/bin/sigstore-kms-infisical"`},
	)
	wantCalls(t, engine.chain(checks.ImageSigningTools), []string{"file", `"/usr/local/bin/sigstore-kms-infisical"`})
	// It is the fleet signer with the file key taken AWAY, after it was mounted.
	wantCalls(t, kms,
		[]string{"withoutMount", `"/run/cosign/key"`},
		[]string{"withoutSecretVariable", `"COSIGN_PASSWORD"`},
	)
	if strings.Contains(kms, saToken) {
		t.Errorf("the ServiceAccount token reached the chain in the clear:\n%s", kms)
	}
	if engine.chain(`"after-sign"`, `"verify"`, `"`+kmsKeyRef+`"`) == "" {
		t.Error("the KMS signature was not verified after it was made")
	}
}

// The orbit dies are signed by both keys as well.
func TestInTheDualSignWindowTheOrbitDiesCarryBothSignatures(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.stdout(`"--name-only"`, "orbits/urania-themis.toml\n")
	said := sayings(t, func() { bundlesWithKMS(t, m, kmsSpec, dag.SetSecret("kms-token", saToken)) })
	settledOn(t, "0", "published and signed "+bundlelane.ContractsDie)
	for _, line := range []string{
		"── orbit dies: second signature, " + kmsKeyRef + " (dual-sign window) ──",
		"with " + kmsKeyRef,
		"with the fleet key",
	} {
		if !strings.Contains(said, line) {
			t.Errorf("the lane did not say %q:\n%s", line, said)
		}
	}
	if engine.chain(`"sign"`, `"`+kmsKeyRef+`"`, `"--yes"`) == "" {
		t.Error("the orbit dies did not get the KMS signature")
	}
}

// Without --kms nothing changes: the fleet key alone, and no plugin anywhere.
func TestWithoutKMSTheFleetKeySignsAlone(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	bundles(t, m)
	settledOn(t, "0", "published and signed")
	if c := engine.chain(`"sigstore-kms-infisical"`); c != "" {
		t.Errorf("the KMS plugin was used without --kms:\n%s", c)
	}
}

// A digest the KMS key already signed is not signed again.
func TestADigestTheKMSKeyAlreadySignedIsNotSignedTwice(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.exitCode(`"`+kmsKeyRef+`"`, 0)
	said := sayings(t, func() { bundlesWithKMS(t, m, kmsSpec, dag.SetSecret("kms-token", saToken)) })
	settledOn(t, "0", "published and signed")
	if !strings.Contains(said, "already carries a signature "+kmsKeyRef+" verifies — not stacking a second one") {
		t.Errorf("the lane did not say why it skipped the KMS signature:\n%s", said)
	}
	if c := engine.chain(`"sign"`, `"`+kmsKeyRef+`"`, `"--yes"`); c != "" {
		t.Errorf("a digest the KMS key already verifies was signed again:\n%s", c)
	}
	if engine.chain(`"sign"`, `"/run/cosign/key"`, `"--yes"`) == "" {
		t.Error("the fleet key's own judgement was skipped because the KMS key had signed")
	}
}

// A KMS signature that fails fails the lane: once a verifier trusts the KMS
// key, a digest without its signature is one that verifier refuses.
func TestAKMSSignatureThatFailsFailsTheLane(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.exitCode(`"`+kmsKeyRef+`"`, 1)
	bundlesWithKMS(t, m, kmsSpec, dag.SetSecret("kms-token", saToken))
	if c := engine.chain(`"/usr/local/bin/verdict"`, `"0"`); c != "" {
		t.Fatalf("the lane settled green with no KMS signature:\n%s", c)
	}
	if engine.chain(`"/usr/local/bin/verdict"`, "cosign sign") == "" {
		t.Error("the lane's verdict does not name the failed cosign sign")
	}
}

func TestKMSWithoutATokenSignsNothing(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	bundlesWithKMS(t, m, kmsSpec, nil)
	settledOn(t, "2", "--kms-token")
	nothingPushed(t)
}

func TestAKMSSpecThatCannotSignSignsNothing(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	bundlesWithKMS(t, m, "awskms://fleet", dag.SetSecret("kms-token", saToken))
	settledOn(t, "2", "want infisical://")
	nothingPushed(t)
}

// A dry run needs no token even with --kms, and signs with neither key.
func TestADryRunWithKMSNeedsNoToken(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	if err := m.Bundle(context.Background(), nil, nil, nil, nil, true, kmsSpec, nil); err != nil {
		t.Fatalf("bundle: %v", err)
	}
	if c := engine.chain(`"/usr/local/bin/verdict"`, `"2"`); c != "" {
		t.Fatalf("a dry run with --kms and no token could not run:\n%s", c)
	}
	nothingPushed(t)
}

// kmsSignOf is the needle for the KMS signature of one ref: cosign's argv as
// the engine records it, contiguous.
func kmsSignOf(ref string) string {
	return `"` + kmsKeyRef + `","--yes","` + ref + `"`
}

// distinctDigests makes the signed twin resolve to its own digest, so a test
// can tell the two refs of one die apart.
func distinctDigests() (plain, signed string) {
	engine.stdout(`-signed"`, "sha256:"+strings.Repeat("2c", 32)+"\n")
	return bundlelane.PolicyDie + "@sha256:" + strings.Repeat("1b", 32), bundlelane.PolicyDie + "@sha256:" + strings.Repeat("2c", 32)
}

// Every ref is signed by the KMS key, not only the first.
func TestTheKMSKeySignsEveryRefOfADie(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	plain, signed := distinctDigests()
	bundlesWithKMS(t, m, kmsSpec, dag.SetSecret("kms-token", saToken))
	settledOn(t, "0", "published and signed")
	for _, ref := range []string{plain, signed} {
		if engine.chain(kmsSignOf(ref)) == "" {
			t.Errorf("%s was not signed by the KMS key", ref)
		}
	}
}

// The first KMS refusal stops the die: the next ref is not signed, and the
// lane does not settle green on the strength of a later success.
func TestTheFirstKMSRefusalStopsTheDie(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	plain, signed := distinctDigests()
	engine.exitCode(kmsSignOf(plain), 1)
	bundlesWithKMS(t, m, kmsSpec, dag.SetSecret("kms-token", saToken))
	if c := engine.chain(`"/usr/local/bin/verdict"`, `"0"`); c != "" {
		t.Fatalf("the lane settled green after the KMS key refused %s:\n%s", plain, c)
	}
	if c := engine.chain(kmsSignOf(signed)); c != "" {
		t.Errorf("signing went on to %s after the KMS key refused %s:\n%s", signed, plain, c)
	}
}

// A fleet-key failure fails the lane before the KMS key is asked at all.
func TestAFleetKeyFailureNeverReachesTheKMSKey(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.exitCode(`"/run/cosign/key","--yes"`, 1)
	bundlesWithKMS(t, m, kmsSpec, dag.SetSecret("kms-token", saToken))
	if c := engine.chain(`"/usr/local/bin/verdict"`, `"0"`); c != "" {
		t.Fatalf("the lane settled green after the fleet key failed:\n%s", c)
	}
	if c := engine.chain(`"sign"`, `"`+kmsKeyRef+`"`, `"--yes"`); c != "" {
		t.Errorf("the KMS key signed after the fleet key failed:\n%s", c)
	}
}

// bundlesKMSOnly runs the lane after the dual-sign window: no file key at all,
// the KMS key and its token alone.
func bundlesKMSOnly(t *testing.T, m *FoundryTools, cosignKey, cosignPassphrase *dagger.Secret) {
	t.Helper()
	if err := m.Bundle(context.Background(), dag.SetSecret("opa-key", pem64()), dag.SetSecret("registry-token", bundleToken),
		cosignKey, cosignPassphrase, false, kmsSpec, dag.SetSecret("kms-token", saToken)); err != nil {
		t.Fatalf("bundle: %v", err)
	}
}

// AFTER THE WINDOW the KMS key signs alone ("Trust Roots to KMS" T1): no file
// key is mounted, no passphrase is set, the repo's cosign.pub is never read,
// and every signature is checked against the KMS key itself.
func TestWithKMSAloneTheKMSKeySignsAndNothingElseDoes(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	plain, signed := distinctDigests()
	said := sayings(t, func() { bundlesKMSOnly(t, m, nil, nil) })
	settledOn(t, "0", "published and signed")
	for _, ref := range []string{plain, signed} {
		if engine.chain(kmsSignOf(ref)) == "" {
			t.Errorf("%s was not signed by the KMS key", ref)
		}
	}
	for needle, what := range map[string]string{
		`"/run/cosign/key"`:        "the file key was mounted or signed with",
		`"COSIGN_PASSWORD"`:        "a passphrase was set",
		`"/run/cosign/cosign.pub"`: "the repo's cosign.pub was read",
		`"withoutMount"`:           "a mount that was never made was taken away",
	} {
		if c := engine.chain(needle); c != "" {
			t.Errorf("%s with --kms alone:\n%s", what, c)
		}
	}
	if engine.chain(`"after-sign"`, `"verify"`, `"`+kmsKeyRef+`"`) == "" {
		t.Error("the KMS signature was not verified against the KMS key after it was made")
	}
	if !strings.Contains(said, "signature, "+kmsKeyRef+" (the KMS key alone)") {
		t.Errorf("the lane did not say the KMS key signs alone:\n%s", said)
	}
	if strings.Contains(said, "dual-sign window") {
		t.Errorf("the lane claimed a dual-sign window it is not in:\n%s", said)
	}
}

// Half a file key is a misconfiguration, not a choice: refused before anything
// is built, with --kms or without it.
func TestHalfAFileKeyIsCouldNotRun(t *testing.T) {
	for name, c := range map[string]struct{ key, pass bool }{
		"a key, no passphrase": {true, false},
		"a passphrase, no key": {false, true},
	} {
		t.Run(name, func(t *testing.T) {
			m := bundleOn(t, nil)
			scriptAGreenBundle()
			var key, pass *dagger.Secret
			if c.key {
				key = dag.SetSecret("cosign-key", pem64())
			}
			if c.pass {
				pass = dag.SetSecret("cosign-password", "pw")
			}
			bundlesKMSOnly(t, m, key, pass)
			settledOn(t, "2", "go together")
			nothingPushed(t)
		})
	}
}

// With neither the file key nor --kms there is nothing to sign with.
func TestABundleWithNoSignerAtAllIsCouldNotRun(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	bundleWith(t, m, false, dag.SetSecret("opa-key", pem64()), dag.SetSecret("registry-token", bundleToken), nil, nil)
	settledOn(t, "2", "a bundle is signed by something")
	nothingPushed(t)
}

// A dry run needs no signer at all.
func TestADryRunNeedsNoSigner(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	bundleWith(t, m, true, nil, nil, nil, nil)
	if c := engine.chain(`"/usr/local/bin/verdict"`, `"2"`); c != "" {
		t.Fatalf("a dry run with no signer could not run:\n%s", c)
	}
	nothingPushed(t)
}
