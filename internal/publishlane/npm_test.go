package publishlane

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

func TestNpmOfReadsWhatDecidesAPublish(t *testing.T) {
	got, err := NpmOf(`{
		"name": "@forge/stellar-core-ts", "version": "0.6.0",
		"publishConfig": {"registry": "https://nexus.example/repository/npm-hosted/"},
		"scripts": {"build": "tsc", "prepublishOnly": "bun run build"}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	want := Npm{Name: "@forge/stellar-core-ts", Version: "0.6.0", PrepublishOnly: "bun run build", Registry: "https://nexus.example/repository/npm-hosted/"}
	if !reflect.DeepEqual(got, want) || !got.Builds() {
		t.Fatalf("NpmOf = %+v (builds %v), want %+v that builds", got, got.Builds(), want)
	}

	for name, tc := range map[string]struct {
		manifest string
		want     Npm
	}{
		"a private root":                  {`{"name": "theia", "private": true}`, Npm{Name: "theia", Private: true}},
		"a workspace list":                {`{"name": "theia", "workspaces": ["apps/*", "packages/*"]}`, Npm{Name: "theia", Workspaces: []string{"apps/*", "packages/*"}}},
		"a workspace object":              {`{"name": "y", "workspaces": {"packages": ["libs/*"]}}`, Npm{Name: "y", Workspaces: []string{"libs/*"}}},
		"an empty workspace list":         {`{"name": "x", "workspaces": []}`, Npm{Name: "x", Workspaces: []string{}}},
		"a null workspace":                {`{"name": "x", "workspaces": null}`, Npm{Name: "x"}},
		"no scripts, no registry":         {`{"name": "x", "version": "1.0.0"}`, Npm{Name: "x", Version: "1.0.0"}},
		"another script is no prepublish": {`{"name": "x", "scripts": {"build": "tsc"}}`, Npm{Name: "x"}},
	} {
		got, err := NpmOf(tc.manifest)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: NpmOf = %+v, %v; want %+v", name, got, err, tc.want)
		}
		if got.Builds() {
			t.Errorf("%s: a manifest with no prepublishOnly must not build", name)
		}
	}

	if blank, _ := NpmOf(`{"name": "x", "scripts": {"prepublishOnly": "  "}}`); blank.Builds() {
		t.Error("a blank prepublishOnly runs nothing, so it must not build")
	}
	for manifest, want := range map[string]string{
		`{"name": `:                      "does not parse",
		`{"name": "x", "workspaces": 3}`: "neither a list of globs",
	} {
		if _, err := NpmOf(manifest); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("NpmOf(%s) must refuse with %q: %v", manifest, want, err)
		}
	}
}

func TestPlainBuildIsOnlyThePackagesOwnBuildScript(t *testing.T) {
	for script, want := range map[string]bool{
		"bun run build":             true,
		"pnpm run build":            true,
		"npm run build":             true,
		"yarn run build":            true,
		"  bun run build  ":         true,
		"":                          false,
		"bun run build && bun test": false,
		"tsc":                       false,
		"bun run build:types":       false,
		"npx run build":             false,
	} {
		if got := PlainBuild(script); got != want {
			t.Errorf("PlainBuild(%q) = %v, want %v", script, got, want)
		}
	}
}

func TestPackumentURLEscapesTheScope(t *testing.T) {
	for _, tc := range []struct{ registry, name, want string }{
		{"https://nexus.example/repository/npm-hosted/", "@forge/stellar-core-ts", "https://nexus.example/repository/npm-hosted/@forge%2Fstellar-core-ts"},
		{"https://nexus.example/repository/npm-hosted", "left-pad", "https://nexus.example/repository/npm-hosted/left-pad"},
	} {
		if got := PackumentURL(tc.registry, tc.name); got != tc.want {
			t.Errorf("PackumentURL(%q, %q) = %q, want %q", tc.registry, tc.name, got, tc.want)
		}
	}
}

func TestNpmReleasedReadsTheVersionsNotTheText(t *testing.T) {
	packument := `{"name": "@forge/x", "dist-tags": {"latest": "0.10.0"},
		"versions": {"0.10.0": {"dependencies": {"y": "^0.1"}}, "0.5.0": {}}}`
	for version, want := range map[string]bool{"0.10.0": true, "0.5.0": true, "0.1": false, "0.6.0": false} {
		got, err := NpmReleased(packument, version)
		if err != nil || got != want {
			t.Errorf("NpmReleased(%q) = %v, %v; want %v", version, got, err, want)
		}
	}
	if got, err := NpmReleased(`{"name": "@forge/x"}`, "0.1.0"); err != nil || got {
		t.Errorf("a packument with no versions lists none: %v, %v", got, err)
	}
	if _, err := NpmReleased(`<html>Service Unavailable</html>`, "0.1.0"); err == nil {
		t.Error("a packument that does not parse must be an error, never an answer")
	}
}

func TestSameRegistryIgnoresCaseAndTheTrailingSlash(t *testing.T) {
	const hosted = "https://nexus.example/repository/npm-hosted/"
	for other, want := range map[string]bool{
		"https://nexus.example/repository/npm-hosted":  true,
		"HTTPS://Nexus.Example/repository/npm-hosted/": true,
		"https://evil.example/repository/npm-hosted/":  false,
		"https://nexus.example/repository/npm-group/":  false,
		"http://nexus.example/repository/npm-hosted/":  false,
		"not a url": false,
		"":          false,
	} {
		if got := SameRegistry(hosted, other); got != want {
			t.Errorf("SameRegistry(hosted, %q) = %v, want %v", other, got, want)
		}
	}
}

func TestTheAuthLineIsBasicAuthKeyedByTheRegistryPath(t *testing.T) {
	const key = "//nexus.example/repository/npm-hosted/"
	for _, registry := range []string{"https://nexus.example/repository/npm-hosted", "https://nexus.example/repository/npm-hosted/"} {
		if got, err := AuthKey(registry); err != nil || got != key {
			t.Errorf("AuthKey(%q) = %q, %v; want %q — a trailing slash must not change the key", registry, got, err, key)
		}
	}
	for _, registry := range []string{"nexus.example", "not a url", ""} {
		if got, err := AuthKey(registry); err == nil {
			t.Errorf("AuthKey(%q) = %q; a registry with no host must refuse", registry, got)
		}
	}
	want := key + ":_auth=" + base64.StdEncoding.EncodeToString([]byte("publisher:hunter3"))
	if got := AuthLine(key, "publisher", "hunter3"); got != want {
		t.Errorf("AuthLine = %q, want %q", got, want)
	}
}
