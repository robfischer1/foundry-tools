package publishlane

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// The npm half of the lane, ported from theia ci/publish.sh's contract (probe,
// auth, publish). It publishes ONE package: the tree's root package.json. A
// workspace root names no single package, and which of its packages release is
// the repository's decision — theia publishes four of its six public ones — so
// the lane refuses one by name rather than guessing.

// Npm is what the lane reads off a root package.json.
type Npm struct {
	Name, Version string
	Private       bool
	// Workspaces is true for any manifest that declares workspaces at all: the
	// lane refuses rather than decide which of them release.
	Workspaces bool
	// Builds is true when prepublishOnly runs something. `bun publish` runs it,
	// and it needs the lockfile's dependencies installed first.
	Builds bool
	// Registry is publishConfig.registry, "" when the manifest names none.
	Registry string
}

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
		return Npm{}, fmt.Errorf("package.json does not parse: %v", err)
	}
	workspaces := strings.TrimSpace(string(doc.Workspaces))
	return Npm{
		Name:       doc.Name,
		Version:    doc.Version,
		Private:    doc.Private,
		Workspaces: workspaces != "" && workspaces != "null",
		Builds:     strings.TrimSpace(doc.Scripts["prepublishOnly"]) != "",
		Registry:   doc.PublishConfig.Registry,
	}, nil
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
