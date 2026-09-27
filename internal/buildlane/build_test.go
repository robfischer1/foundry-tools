package buildlane

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestOnlyNonInertChangesBuild(t *testing.T) {
	changed := "README.md\ndocs/a.md\n.forgejo/build-args.env\nstar.toml\nsrc/main.go\n\nDockerfile\nnested/justfile\ninfra/prod.tfvars\n.claude/settings.json\nnested/stages.just\nhooks/git_guard/git_guard.py\nhooks/pre-push.d/x\n"
	got := NonInert(changed)
	want := []string{"src/main.go", "Dockerfile", "nested/justfile", "nested/stages.just", "hooks/git_guard/git_guard.py", "hooks/pre-push.d/x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NonInert = %v, want %v", got, want)
	}
	if got := NonInert("LICENSE\n.gitignore\nrules/sast/x.yml\n"); got != nil {
		t.Fatalf("an inert-only push builds: %v", got)
	}
	// The hook files: a fleet re-lay of stages.just must not roll every star.
	if got := NonInert("stages.just\nhooks/pre-commit\nhooks/pre-push\ncliff.toml\n"); got != nil {
		t.Fatalf("a hook-files-only push builds: %v", got)
	}
}

func TestTheDeclaredImageComesFromComposeOrTheStarsName(t *testing.T) {
	for _, c := range []struct{ compose, want string }{
		{"services:\n  a:\n    image: registry.notusmi.com/rob/ares:stable\n", "registry.notusmi.com/rob/ares:stable"},
		{"image: forgejo.notusmi.com/rob/ares\n", "forgejo.notusmi.com/rob/ares:latest"},
		{"image: docker.io/library/postgres:18\n", "registry.notusmi.com/rob/star:latest"},
		{"", "registry.notusmi.com/rob/star:latest"},
	} {
		if got := DeclaredImage(c.compose, "star"); got != c.want {
			t.Errorf("DeclaredImage(%q) = %q, want %q", c.compose, got, c.want)
		}
	}
}

func TestThePushRepoIsTheRegistryAndThePathWithoutTheTag(t *testing.T) {
	for _, c := range []struct{ image, want string }{
		{"registry.notusmi.com/rob/ares:stable", "registry.notusmi.com/rob/ares"},
		{"forgejo.notusmi.com/rob/nested/ares:latest", "registry.notusmi.com/rob/nested/ares"},
		{"registry.notusmi.com/rob/ares", "registry.notusmi.com/rob/ares"},
		// No slash and no colon at all: nothing to cut.
		{"ares", "registry.notusmi.com/ares"},
		{"ares:tag", "registry.notusmi.com/ares"},
	} {
		if got := PushRepo("registry.notusmi.com", c.image); got != c.want {
			t.Errorf("PushRepo(%q) = %q, want %q", c.image, got, c.want)
		}
	}
	pin, err := GPin("registry.notusmi.com/rob/ares", "0123456789abcdef0123")
	if err != nil || pin != "registry.notusmi.com/rob/ares:g0123456789ab" {
		t.Fatalf("GPin = %q, %v", pin, err)
	}
	// Twelve characters is the g-pin whole, and enough.
	if pin, err := GPin("r", "0123456789ab"); err != nil || pin != "r:g0123456789ab" {
		t.Fatalf("a twelve-character commit: %q, %v", pin, err)
	}
	if _, err := GPin("r", "0123456789a"); err == nil {
		t.Fatal("an eleven-character commit made a g-pin")
	}
}

func TestBuildArgsSkipBlanksAndCommentsAndKeepLinesVerbatim(t *testing.T) {
	got := BuildArgs("# comment\nA=1\n\nB=two words\r\nC= spaced \n")
	want := []string{"A=1", "B=two words", "C= spaced "}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildArgs = %q, want %q", got, want)
	}
}

