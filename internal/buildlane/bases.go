package buildlane

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// BASE IMAGES, AS THE IMAGE LANE BUILDS THEM (Rob, 2026-09-14: "CI base images
// go away. The entire base image DAG goes away. Each language has one base
// image"; "foundry/base-images ... subdirectories"; "a thin dagger module that
// runs base images through the image lane and runs trivy at the end").
//
// A repository of base images holds one directory per base under bases/, each
// with its own Dockerfile, and no Dockerfile at its root. Every base builds
// against the whole tree, so a builder stage can compile source that sits
// beside the bases instead of copying it out of
// another published image — the edge that made the old bases a DAG.

// BasesDir is where a repository of base images keeps them.
const BasesDir = "bases"

// BaseDockerfile is a base's Dockerfile, relative to the tree's root.
func BaseDockerfile(base string) string { return BasesDir + "/" + base + "/Dockerfile" }

// CustodyPath answers the path a repository URL or custody key names, without
// scheme, host or .git: http://ourea…:8215/foundry/base-images.git and
// foundry/base-images are both foundry/base-images.
func CustodyPath(repo string) string {
	p := strings.TrimSpace(repo)
	if u, err := url.Parse(p); err == nil && u.Host != "" {
		p = u.Path
	}
	return strings.TrimSuffix(strings.Trim(p, "/"), ".git")
}

// BasePushRepo answers where a base is pushed: the repository's own custody
// path on the lane's registry, then the base's name —
// registry.notusmi.com/foundry/base-images/go.
func BasePushRepo(registry, repo, base string) string {
	return registry + "/" + CustodyPath(repo) + "/" + base
}

// BaseChanges answers the lines of `git diff --name-only` output that concern
// one base: its own directory. The rest of
// the repository — another base, the README — is not this base's change.
func BaseChanges(base, changed string) string {
	own := BasesDir + "/" + base + "/"
	var out []string
	for _, p := range strings.Split(changed, "\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, own) {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}

// trivyReport is the part of trivy's JSON the gate reads.
type trivyReport struct {
	Results []struct {
		Target          string `json:"Target"`
		Vulnerabilities []struct {
			VulnerabilityID  string `json:"VulnerabilityID"`
			PkgName          string `json:"PkgName"`
			InstalledVersion string `json:"InstalledVersion"`
			FixedVersion     string `json:"FixedVersion"`
			Severity         string `json:"Severity"`
		} `json:"Vulnerabilities"`
	} `json:"Results"`
}

// TrivyFindings reads a trivy JSON report — asked for HIGH and CRITICAL with
// --ignore-unfixed, so every vulnerability in it has a fix — and answers one
// sorted line per finding. A report that is not JSON is an error: trivy that
// wrote nothing readable did not scan.
func TrivyFindings(raw []byte) ([]string, error) {
	var report trivyReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, fmt.Errorf("trivy's report is not JSON: %w", err)
	}
	var out []string
	for _, r := range report.Results {
		for _, v := range r.Vulnerabilities {
			out = append(out, fmt.Sprintf("%s %s %s %s -> %s (%s)",
				v.Severity, v.VulnerabilityID, v.PkgName, v.InstalledVersion, v.FixedVersion, r.Target))
		}
	}
	sort.Strings(out)
	return out, nil
}
