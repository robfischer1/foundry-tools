package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

const (
	publishSdist = "stellar_core-1.43.0.tar.gz"
	publishWheel = "stellar_core-1.43.0-py3-none-any.whl"

	publishURL  = "https://nexus.example/repository/pypi-hosted/"
	checkURL    = "https://nexus.example/repository/pypi-hosted/simple/"
	buildIndex  = "https://nexus.example/repository/pypi/simple"
	publishPass = "hunter2"

	// probeNeedle is in the index probe's chain and in no other.
	probeNeedle = `"PUBLISH_PROBED_AT"`
	// uploadNeedle is in every upload's chain and in no other.
	uploadNeedle = `"--publish-url"`
	// buildNeedle is in the build's chain, and so in every upload built on it.
	buildNeedle = `"--out-dir"`
)

// publishOn is the module constructed on a commit the engine fetched, with the
// files uv build writes declared under distDir.
func publishOn(t *testing.T, built ...string) *FoundryTools {
	t.Helper()
	engine.reset()
	tree := map[string]string{"pyproject.toml": "[project]\nname = \"stellar_core\"\n"}
	for _, f := range built {
		tree[distDir+"/"+f] = "artifact"
	}
	engine.withTree(tree)
	return &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/rob/stellar_core.git", Sha: buildSha}
}

func publishWith(t *testing.T, m *FoundryTools, token *dagger.Secret, indexURL string, dryRun bool) {
	t.Helper()
	if err := m.Publish(context.Background(), token, publishURL, checkURL, "publisher", indexURL, nil, npmHosted, dryRun); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

// publishes runs the lane the way a landing does: the publisher's password and
// the build index.
func publishes(t *testing.T, m *FoundryTools) {
	t.Helper()
	publishWith(t, m, dag.SetSecret("pypi-token", publishPass), buildIndex, false)
}

// indexAnswers scripts the probe's answer: the project page, then its status.
func indexAnswers(page, status string) { engine.stdout(probeNeedle, page+"\n"+status) }

func uploadOf(file string) string { return engine.chain(uploadNeedle, `"`+distDir+"/"+file+`"`) }

func noUploads(t *testing.T) {
	t.Helper()
	if chain := engine.chain(uploadNeedle); chain != "" {
		t.Fatalf("something was uploaded:\n%s", chain)
	}
}

// probes counts the probes that ran: each is read for its exit code once.
func probes() int {
	n := 0
	for _, q := range engine.chains() {
		if strings.Contains(q, probeNeedle) && strings.Contains(q, "exitCode") {
			n++
		}
	}
	return n
}

// A tree the engine did not fetch has no commit to publish: could-not-run, and
// nothing is built.
func TestThePublishLaneRefusesATreeItDidNotFetch(t *testing.T) {
	engine.reset()
	publishes(t, &FoundryTools{Source: dag.Directory()})
	settledOn(t, "2", "--repo and --sha")
	if engine.chain(buildNeedle) != "" {
		t.Error("a tree the engine did not fetch was built")
	}
}

func TestAPublishWithNoTokenBuildsNothing(t *testing.T) {
	m := publishOn(t, publishSdist, publishWheel)
	publishWith(t, m, nil, buildIndex, false)
	settledOn(t, "2", "--token is required")
	if engine.chain(buildNeedle) != "" {
		t.Error("a publish that could never upload built anyway")
	}
}

// The trigger is the tree: a landing that did not bump the version finds every
// file on the index and does nothing.
func TestAReleasedVersionUploadsNothing(t *testing.T) {
	m := publishOn(t, publishSdist, publishWheel, ".gitignore")
	indexAnswers(`<a href="../../packages/stellar-core/1.43.0/`+publishWheel+`#sha256=aa">`+publishWheel+`</a>`+
		`<a href="../../packages/stellar-core/1.43.0/`+publishSdist+`#sha256=bb">`+publishSdist+`</a>`, "200")
	publishes(t, m)
	settledOn(t, "0", "nothing to do")
	noUploads(t)
	// The index is asked by its PEP 503 name, on the hosted root.
	wantCalls(t, engine.chain(probeNeedle), []string{"withExec", `"curl"`, `"` + checkURL + `stellar-core/"`})
}

// A version the index does not list uploads every artifact, as the publisher,
// with the password handed to uv as a secret and never in the clear. The
// counts in the settle carry a leading space so that a negative count cannot
// satisfy them.
func TestANewVersionUploadsEveryArtifactWithThePasswordAsASecret(t *testing.T) {
	m := publishOn(t, publishSdist, publishWheel, ".gitignore")
	indexAnswers(`<a href="../../packages/stellar-core/1.42.0/stellar_core-1.42.0.tar.gz#sha256=aa">stellar_core-1.42.0.tar.gz</a>`, "200")
	publishes(t, m)
	settledOn(t, "0", " 2 uploaded, 0 already released")
	for _, f := range []string{publishSdist, publishWheel} {
		chain := uploadOf(f)
		if chain == "" {
			t.Fatalf("%s was not uploaded", f)
		}
		wantCalls(t, chain,
			[]string{"withEnvVariable", `"UV_PUBLISH_USERNAME"`, `"publisher"`},
			[]string{"withSecretVariable", `"UV_PUBLISH_PASSWORD"`},
			[]string{"withEnvVariable", `"PUBLISH_UPLOADED_AT"`},
			[]string{"withExec", `"uv"`, `"publish"`, `"` + publishURL + `"`, `"--check-url"`, `"` + checkURL + `"`},
		)
		if strings.Contains(chain, publishPass) {
			t.Errorf("the password reached the upload in the clear:\n%s", chain)
		}
	}
	if uploadOf(".gitignore") != "" {
		t.Error("a file that is neither a wheel nor an sdist was uploaded")
	}
	if n := probes(); n != 1 {
		t.Errorf("the index was asked %d times about one project, want once", n)
	}
}

func TestOnlyTheFilesTheIndexLacksAreUploaded(t *testing.T) {
	m := publishOn(t, publishSdist, publishWheel)
	indexAnswers(`<a href="../../packages/stellar-core/1.43.0/`+publishSdist+`#sha256=bb">`+publishSdist+`</a>`, "200")
	publishes(t, m)
	settledOn(t, "0", " 1 uploaded, 1 already released")
	if uploadOf(publishSdist) != "" {
		t.Error("a released sdist was uploaded again")
	}
	if uploadOf(publishWheel) == "" {
		t.Error("the wheel the index lacks was not uploaded")
	}
}

// A 404 is an answer: nothing of that name is released, and the check ran.
func TestAPackageTheIndexHasNeverSeenUploads(t *testing.T) {
	m := publishOn(t, publishSdist)
	indexAnswers("Not Found", "404")
	publishes(t, m)
	settledOn(t, "0", " 1 uploaded, 0 already released")
	if strings.Contains(engine.chain(`"/usr/local/bin/verdict"`), "did NOT run") {
		t.Error("a 404 was reported as a check that did not run")
	}
}

// An index that does not answer the probe leaves uv's --check-url as the only
// guard. The upload still goes, and the settle says the check did not run.
func TestAProbeTheIndexDoesNotAnswerUploadsAndSaysTheCheckDidNotRun(t *testing.T) {
	for name, script := range map[string]func(){
		"curl failed":     func() { engine.exitCode(probeNeedle, 7) },
		"an error status": func() { indexAnswers("Service Unavailable", "503") },
		"no status line":  func() { engine.stdout(probeNeedle, "<html>garbled") },
		"the engine lost the probe": func() {
			engine.failLeaf(probeNeedle, "exitCode", "connection to the engine was lost")
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := publishOn(t, publishSdist)
			script()
			publishes(t, m)
			settledOn(t, "0", " 1 uploaded")
			settledOn(t, "0", "check did NOT run for stellar-core")
			if uploadOf(publishSdist) == "" {
				t.Error("an unanswered probe stopped the upload")
			}
		})
	}
}

