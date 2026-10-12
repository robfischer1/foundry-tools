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
		dag.SetSecret("cosign-key", pem64()), dag.SetSecret("cosign-password", "pw"), dry, "", token, nil, "", opaKmsSpec)
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
	settledOn(t, "2", "opa run verification of bundle-signed.tar.gz under key id ouranos_bundle")
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

// Every step of the re-sign that fails, by exit code or by the engine, fails the
// lane as could-not-run and publishes nothing.
func TestOpaKMSFailsClosedAtEveryStep(t *testing.T) {
	for _, tc := range []struct{ name, needle, want string }{
		{"resign input", `"bundleresign","input"`, "bundleresign input"},
		{"sign-blob", `"sign-blob"`, "cosign sign-blob"},
		{"public-key", `"public-key"`, "cosign public-key"},
		{"apply", `"bundleresign","apply"`, "bundleresign apply"},
	} {
		for _, how := range []string{"exit", "engine"} {
			t.Run(tc.name+"/"+how, func(t *testing.T) {
				m := bundleOn(t, nil)
				scriptAGreenBundle()
				engine.stdout(`"public-key"`, kmsPublicPEM(t))
				engine.stdout(`"sign-blob"`, "MEUCIQDfake")
				if how == "exit" {
					engine.exitCode(tc.needle, 1)
				} else {
					engine.fail(tc.needle, "engine went away")
				}
				if err := bundlesWithOpaKMS(m, false, dag.SetSecret("kms-token", saToken)); err != nil {
					t.Fatalf("bundle: %v", err)
				}
				settledOn(t, "2", tc.want)
				nothingPublished(t)
			})
		}
	}
}

// A malformed --opa-kms is could-not-run before anything is built.
func TestOpaKMSRefusesAMalformedSpec(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	if err := m.Bundle(context.Background(), nil, nil, nil, nil, true, "", nil, nil, "", "no-slash"); err != nil {
		t.Fatalf("bundle: %v", err)
	}
	settledOn(t, "2", "--opa-kms")
}

// The fleet twin's signature can fail after the policy twin's held.
func TestOpaKMSFailsTheLaneWhenOnlyTheFleetTwinCannotBeSigned(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.stdout(`"public-key"`, kmsPublicPEM(t))
	engine.stdout(`"sign-blob"`, "MEUCIQDfake")
	engine.exitCode(`"apply","/work/fleet-bundle-signed.tar.gz"`, 1)
	if err := bundlesWithOpaKMS(m, false, dag.SetSecret("kms-token", saToken)); err != nil {
		t.Fatalf("bundle: %v", err)
	}
	settledOn(t, "2", "bundleresign apply fleet-bundle-signed.tar.gz")
	nothingPublished(t)
}

// The signer carries the registry login, so a login that does not read stops
// the re-sign before the KMS is called.
func TestOpaKMSStopsWhenTheSignersLoginDoesNotRead(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.failLeaf(`"registry-token"`, "plaintext", "secret vanished")
	if err := bundlesWithOpaKMS(m, false, dag.SetSecret("kms-token", saToken)); err != nil {
		t.Fatalf("bundle: %v", err)
	}
	settledOn(t, "2", "registry token did not read")
	if c := engine.chain(`"sign-blob"`); c != "" {
		t.Errorf("the KMS was called:\n%s", c)
	}
}

// bundlesDual hands the lane BOTH keys: the file key and --opa-kms.
func bundlesDual(m *FoundryTools, dry bool, token *dagger.Secret) error {
	return m.Bundle(context.Background(), dag.SetSecret("opa-key", pem64()), dag.SetSecret("registry-token", bundleToken),
		dag.SetSecret("cosign-key", pem64()), dag.SetSecret("cosign-password", "pw"), dry, "", token, nil, "", opaKmsSpec)
}

// With both keys, :signed is built with the file key and left alone, and the KMS
// signs a COPY that is published on its own pin and channel, :signed-kms.
func TestOpaKMSWithTheFileKeyPublishesASeparateKMSTwin(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	engine.stdout(`"public-key"`, kmsPublicPEM(t))
	engine.stdout(`"sign-blob"`, "MEUCIQDfake")
	said := sayings(t, func() {
		if err := bundlesDual(m, false, dag.SetSecret("kms-token", saToken)); err != nil {
			t.Fatalf("bundle: %v", err)
		}
	})
	settledOn(t, "0", "published and signed")
	if strings.Contains(said, "throwaway") {
		t.Errorf("the file key was handed over, so no throwaway key is minted:\n%s", said)
	}
	for _, f := range []string{"bundle-signed-kms.tar.gz", "fleet-bundle-signed-kms.tar.gz"} {
		if !strings.Contains(said, f+": signed by "+opaKeyRef+" and verified under key id ouranos_bundle") {
			t.Errorf("the lane did not say %s was signed and verified:\n%s", f, said)
		}
		if engine.chain(`"cp"`, `"/work/`+strings.Replace(f, "-kms", "", 1)+`"`, `"/work/`+f+`"`) == "" {
			t.Errorf("%s is not a copy of the file-key twin", f)
		}
		if engine.chain(`"bundleresign"`, `"apply"`, `"/work/`+f+`"`) == "" {
			t.Errorf("the KMS signature was not written into %s", f)
		}
		if engine.chain(`"push"`, `"--artifact-type"`, `"`+f+`:`) == "" {
			t.Errorf("%s was not pushed", f)
		}
	}
	// The file-key twins are never re-signed, and stay on :signed.
	for _, f := range []string{"bundle-signed.tar.gz", "fleet-bundle-signed.tar.gz"} {
		if engine.chain(`"bundleresign","apply","/work/`+f+`"`) != "" {
			t.Errorf("%s, the file-key twin, was re-signed", f)
		}
	}
	for _, tag := range []string{`"signed"`, `"signed-kms"`} {
		if engine.chain(`"tag"`, tag) == "" {
			t.Errorf("no channel %s was moved", tag)
		}
	}
}

// A dry run with both keys builds and gates, calls nothing, and has no KMS twin.
func TestOpaKMSDualDryRunCallsNothing(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	if err := bundlesDual(m, true, nil); err != nil {
		t.Fatalf("bundle: %v", err)
	}
	for _, needle := range []string{`"sign-blob"`, `"bundleresign"`} {
		if c := engine.chain(needle); c != "" {
			t.Errorf("a dry run reached %s:\n%s", needle, c)
		}
	}
	nothingPushed(t)
}
