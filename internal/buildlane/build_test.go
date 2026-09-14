package buildlane

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestOnlyNonInertChangesBuild(t *testing.T) {
	changed := "README.md\ndocs/a.md\n.forgejo/build-args.env\nstar.toml\nsrc/main.go\n\nDockerfile\nnested/justfile\ninfra/prod.tfvars\n.claude/settings.json\n"
	got := NonInert(changed)
	want := []string{"src/main.go", "Dockerfile", "nested/justfile"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NonInert = %v, want %v", got, want)
	}
	if got := NonInert("LICENSE\n.gitignore\nrules/sast/x.yml\n"); got != nil {
		t.Fatalf("an inert-only push builds: %v", got)
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
	} {
		if got := PushRepo("registry.notusmi.com", c.image); got != c.want {
			t.Errorf("PushRepo(%q) = %q, want %q", c.image, got, c.want)
		}
	}
	pin, err := GPin("registry.notusmi.com/rob/ares", "0123456789abcdef0123")
	if err != nil || pin != "registry.notusmi.com/rob/ares:g0123456789ab" {
		t.Fatalf("GPin = %q, %v", pin, err)
	}
	if _, err := GPin("r", "abc"); err == nil {
		t.Fatal("a short commit made a g-pin")
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
	builder := `{"components":[{"name":"b","version":"1","purl":"pkg:x/b@1","from":"builder"},{"name":"a","version":"2"}]}`
	merged, in, bn, mn, err := MergeSBOM([]byte(image), []byte(builder))
	if err != nil {
		t.Fatal(err)
	}
	if in != 2 || bn != 2 || mn != 3 {
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
	if doc.BomFormat != "CycloneDX" || !reflect.DeepEqual(keys, []string{"a@2", "noversion@?", "pkg:x/b@1"}) {
		t.Fatalf("merged %s", merged)
	}
	if _, _, _, _, err := MergeSBOM([]byte("{"), []byte(builder)); err == nil {
		t.Fatal("a broken image SBOM merged")
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
	built := "0123456789abcdef"
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
		{"superseded", 200, tool(true, "no CI artifact at gfedcba9876543"), CouldNotRun, "SUPERSEDED"},
		{"refused", 200, tool(true, "no CI artifact at g0123456789ab"), Findings, "PERMIT REFUSED"},
		{"no-op", 200, tool(false, `{"no_op":true}`), Clean, "no-op"},
		{"stamped", 200, tool(false, `{"digest":"sha256:d","pushed_ref":"r:stable"}`), Clean, "digest=sha256:d, ref=r:stable"},
	} {
		got, reason := Permit(c.status, c.raw, built)
		if got != c.want || !strings.Contains(reason, c.reason) {
			t.Errorf("%s: %d %q, want %d containing %q", c.name, got, reason, c.want, c.reason)
		}
	}
}
