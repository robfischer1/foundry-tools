package pinsync

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
)

func fetchEnv(web *fakeWeb) (*Env, *bytes.Buffer) {
	log := &bytes.Buffer{}
	return &Env{HTTP: &http.Client{Transport: web}, GitHubAPI: "https://api.test", Log: log}, log
}

// A publisher that states nothing (no sidecar, not a GitHub release) leaves the
// measured sum standing.
func TestAnAssetNobodyStatesASumForKeepsTheMeasuredOne(t *testing.T) {
	web := &fakeWeb{bodies: map[string]string{"https://dl.test/tool": "bytes"}}
	e, _ := fetchEnv(web)
	got, err := e.sumOf(context.Background(), "https://dl.test/tool")
	if err != nil || got != sum("bytes") {
		t.Fatalf("sumOf = %q, %v", got, err)
	}
	if strings.Join(web.asked, " ") != "https://dl.test/tool https://dl.test/tool.sha256" {
		t.Errorf("asked %v", web.asked)
	}
}

// What the publisher states but cannot be read is logged, never trusted, and
// never fatal: the measured sum is still the asset's.
func TestAStatementThatCannotBeReadIsLoggedAndSkipped(t *testing.T) {
	const u = "https://github.com/o/r/releases/download/v1/tool"
	for _, tc := range []struct {
		name string
		web  map[string]string
		logs string
	}{
		{"the release cannot be read", map[string]string{}, "could not read the release"},
		{"its checksums file cannot be read", map[string]string{
			"https://api.test/repos/o/r/releases/tags/v1": `{"tag_name":"v1","assets":[{"name":"tool"},{"name":"checksums.txt","browser_download_url":"https://gone"}]}`,
		}, "checksums.txt: GET https://gone: Not Found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.web[u] = "bytes"
			e, log := fetchEnv(&fakeWeb{bodies: tc.web})
			got, err := e.sumOf(context.Background(), u)
			if err != nil || got != sum("bytes") {
				t.Fatalf("sumOf = %q, %v", got, err)
			}
			if !strings.Contains(log.String(), tc.logs) {
				t.Errorf("log %q lacks %q", log, tc.logs)
			}
		})
	}
}

// A checksums file is read for the asset's own line: another asset's line, a
// malformed line and a bare hash are not a statement about it.
func TestSumsLineReadsOnlyTheAssetsOwnLine(t *testing.T) {
	file := []byte(sum("a") + "  other\nnot a line\n" + strings.ToUpper(sum("b")) + "  tool\n" + sum("c") + "\n" + sum("d") + " *dist/tool\n")
	if got := sumsLine(file, "tool"); got != sum("d") {
		t.Errorf("sumsLine = %q, want the dist/tool line", got)
	}
	if got := sumsLine(file, "absent"); got != "" {
		t.Errorf("an asset the file does not list: %q", got)
	}
}

// The token rides the API calls only, never an asset download, which may be
// redirected to a host that has no business seeing it.
func TestTheTokenGoesToTheAPIOnly(t *testing.T) {
	const u = "https://github.com/o/r/releases/download/v1/tool"
	web := &fakeWeb{bodies: map[string]string{u: "bytes", "https://api.test/repos/o/r/releases/tags/v1": `{"tag_name":"v1"}`}}
	e, _ := fetchEnv(web)
	e.Token = "tok"
	if _, err := e.sumOf(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	for i, asked := range web.asked {
		want := ""
		if strings.HasPrefix(asked, "https://api.test/") {
			want = "Bearer tok"
		}
		if web.auth[i] != want {
			t.Errorf("%s carried %q, want %q", asked, web.auth[i], want)
		}
	}
}
