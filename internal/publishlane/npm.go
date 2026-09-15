package publishlane

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// The npm half of the lane, ported from theia ci/publish.sh's contract (probe,
// auth, publish). A plain root publishes its own package.json. A workspace root
// publishes the members that declare where they go — a non-private package.json
// with publishConfig.registry — and never itself (Rob, 2026-09-15: theia's six
// such packages all publish).

// Npm is what the lane reads off one package.json.
type Npm struct {
	Name, Version string
	Private       bool
	// Workspaces are the member globs a workspace root declares ("packages/*"),
	// in either spelling npm accepts: a list, or {"packages": [...]}.
	Workspaces []string
	// PrepublishOnly is the script a publish runs first, verbatim.
	PrepublishOnly string
	// Registry is publishConfig.registry, "" when the manifest names none.
	Registry string
}

// Builds answers whether publishing runs something first, which needs the
// lockfile's dependencies installed.
func (n Npm) Builds() bool { return strings.TrimSpace(n.PrepublishOnly) != "" }

// NpmOf reads a package.json.
func NpmOf(manifest string) (Npm, error) {
	var doc struct {
		Name          string            `json:"name"`
		Version       string            `json:"version"`
		Private       bool              `json:"private"`
		Workspaces    json.RawMessage   `json:"workspaces"`
		Scripts       map[string]string `json:"scripts"`
		PublishConfig struct {
			Registry string `json:"registry"`
		} `json:"publishConfig"`
	}
	if err := json.Unmarshal([]byte(manifest), &doc); err != nil {
		return Npm{}, fmt.Errorf("does not parse: %v", err)
	}
	workspaces, err := workspaceGlobs(doc.Workspaces)
	if err != nil {
		return Npm{}, err
	}
	return Npm{
		Name:           doc.Name,
		Version:        doc.Version,
		Private:        doc.Private,
		Workspaces:     workspaces,
		PrepublishOnly: doc.Scripts["prepublishOnly"],
		Registry:       doc.PublishConfig.Registry,
	}, nil
}

// workspaceGlobs reads workspaces in either spelling. Absent or null is no
// workspace at all.
func workspaceGlobs(raw json.RawMessage) ([]string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	var object struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf(`does not parse: workspaces is neither a list of globs nor {"packages": [...]}`)
	}
	return object.Packages, nil
}

var plainBuild = regexp.MustCompile(`^(bun|npm|pnpm|yarn) run build$`)

// PlainBuild answers whether a prepublishOnly does nothing but run the package's
// own build script — the one the lane may run itself, through turbo when the
// workspace has it, so the package's workspace dependencies build first. The
// package manager it names does not matter: theia's aglaia and plugin-contract
// say `pnpm run build`, and the lane image carries no pnpm.
func PlainBuild(prepublishOnly string) bool {
	return plainBuild.MatchString(strings.TrimSpace(prepublishOnly))
}

// PackumentURL is where a registry answers a package's packument. The scope's
// slash is escaped: the per-version document is not served (theia measured a
// 404 for a version that exists), the packument is.
func PackumentURL(registry, name string) string {
	return strings.TrimSuffix(registry, "/") + "/" + strings.ReplaceAll(name, "/", "%2F")
}

// NpmReleased answers whether a packument lists this exact version. It parses
// rather than matches text: "0.1" is inside "0.10.0", and a version string also
// appears in dependency ranges.
func NpmReleased(packument, version string) (bool, error) {
	var doc struct {
		Versions map[string]json.RawMessage `json:"versions"`
	}
	if err := json.Unmarshal([]byte(packument), &doc); err != nil {
		return false, fmt.Errorf("the registry's packument does not parse: %v", err)
	}
	_, ok := doc.Versions[version]
	return ok, nil
}

// SameRegistry answers whether two registry URLs name the same place: scheme and
// host without regard to case, path without its trailing slash.
func SameRegistry(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	if errA != nil || errB != nil || ua.Host == "" || ub.Host == "" {
		return false
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) &&
		strings.EqualFold(ua.Host, ub.Host) &&
		strings.TrimSuffix(ua.Path, "/") == strings.TrimSuffix(ub.Path, "/")
}

// AuthKey is a registry's .npmrc key: the URL without its scheme, with exactly
// one trailing slash. npm ignores an auth line whose path does not match the
// registry it is talking to, so the key is derived from the one URL rather than
// spelled a second time. The registry is the door's configuration, so one with
// no host is the lane's to refuse before it judges any tree against it.
func AuthKey(registry string) (string, error) {
	u, err := url.Parse(registry)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("the registry %q is not a URL", registry)
	}
	return "//" + u.Host + strings.TrimSuffix(u.Path, "/") + "/", nil
}

// AuthLine is the .npmrc line that logs user in under key with HTTP Basic.
// `_auth`, not `_authToken`: Nexus's npm bearer realm is not active, so a bearer
// line is a 401 that names nothing (theia ci/publish.sh has the measurement).
func AuthLine(key, user, password string) string {
	return key + ":_auth=" + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}