func TestAnUploadTheIndexRefusesIsFindings(t *testing.T) {
	m := publishOn(t, publishSdist)
	indexAnswers("", "404")
	engine.exitCode(uploadNeedle, 2)
	engine.stderr(uploadNeedle, "error: Failed to publish `/dist/"+publishSdist+"` to "+publishURL+"\n  Caused by: Upload failed with status code 403 Forbidden.")
	publishes(t, m)
	settledOn(t, "1", "findings in uv publish "+publishSdist)
}

func TestAnUploadTheIndexNeverAnsweredCouldNotRun(t *testing.T) {
	m := publishOn(t, publishSdist)
	indexAnswers("", "404")
	engine.exitCode(uploadNeedle, 2)
	engine.stderr(uploadNeedle, "error: Failed to publish `/dist/"+publishSdist+"`\n  Caused by: tcp connect error: Connection refused (os error 111)")
	publishes(t, m)
	settledOn(t, "2", "network fault")
}

func TestABuildThatFailsAsksAndUploadsNothing(t *testing.T) {
	m := publishOn(t, publishSdist)
	engine.exitCode(buildNeedle, 1)
	engine.stderr(buildNeedle, "Failed to build `/src`\nThe build backend returned an error")
	publishes(t, m)
	settledOn(t, "1", "findings in uv build")
	noUploads(t)
	if engine.chain(probeNeedle) != "" {
		t.Error("the index was asked about a build that failed")
	}
}

func TestABuildThatWritesNoArtifactIsFindings(t *testing.T) {
	m := publishOn(t, ".gitignore")
	publishes(t, m)
	settledOn(t, "1", "no wheel or sdist")
	noUploads(t)
}

func TestADryRunAsksTheIndexAndUploadsNothing(t *testing.T) {
	m := publishOn(t, publishSdist, publishWheel)
	indexAnswers("", "404")
	publishWith(t, m, nil, buildIndex, true)
	settledOn(t, "0", " 2 would upload")
	noUploads(t)
	if engine.chain(probeNeedle) == "" {
		t.Error("a dry run must ask the index for real")
	}
}

// The build runs in the python lane and resolves its backend through the index
// it is handed — and through uv's own resolution when it is handed none.
func TestTheBuildResolvesThroughTheIndexItIsHanded(t *testing.T) {
	m := publishOn(t, publishSdist)
	indexAnswers("", "404")
	publishes(t, m)
	wantCalls(t, engine.chain(buildNeedle),
		[]string{"from", `"` + checks.ImagePython + `"`},
		[]string{"withEnvVariable", `"UV_INDEX_URL"`, `"` + buildIndex + `"`},
		[]string{"withExec", `"uv"`, `"build"`, `"` + distDir + `"`},
	)

	m = publishOn(t, publishSdist)
	indexAnswers("", "404")
	publishWith(t, m, dag.SetSecret("pypi-token", publishPass), "", false)
	if chain := engine.chain(buildNeedle); hasCall(chain, "withEnvVariable", `"UV_INDEX_URL"`) {
		t.Errorf("no index was handed, and the build was pointed at one anyway:\n%s", chain)
	}
}
