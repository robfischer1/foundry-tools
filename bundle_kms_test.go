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
	)
	wantCalls(t, engine.chain(checks.ImageSigningTools), []string{"file", `"/usr/local/bin/sigstore-kms-infisical"`})
	if strings.Contains(kms, `"/run/cosign/key"`) || strings.Contains(kms, `"COSIGN_PASSWORD"`) {
		t.Errorf("the KMS signer was handed the file key:\n%s", kms)
	}
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
	bundlesWithKMS(t, m, kmsSpec, dag.SetSecret("kms-token", saToken))
	settledOn(t, "0", "published and signed")
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