func TestTheRegistryLoginReadsEitherSpellingAndNeverLeaksIt(t *testing.T) {
	cfg := `{"auths":{"registry.notusmi.com":{"username":"publisher","password":"hunter2"}}}`
	if u, p, err := RegistryLogin(cfg, "registry.notusmi.com"); err != nil || u != "publisher" || p != "hunter2" {
		t.Fatalf("username/password: %q %q %v", u, p, err)
	}
	auth := base64.StdEncoding.EncodeToString([]byte("publisher:hunter2"))
	cfg = `{"auths":{"registry.notusmi.com":{"auth":"` + auth + `"}}}`
	if u, p, err := RegistryLogin(cfg, "registry.notusmi.com"); err != nil || u != "publisher" || p != "hunter2" {
		t.Fatalf("auth pair: %q %q %v", u, p, err)
	}
	for _, bad := range []string{
		`not json hunter2`,
		`{"auths":{"other.host":{"username":"publisher","password":"hunter2"}}}`,
		`{"auths":{"registry.notusmi.com":{"username":"publisher"}}}`,
		`{"auths":{"registry.notusmi.com":{"auth":"%%%hunter2"}}}`,
	} {
		_, _, err := RegistryLogin(bad, "registry.notusmi.com")
		if err == nil {
			t.Errorf("accepted %q", bad)
			continue
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("the error carries the credential: %v", err)
		}
	}
}

func TestTheBuilderStageAndDigestAreRead(t *testing.T) {
	if !HasBuilderStage("FROM golang:1.26 AS builder\nRUN x\nFROM scratch\n") || !HasBuilderStage("from x as BUILDER  \n") {
		t.Fatal("a builder stage was missed")
	}
	if HasBuilderStage("FROM x AS builder-cache\nFROM scratch\n") {
		t.Fatal("a stage merely starting with builder counted")
	}
	d := "sha256:" + strings.Repeat("a", 64)
	if got := DigestOf("registry.notusmi.com/rob/x:g1@" + d + "\n"); got != d {
		t.Fatalf("DigestOf = %q", got)
	}
	if DigestOf("no digest") != "" {
		t.Fatal("a digest out of nothing")
	}
}

func TestTheSBOMMergeKeepsOneComponentPerKeyTheImagesFirst(t *testing.T) {
	image := `{"bomFormat":"CycloneDX","components":[{"name":"b","version":"1","purl":"pkg:x/b@1","from":"image"},{"name":"noversion"}]}`
	builder := `{"components":[{"name":"b","version":"1","purl":"pkg:x/b@1","from":"builder"},{"name":"a","version":"2"},{"version":"3"}]}`
	merged, in, bn, mn, err := MergeSBOM([]byte(image), []byte(builder))
	if err != nil {
		t.Fatal(err)
	}
	if in != 2 || bn != 3 || mn != 4 {
		t.Fatalf("counts %d %d %d", in, bn, mn)
	}
	var doc struct {
		BomFormat  string           `json:"bomFormat"`
		Components []map[string]any `json:"components"`
	}
	if err := json.Unmarshal(merged, &doc); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, c := range doc.Components {
		keys = append(keys, componentKey(c))
		if c["purl"] == "pkg:x/b@1" && c["from"] != "image" {
			t.Fatalf("the builder's copy won: %v", c)
		}
	}
	if doc.BomFormat != "CycloneDX" || !reflect.DeepEqual(keys, []string{"?@3", "a@2", "noversion@?", "pkg:x/b@1"}) {
		t.Fatalf("merged %s", merged)
	}
	if _, _, _, _, err := MergeSBOM([]byte("{"), []byte(builder)); err == nil {
		t.Fatal("a broken image SBOM merged")
	}
	if _, _, _, _, err := MergeSBOM([]byte(image), []byte("{")); err == nil {
		t.Fatal("a broken builder SBOM merged")
	}
}

