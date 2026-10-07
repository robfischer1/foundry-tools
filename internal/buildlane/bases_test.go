package buildlane

import (
	"strings"
	"testing"
)

func TestABaseBuildsFromItsOwnDirectoryUnderBases(t *testing.T) {
	if got := BaseDockerfile("go"); got != "bases/go/Dockerfile" {
		t.Fatalf("BaseDockerfile(go) = %q", got)
	}
}

// The push repo is the repository's own custody path plus the base, whichever
// spelling of the repository the lane was handed.
func TestABaseIsPushedUnderItsRepositorysPath(t *testing.T) {
	for repo, want := range map[string]string{
		"http://ourea.default.svc.cluster.local:8215/foundry/base-images.git": "registry.notusmi.com/foundry/base-images/go",
		"foundry/base-images":       "registry.notusmi.com/foundry/base-images/go",
		"/foundry/base-images.git/": "registry.notusmi.com/foundry/base-images/go",
		" foundry/base-images.git ": "registry.notusmi.com/foundry/base-images/go",
	} {
		if got := BasePushRepo("registry.notusmi.com", repo, "go"); got != want {
			t.Errorf("BasePushRepo(%q) = %q, want %q", repo, got, want)
		}
	}
}

// A base's change set is its own directory; another
// base's directory, and a path that merely starts with the base's name, are
// not.
func TestABasesChangesAreItsDirectory(t *testing.T) {
	changed := strings.Join([]string{
		"bases/go/Dockerfile",
		"bases/gopher/Dockerfile",
		"bases/rust/Dockerfile",
		"docs/notes.md",
		"README.md",
		"",
		"  bases/go/extra.txt  ",
	}, "\n")
	if got, want := BaseChanges("go", changed), "bases/go/Dockerfile\nbases/go/extra.txt"; got != want {
		t.Fatalf("BaseChanges(go) = %q, want %q", got, want)
	}
	if got := BaseChanges("bun", "bases/go/Dockerfile\nREADME.md"); got != "" {
		t.Fatalf("another base's change was bun's: %q", got)
	}
}

func TestTrivyFindingsAreOneSortedLinePerVulnerability(t *testing.T) {
	raw := `{"Results":[
	  {"Target":"img (debian 12)","Vulnerabilities":[
	    {"VulnerabilityID":"CVE-2","PkgName":"zlib","InstalledVersion":"1","FixedVersion":"2","Severity":"HIGH"},
	    {"VulnerabilityID":"CVE-1","PkgName":"libc","InstalledVersion":"3","FixedVersion":"4","Severity":"CRITICAL"}]},
	  {"Target":"usr/bin/x","Vulnerabilities":null}]}`
	got, err := TrivyFindings([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"CRITICAL CVE-1 libc 3 -> 4 (img (debian 12))",
		"HIGH CVE-2 zlib 1 -> 2 (img (debian 12))",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("findings = %q, want %q", got, want)
	}
	clean, err := TrivyFindings([]byte(`{"Results":[]}`))
	if err != nil || len(clean) != 0 {
		t.Fatalf("a clean report: %v %v", clean, err)
	}
	if _, err := TrivyFindings([]byte("trivy crashed")); err == nil {
		t.Fatal("a report that is not JSON read as a scan")
	}
}
