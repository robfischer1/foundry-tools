package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/dagger"
)

const (
	opaKmsSpec = "e4815550-0000-4000-8000-0000000000aa/ouranos-bundle-1?identity=739c79f7-9f0b-4ee3-b0de-bc244c4d3418"
	opaKeyRef  = "infisical://ouranos-bundle-1"
)

func kmsPublicPEM(t *testing.T) string {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// bundlesWithOpaKMS runs the lane with --opa-kms and NO --opa-signing-key: the
// long-lived key is not handed over at all.
func bundlesWithOpaKMS(m *FoundryTools, dry bool, token *dagger.Secret) error {
	return m.Bundle(context.Background(), nil, dag.SetSecret("registry-token", bundleToken),
		dag.SetSecret("cosign-key", pem64()), dag.SetSecret("cosign-password", "pw"), dry, "", token, opaKmsSpec)
}

// Without the flag the OPA twins are signed by the file key exactly as before,
// and nothing about the KMS path is built.
func TestWithoutOpaKMSTheTwinsAreSignedByTheFileKeyAlone(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	bundles(t, m)
	settledOn(t, "0", "published and signed")
	if c := engine.chain(`"--signing-key"`, `"/run/opa/sign.key"`, `"ES256"`); c == "" {
		t.Error("the twins were not built with the mounted file key")
	}
	for _, needle := range []string{`"sign-blob"`, `"bundleresign"`, `"ouranos_bundle"`} {
		if c := engine.chain(needle); c != "" {
			t.Errorf("%s reached the engine without --opa-kms:\n%s", needle, c)
		}
	}
}

// With the flag: no long-lived key is needed, the twin is built with a
// throwaway, the KMS key signs `header.payload`, and opa run gates the result.
func TestOpaKMSSignsTheTwinsAndGatesThemWithOpaRun(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.stdout(`"public-key"`, kmsPublicPEM(t))
	engine.stdout(`"sign-blob"`, "MEUCIQDfake")
	said := sayings(t, func() {
		if err := bundlesWithOpaKMS(m, false, dag.SetSecret("kms-token", saToken)); err != nil {
			t.Fatalf("bundle: %v", err)
		}
	})
	settledOn(t, "0", "published and signed")
	for _, f := range []string{"bundle-signed.tar.gz", "fleet-bundle-signed.tar.gz"} {
		if !strings.Contains(said, f+": signed by "+opaKeyRef+" and verified under key id ouranos_bundle") {
			t.Errorf("the lane did not say %s was signed and verified:\n%s", f, said)
		}
	}
	sign := engine.chain(`"sign-blob"`, `"--key"`, `"`+opaKeyRef+`"`, `"/in/signing-input"`)
	if sign == "" {
		t.Fatal("the KMS key signed nothing")
	}
	wantCalls(t, sign,
		[]string{"withEnvVariable", `"INFISICAL_PROJECT_ID"`, `"e4815550-0000-4000-8000-0000000000aa"`},
		[]string{"withEnvVariable", `"INFISICAL_MACHINE_IDENTITY_ID"`, `"739c79f7-9f0b-4ee3-b0de-bc244c4d3418"`},
		[]string{"withMountedSecret", `"/run/kms/token"`},
		[]string{"withoutMount", `"/run/cosign/key"`},
	)
	if strings.Contains(sign, saToken) {
		t.Errorf("the ServiceAccount token reached the chain in the clear:\n%s", sign)
	}
	if engine.chain(`"bundleresign"`, `"apply"`, `"/work/bundle-signed.tar.gz"`, `"ouranos_bundle"`) == "" {
		t.Error("the KMS signature was not written into the policy twin")
	}
	if engine.chain(`"bundleresign"`, `"apply"`, `"/work/fleet-bundle-signed.tar.gz"`, `"ouranos_bundle"`) == "" {
		t.Error("the KMS signature was not written into the fleet twin")
	}
	gate := engine.chain(`"opa"`, `"run"`, `"--verification-key-id"`, `"ouranos_bundle"`, `"/work/fleet-bundle-signed.tar.gz"`)
	if gate == "" {
		t.Fatal("no opa run verified the re-signed twin")
	}
	// The build-time key is a throwaway: the long-lived one was never offered.
	if !strings.Contains(said, "throwaway") {
		t.Errorf("the lane did not say the twins are built with a throwaway key:\n%s", said)
	}
}

// A twin whose KMS signature does not verify fails the build: nothing is
// pushed, tagged or signed.
func TestOpaKMSFailsTheBuildWhenTheSignatureDoesNotVerify(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.stdout(`"public-key"`, kmsPublicPEM(t))
	engine.stdout(`"sign-blob"`, "MEUCIQDfake")
	engine.exitCode(`"--verification-key-id"`, 1)
	if err := bundlesWithOpaKMS(m, false, dag.SetSecret("kms-token", saToken)); err != nil {
		t.Fatalf("bundle: %v", err)
	}
	settledOn(t, "2", "does not verify under key id ouranos_bundle")
	nothingPublished(t)
}

// A KMS that answers a key that is not P-256 is refused before anything is
// signed with it.
func TestOpaKMSRefusesAKeyThatIsNotP256(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.stdout(`"public-key"`, "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n")
	engine.stdout(`"sign-blob"`, "MEUCIQDfake")
	if err := bundlesWithOpaKMS(m, false, dag.SetSecret("kms-token", saToken)); err != nil {
		t.Fatalf("bundle: %v", err)
	}
	settledOn(t, "2", "infisical://ouranos-bundle-1")
	nothingPublished(t)
}

// It signs as a machine identity: no token, no run.
func TestOpaKMSNeedsTheServiceAccountToken(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	if err := bundlesWithOpaKMS(m, false, nil); err != nil {
		t.Fatalf("bundle: %v", err)
	}
	settledOn(t, "2", "--kms-token")
	nothingPushed(t)
}

// A dry run builds and gates the twins with their throwaway signature and
// calls the KMS for nothing.
func TestADryRunWithOpaKMSNeverCallsTheKMS(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	if err := bundlesWithOpaKMS(m, true, nil); err != nil {
		t.Fatalf("bundle: %v", err)
	}
	if c := engine.chain(`"/usr/local/bin/verdict"`, `"2"`); c != "" {
		t.Fatalf("a dry run with --opa-kms and no token could not run:\n%s", c)
	}
	if c := engine.chain(`"sign-blob"`); c != "" {
		t.Errorf("a dry run called the KMS:\n%s", c)
	}
	nothingPushed(t)
}

// nothingPublished is nothingPushed less its signing check: the KMS signature
// of the twin is made BEFORE the publish, and is why the gate exists.
func nothingPublished(t *testing.T) {
	t.Helper()
	if chain := engine.chain(`"push"`, `"--artifact-type"`); chain != "" {
		t.Fatalf("something was pushed:\n%s", chain)
	}
	if chain := engine.chain(`"sign"`, `"--yes"`); chain != "" {
		t.Fatalf("an image digest was signed:\n%s", chain)
	}
}

func TestParseOpaKMS(t *testing.T) {
	k, ok, err := bundlelane.ParseOpaKMS("proj/key-1", "fallback-id")
	if err != nil || !ok || k.KeyRef != "infisical://key-1" || k.Project != "proj" || k.Identity != "fallback-id" || k.Site != bundlelane.DefaultKMSSite {
		t.Errorf("%+v %v %v", k, ok, err)
	}
	if k, _, _ := bundlelane.ParseOpaKMS("proj/key-1?identity=x&site=https://s.example", "fallback-id"); k.Identity != "x" || k.Site != "https://s.example" {
		t.Errorf("%+v", k)
	}
	if _, ok, err := bundlelane.ParseOpaKMS("  ", "x"); ok || err != nil {
		t.Errorf("an empty spec is no signer: %v %v", ok, err)
	}
	for _, bad := range []string{"proj", "/key", "proj/", "a/b/c", "proj/key?site=http://s"} {
		if _, ok, err := bundlelane.ParseOpaKMS(bad, ""); ok || err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
