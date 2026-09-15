package publishlane

import (
	"encoding/base64"
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
	want := Npm{Name: "@forge/stellar-core-ts", Version: "0.6.0", Builds: true, Registry: "https://nexus.example/repository/npm-hosted/"}
	if got != want {
		t.Fatalf("NpmOf = %+v, want %+v", got, want)
	}

	for name, tc := range map[string]struct {
		manifest string
		want     Npm
	}{
		"a private root":             {`{"name": "theia", "private": true}`, Npm{Name: "theia", Private: true}},
		"a workspace list":           {`{"name": "theia", "workspaces": ["apps/*"]}`, Npm{Name: "theia", Workspaces: true}},
		"an empty workspace list":    {`{"name": "x", "workspaces": []}`, Npm{Name: "x", Workspaces: true}},
		"a null workspace":           {`{"name": "x", "workspaces": null}`, Npm{Name: "x"}},
		"a blank prepublishOnly":     {`{"name": "x", "scripts": {"prepublishOnly": "  "}}`, Npm{Name: "x"}},
		"no scripts, no registry":    {`{"name": "x", "version": "1.0.0"}`, Npm{Name: "x", Version: "1.0.0"}},
		"another script is no build": {`{"name": "x", "scripts": {"build": "tsc"}}`, Npm{Name: "x"}},
	} {
		got, err := NpmOf(tc.manifest)
		if err != nil || got != tc.want {
			t.Errorf("%s: NpmOf = %+v, %v; want %+v", name, got, err, tc.want)
		}
	}

	if _, err := NpmOf(`{"name": `); err == nil || !strings.Contains(err.Error(), "does not parse") {
		t.Errorf("a manifest that does not parse must say so: %v", err)
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
