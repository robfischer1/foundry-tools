package main

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/dagger"
)

// PUBLISH-SVID. With --publish-svid and a --spire socket, the index password is
// a JWT-SVID fetched by an exec of the helper right before the upload or
// publish, under the index's audience, with a fixed username. Without both, the
// static tokens are used exactly as before.

func svidSocket() *dagger.Socket { return dag.LoadSocketFromID("spire-agent-socket") }

func publishSvid(t *testing.T, m *FoundryTools, token, npmToken *dagger.Secret, spire *dagger.Socket, flag bool) {
	t.Helper()
	if err := m.Publish(context.Background(), token, publishURL, checkURL, "publisher", buildIndex, npmToken, npmHosted, false, spire, flag); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func TestAPypiUploadWithPublishSvidUsesAFreshPypiSvidAndNoStaticToken(t *testing.T) {
	m := publishOn(t, publishSdist, publishWheel)
	indexAnswers("", "404")
	engine.stdout(svidHelper+`,"get"`, svidGetAnswer+"\n")
	publishSvid(t, m, nil, nil, svidSocket(), true)
	settledOn(t, "0", " 2 uploaded, 0 already released")
	if engine.chain(svidHelper+`,"get"`) == "" {
		t.Fatal("no JWT-SVID was fetched")
	}
	fetch := engine.chain(svidHelper + `,"get"`)
	wantCalls(t, fetch, []string{"withEnvVariable", `"SVIDCRED_AUDIENCE"`, `"packages-pypi"`})
	up := uploadOf(publishWheel)
	wantCalls(t, up,
		[]string{"withEnvVariable", `"UV_PUBLISH_USERNAME"`, `"svid"`},
		[]string{"withSecretVariable", `"UV_PUBLISH_PASSWORD"`},
	)
	if strings.Contains(up, svidFakeToken) || strings.Contains(fetch, svidFakeToken) {
		t.Error("the token is in a chain in the clear")
	}
}

// The flag without a socket is not a login: the static token is required, and
// used, as ever.
func TestAPypiPublishSvidWithoutASocketKeepsTheStaticPath(t *testing.T) {
	m := publishOn(t, publishSdist, publishWheel)
	indexAnswers("", "404")
	publishSvid(t, m, dag.SetSecret("pypi-token", publishPass), nil, nil, true)
	settledOn(t, "0", " 2 uploaded")
	if engine.chain(svidHelper+`,"get"`) != "" {
		t.Error("a token was fetched with no socket")
	}
	wantCalls(t, uploadOf(publishWheel), []string{"withEnvVariable", `"UV_PUBLISH_USERNAME"`, `"publisher"`})
	m = publishOn(t, publishSdist, publishWheel)
	publishSvid(t, m, nil, nil, nil, true)
	settledOn(t, "2", "--token is required")
}

// Without the flag a socket changes nothing: no helper, no fetch.
func TestAPypiPublishWithASocketButNoFlagFetchesNothing(t *testing.T) {
	m := publishOn(t, publishSdist, publishWheel)
	indexAnswers("", "404")
	publishSvid(t, m, dag.SetSecret("pypi-token", publishPass), nil, svidSocket(), false)
	settledOn(t, "0", " 2 uploaded")
	for _, c := range engine.chains() {
		if strings.Contains(c, "docker-credential-svid") || strings.Contains(c, "SVIDCRED") {
			t.Fatalf("the static path carries the helper:\n%s", c)
		}
	}
}

func TestAPypiSvidThatDoesNotFetchIsCouldNotRunAndUploadsNothing(t *testing.T) {
	m := publishOn(t, publishSdist, publishWheel)
	indexAnswers("", "404")
	engine.stdout(svidHelper+`,"get"`, "{}")
	publishSvid(t, m, dag.SetSecret("pypi-token", publishPass), nil, svidSocket(), true)
	settledOn(t, "2", "JWT-SVID did not fetch")
	noUploads(t)
}

func TestAnNpmPublishWithPublishSvidAuthenticatesWithAFreshNpmSvid(t *testing.T) {
	m := npmOn(t, stellarCoreTS, nil)
	registryAnswers(`{"versions": {"0.5.0": {}}}`, "200")
	engine.stdout(svidHelper+`,"get"`, svidGetAnswer+"\n")
	publishSvid(t, m, nil, nil, svidSocket(), true)
	settledOn(t, "0", "@forge/stellar-core-ts@0.6.0 — published to "+npmHosted)
	wantCalls(t, engine.chain(svidHelper+`,"get"`), []string{"withEnvVariable", `"SVIDCRED_AUDIENCE"`, `"packages-npm"`})
	chain := publishOf("")
	auth := base64.StdEncoding.EncodeToString([]byte("svid:" + svidFakeToken))
	if chain == "" || strings.Contains(chain, svidFakeToken) || strings.Contains(chain, auth) {
		t.Errorf("no publish, or the credential reached it in the clear:\n%s", chain)
	}
	if secret := engine.chain(npmrcNeedle); !strings.Contains(secret, ":_auth="+auth) {
		t.Errorf("the .npmrc secret does not carry the SVID as the Basic password:\n%s", secret)
	}
}

// Each member is its own publish, so each fetches its own token.
func TestAnNpmWorkspaceFetchesATokenPerPackage(t *testing.T) {
	m := npmTreeOn(t, theiaTree())
	registryAnswers(`{"versions": {}}`, "200")
	engine.stdout(svidHelper+`,"get"`, svidGetAnswer+"\n")
	publishSvid(t, m, nil, nil, svidSocket(), true)
	names := map[string]bool{}
	for _, q := range engine.chains() {
		for _, f := range strings.Split(q, `"`) {
			if strings.HasPrefix(f, "publish-svid-packages-npm-") {
				names[f] = true
			}
		}
	}
	n := len(names)
	if n < 2 {
		t.Errorf("fetched %d tokens for a multi-member workspace, want one per member", n)
	}
}

func TestAnNpmPublishSvidWithoutASocketStillNeedsTheStaticToken(t *testing.T) {
	m := npmOn(t, stellarCoreTS, nil)
	publishSvid(t, m, nil, nil, nil, true)
	settledOn(t, "2", "--npm-token is required")
}