func TestAFailedStepIsCouldNotRunOnlyOnANetworkFault(t *testing.T) {
	if v, r := Failed("sign", "Error: GET https://registry: 503 Service Unavailable"); v != CouldNotRun || !strings.Contains(r, "503 Service Unavailable") {
		t.Fatalf("a registry outage: %d %q", v, r)
	}
	if v, r := Failed("publish", "failed to solve: dockerfile parse error"); v != Findings || !strings.Contains(r, "findings in publish") {
		t.Fatalf("a broken Dockerfile: %d %q", v, r)
	}
	digest := "sha256:" + strings.Repeat("c", 64)
	if v, _ := Failed("publish", "unexpected media type application/x for "+digest+": not found"); v != CouldNotRun {
		t.Fatal("a reaped blob read as findings")
	}
	// The registry's own 500 (seaweedfs down behind zot, 2026-09-18 10:49Z —
	// glaucus 50e9489) and the engine failing to load what it pulled through
	// it (harmonia a17f953, same minute): the outage's, not the tree's.
	for _, out := range []string{
		"pushing registry.notusmi.com/rob/glaucus:g50e9489e672d ERROR [6.2s]\n! unexpected status from POST request to https://registry.notusmi.com/v2/rob/glaucus/blobs/uploads/?mount=sha256:39dc: 500 Internal Server Error",
		"unexpected status from HEAD request to https://registry.notusmi.com/v2/rob/x/manifests/stable: 500 Internal Server Error",
		"failed to load container from converted ID: load Container@Co5O…: inputs: failed to content hash dockerfile copy: exit code: 1",
	} {
		if v, r := Failed("publish", out); v != CouldNotRun || !strings.Contains(r, "network fault") {
			t.Errorf("a registry or engine outage read as findings: %d %q for %q", v, r, out)
		}
	}
	// A bare number is not a phrase: a Dockerfile that echoes "500" is the
	// tree's own business.
	if v, _ := Failed("publish", "step 3/5: RUN echo 500 && exit 1"); v != Findings {
		t.Error("a bare 500 in a build log read as a fault")
	}
	// uv's wording, through reqwest, for an index it could not reach.
	for _, out := range []string{
		"error: Failed to publish `/dist/x-1.0.tar.gz`\n  Caused by: tcp connect error: Connection timed out (os error 110)",
		"  Caused by: dns error: failed to lookup address information: Temporary failure in name resolution",
		"  Caused by: operation timed out",
	} {
		if v, _ := Failed("uv publish", out); v != CouldNotRun {
			t.Errorf("an unreachable index read as findings: %q", out)
		}
	}
}

// The refusals are the pinned tools' own words, captured 2026-09-14 from
// cosign v3.1.1 and syft v1.33.0.
func TestAToolThatRefusesTheLanesArgumentsIsCouldNotRun(t *testing.T) {
	for _, out := range []string{
		"Error: unknown flag: --use-signing-config\nerror during command execution: unknown flag: --use-signing-config",
		"Error: unknown shorthand flag: 'Z' in -Z",
		"Error: requires at least 1 arg(s), only received 0",
		"Error: flag needs an argument: --key",
		"Error: unknown command \"bogus\" for \"cosign\"\nRun 'cosign --help' for usage.",
		"unknown flag: --bogus-flag",
		"WARNING: something first\nerror during command execution: unknown flag: --x",
	} {
		if v, r := ToolFailed("sign", out); v != CouldNotRun || !strings.Contains(r, "sign refused the lane's own arguments") {
			t.Errorf("%q: %d %q", out, v, r)
		}
	}
	if _, r := ToolFailed("sign", "Error: unknown flag: --use-signing-config\n"); !strings.Contains(r, "(Error: unknown flag: --use-signing-config)") {
		t.Errorf("the refusal is not named: %q", r)
	}
	// The same words in the middle of a line belong to someone else: BuildKit
	// refusing a Dockerfile's flag is a finding in the tree.
	if v, _ := ToolFailed("sign", "ERROR: failed to build: failed to solve: dockerfile parse error on line 2: unknown flag: --bogus"); v != Findings {
		t.Error("a Dockerfile's refused flag read as the lane's call")
	}
	if v, r := ToolFailed("sign", "Error: no signatures found"); v != Findings || !strings.Contains(r, "findings in sign") {
		t.Errorf("a signature that does not verify: %d %q", v, r)
	}
	if v, r := ToolFailed("sign (SBOM)", "Error: GET https://registry: 503 Service Unavailable"); v != CouldNotRun || !strings.Contains(r, "network fault") {
		t.Errorf("a network fault through a tool: %d %q", v, r)
	}
}

func TestTheCallOutputIsReadOrRefused(t *testing.T) {
	status, body, err := ParseCall("HTTP 403\n{\"detail\":\"no\"}")
	if err != nil || status != 403 || body != `{"detail":"no"}` {
		t.Fatalf("%d %q %v", status, body, err)
	}
	for _, bad := range []string{"", "hello\n{}", "HTTP abc\n{}"} {
		if _, _, err := ParseCall(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestThePermitAnswerFoldsIntoTheVerdict(t *testing.T) {
	tool := func(isError bool, text string) string {
		b, _ := json.Marshal(map[string]any{"isError": isError, "content": []map[string]string{{"type": "text", "text": text}}})
		return string(b)
	}
	// molded is mold's refusal verbatim — hephaestus internal/mold/mold.go,
	// the error it returns when no g-pin in the tip's history resolves. If
	// that sentence is ever reworded, these cases stop reading SUPERSEDED and
	// this test is the thing that notices.
	molded := func(tip string) string {
		return "mold iris: no CI artifact at the tip " + tip +
			" or the 3 commit(s) behind it — mold stamps, it does not build; run the build for the tip first"
	}
	built := "0123456789abcdef"
	// movedTip is a commit this build never claimed. It is deliberately dull
	// hex: a realistic sha literal read to detect-secrets as a high-entropy
	// string and redded the commit stage. That atom is retired (2026-09-25);
	// the dullness stays because the entropy was doing no work here anyway.
	movedTip := strings.Repeat("fe", 8)
	for _, c := range []struct {
		name   string
		status int
		raw    string
		want   int
		reason string
	}{
		{"no principal", 403, "forbidden: unidentifiable caller", CouldNotRun, "DERIVE a principal"},
		{"policy refused", 403, "not granted", Findings, "POLICY refused"},
		{"no cert", 401, "", CouldNotRun, "401"},
		{"hades down", 502, "", CouldNotRun, "HTTP 502"},
		{"not a tool answer", 200, "<html>", CouldNotRun, "not a tool answer"},
		// EVERY REFUSAL BELOW IS MOLD'S OWN SENTENCE, copied from hephaestus
		// internal/mold/mold.go. The fixtures this replaced read "no CI
		// artifact at g<12 hex>" — a message mold has never produced — so the
		// matcher and its test agreed with each other and with nothing else.
		{"superseded", 200, tool(true, molded(movedTip)), CouldNotRun, "SUPERSEDED"},
		{"refused", 200, tool(true, molded(built)), Findings, "PERMIT REFUSED"},
		// The tip and the build's sha name one commit at different lengths,
		// either way round. Both must read as the SAME commit: an abbreviation
		// is not a race.
		{"same commit, tip abbreviated", 200, tool(true, molded(built[:12])), Findings, "PERMIT REFUSED"},
		{"same commit, tip in full", 200, tool(true, molded(built+strings.Repeat("a", 24))), Findings, "PERMIT REFUSED"},
		{"refused at length", 200, tool(true, strings.Repeat("x", 400)), Findings, "promoted. " + strings.Repeat("x", 300)},
		{"no-op", 200, tool(false, `{"no_op":true}`), Clean, "no-op"},
		{"stamped", 200, tool(false, `{"digest":"sha256:d","pushed_ref":"r:stable"}`), Clean, "digest=sha256:d, ref=r:stable"},
		{"an answer with no content", 200, `{"isError":false,"content":[]}`, Clean, "digest=?, ref=?"},
	} {
		got, reason := Permit(c.status, c.raw, built)
		if got != c.want || !strings.Contains(reason, c.reason) {
			t.Errorf("%s: %d %q, want %d containing %q", c.name, got, reason, c.want, c.reason)
		}
		if c.name == "refused at length" && strings.Contains(reason, strings.Repeat("x", 301)) {
			t.Errorf("the refusal was not cut at 300 characters: %d", len(reason))
		}
	}
}

// A REGISTRY 5xx IS A FAULT, AND ORAS SPELLS IT WITH A COLON.
//
// Every case below is a real phrasing. The first is anvil's own output from
// cast-anvil-93f3819-b9kw5 (2026-09-16), where zot was restarting — rebuilding
// its 221-repo metadata DB, port 5000 not yet listening — and `oras push` to
// staging got a 502. Failed filed it as findings, so the lane told the
// operator "running again changes nothing" about the one failure where running
// again is the entire fix. The pattern already carried "502 Bad Gateway"; oras
// writes "502: Bad Gateway", and one character was the whole of it.
func TestARegistry5xxIsAFaultInEveryPhrasingATooUses(t *testing.T) {
	for _, out := range []string{
		`Error response from registry: HEAD "https://foundry.notusmi.com/v2/staging/app-anvil/manifests/sha256:d83549fa1f052d3c096894a1feb53230b811b90e1a07ffce13c426ead9d3144b": response status code 502: Bad Gateway`,
		`response status code 503: Service Unavailable`,
		`response status code 504: Gateway Timeout`,
		`Error: 502 Bad Gateway`,
		`Error: 503 Service Unavailable`,
	} {
		code, why := Failed("staging push", out)
		if code != CouldNotRun {
			t.Errorf("a registry 5xx must be could-not-run, got %d for %.80q", code, out)
		}
		if !strings.Contains(why, "run it again") {
			t.Errorf("the reason must tell the operator to run it again, got %q", why)
		}
	}
}

// The inverse, so the widened pattern cannot swallow a real finding: a 4xx is
// the registry answering, and the answer is about what the lane sent.
func TestAClientErrorIsStillAFinding(t *testing.T) {
	for _, out := range []string{
		`response status code 401: Unauthorized`,
		`response status code 404: Not Found`,
		`Error: manifest invalid`,
	} {
		if code, _ := Failed("staging push", out); code != Findings {
			t.Errorf("a client error is a finding, got %d for %q", code, out)
		}
	}
}

// A CONTENDED CACHE IS NOT THE TREE. Measured 2026-09-16 on tongs
// (cast-tongs-560a9f9-wrkxj): gavel's cast was resolving crates in the same
// seconds, both cargos unpacked sha2 into the shared registry volume, and the
// loser found .cargo-ok already written. The lane settled "findings in cargo
// build — running again changes nothing" about a failure where the next run
// reads a cache that is already correct.
func TestAContendedCargoCacheIsNotTheTree(t *testing.T) {
	for _, out := range []string{
		"error: failed to unpack package `sha2 v0.10.9`\nCaused by:\n  failed to open `/usr/local/cargo/registry/src/index.crates.io-1949cf8c6b5b557f/sha2-0.10.9/.cargo-ok`\nCaused by:\n  File exists (os error 17)",
		"error: failed to unpack package `serde v1.0.229`",
		"failed to open /usr/local/cargo/registry/src/index.crates.io-1949cf8c6b5b557f/typenum-1.20.1/.cargo-ok",
	} {
		code, why := Failed("cargo build", out)
		if code != CouldNotRun {
			t.Errorf("a contended cargo cache must be could-not-run, got %d for %.60q", code, out)
		}
		if !strings.Contains(why, "run it again") {
			t.Errorf("the reason must tell the operator to run it again, got %q", why)
		}
		// It must not claim the network was at fault, because it was not.
		if strings.Contains(why, "network fault") {
			t.Errorf("a cache race is not a network fault, got %q", why)
		}
	}
}

// The inverse, so the new pattern cannot swallow a real compile failure: these
// are the tree being wrong, and no amount of re-running fixes them.
func TestARealCargoFailureIsStillAFinding(t *testing.T) {
	for _, out := range []string{
		"error[E0308]: mismatched types\n  --> src/main.rs:4:5",
		"error: could not compile `tongs` (bin \"tongs\") due to 1 previous error",
		"error: failed to select a version for `sha2`.\n    ... required by package `tongs v0.3.1`",
		"error: the lock file needs to be updated but --locked was passed",
	} {
		if code, _ := Failed("cargo build", out); code != Findings {
			t.Errorf("a real cargo failure is a finding, got %d for %.60q", code, out)
		}
	}
}

// ToolFailed delegates to Failed, so the cast lane's own entry point (which
// calls ToolFailed, not Failed) must reach the same verdict.
func TestToolFailedAlsoSeesAContendedCache(t *testing.T) {
	out := "error: failed to unpack package `sha2 v0.10.9`"
	if code, _ := ToolFailed("cargo build", out); code != CouldNotRun {
		t.Errorf("ToolFailed must inherit the contended-cache verdict, got %d", code)
	}
}

// TestAFaultIsTransientAndANotFoundIsNot pins both sides of the fault
// classification, because the regexp is the only thing standing between a
// two-minute registry outage and a nine-rung re-ask ladder — and it got the
// second side wrong until 2026-09-26.
//
// THE TRANSIENT ROWS ARE INCIDENTS, not invented strings. Each one is a
// phrasing a real lane settled on: the anvil 502 (2026-09-16, zot rebuilding
// its metadata DB), the glaucus 500 and harmonia engine-load (2026-09-18,
// seaweedfs down ~2 min after dev01 rebooted). They are here so a later
// narrowing cannot quietly re-break the case the narrowing was for.
//
// THE PERMANENT ROWS ARE WHY THIS TEST EXISTS. A 4xx is the registry
// answering correctly about something absent, and `: not found` names a
// digest that is gone. Retrying either burns the ladder on a condition no
// re-ask can change — measured on foundry/base-images 92eb0db0 as five
// byte-identical settles, five of nine rungs, stopped only by the landing.
func TestAFaultIsTransientAndANotFoundIsNot(t *testing.T) {
	for _, c := range []struct {
		name string
		out  string
	}{
		{"oras writes a colon before the reason", "Error: failed to push: response status code 502: Bad Gateway"},
		{"a tool's 503, spaced", "Error: GET https://registry: 503 Service Unavailable"},
		{"buildkit's pusher on zot's own 500", "unexpected status from POST request to https://registry.notusmi.com/v2/foundry/base-images/go/blobs/uploads/abc: 500 Internal Server Error"},
		{"the engine cannot load what it pulled", "failed to load container from converted ID: failed to content hash dockerfile copy: exit code: 1"},
		{"the S3 backend refuses the connection", "dial tcp 10.43.1.2:8333: connection refused"},
		{"name resolution", "no such host"},
		{"the registry is rate limiting", "toomanyrequests: retry later"},
		{"a wrapper whose CAUSE is transient", "failed to resolve source metadata for registry.notusmi.com/foundry/base-images/go:stable: dial tcp: connection refused"},
		{"a blob upload's 502", "unexpected status from PUT request to https://registry/v2/x/blobs/uploads/y: 502 Bad Gateway"},
		{"a reaped blob, which the producing build re-pushes", "unexpected media type application/x for sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc: not found"},
	} {
		if v, why := Failed("build", c.out); v != CouldNotRun || !strings.Contains(why, "network fault") {
			t.Errorf("transient %q read as %d %q — a re-ask is the whole fix here", c.name, v, why)
		}
	}

	for _, c := range []struct {
		name string
		out  string
	}{
		{"a manifest that is not there", "unexpected status from GET request to https://registry.notusmi.com/v2/foundry/base-images/go/manifests/sha256:deadbeef: 404 Not Found"},
		{"credentials the registry rejects", "unexpected status from HEAD request to https://registry.notusmi.com/v2/x/manifests/latest: 401 Unauthorized"},
		{"a pull the registry forbids", "unexpected status from GET request to https://registry/v2/x/manifests/latest: 403 Forbidden"},
		{"the same wrapper, permanent cause", "failed to resolve source metadata for registry.notusmi.com/foundry/base-images/go:stable: not found"},
		{"a reference that resolves to nothing", "failed to resolve source metadata for registry/x:tag: registry/x:tag: not found"},
	} {
		if _, why := Failed("build", c.out); strings.Contains(why, "network fault") {
			t.Errorf("permanent %q read as a network fault (%q) — this spends the ladder on a condition no re-ask changes", c.name, why)
		}
	}
}
