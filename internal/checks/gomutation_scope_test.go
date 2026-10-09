package checks

import (
	"strings"
	"testing"
	"text/template"
)

func TestGoMutationScope(t *testing.T) {
	keys := func(units ...string) []UnitKey {
		var out []UnitKey
		for _, u := range units {
			out = append(out, UnitKey{Unit: u})
		}
		return out
	}
	cases := []struct {
		name   string
		dir    string
		misses []UnitKey
		listed string
		want   string
	}{
		{"the root module's changed packages", ".", keys(".", "internal/x"), "/src\n/src/internal/x\n/src/internal/y\n", ". ./internal/x"},
		{"a unit with no source is dropped", ".", keys("internal/x", "internal/onlytests"), "/src/internal/x\n", "./internal/x"},
		{"a nested module's directory is not the root module's", ".", keys("internal/x", "tools/forge/oci"), "/src/internal/x\n", "./internal/x"},
		{"a nested module's own units", "tools/forge", keys("tools/forge", "tools/forge/oci"), "/src/tools/forge\n/src/tools/forge/oci\n", ". ./oci"},
		{"nothing listed is the whole module", ".", keys("internal/x"), "", "./..."},
		{"nothing to grade is the whole module", ".", nil, "/src/internal/x\n", "./..."},
		{"a listing outside the tree names nothing", ".", keys("internal/x"), "/go/pkg/mod/x\n", "./..."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := strings.Join(GoMutationScope(c.dir, c.misses, c.listed, "/src"), " "); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

// The template is executed by go list, so it is pinned by executing it: a
// package with source is named, a test-only one is not.
func TestGoSourcePackagesFormat(t *testing.T) {
	tmpl := template.Must(template.New("golist").Parse(GoSourcePackagesFormat))
	type pkg struct {
		Dir               string
		GoFiles, CgoFiles []string
	}
	var b strings.Builder
	for _, p := range []pkg{{Dir: "/src/a", GoFiles: []string{"a.go"}}, {Dir: "/src/tests"}, {Dir: "/src/c", CgoFiles: []string{"c.go"}}} {
		if err := tmpl.Execute(&b, p); err != nil {
			t.Fatal(err)
		}
	}
	if b.String() != "/src/a\n/src/c\n" {
		t.Fatalf("got %q", b.String())
	}
}

func TestGoCachedProfile(t *testing.T) {
	for name, c := range map[string]struct{ raw, want string }{
		"a profile":      {`{"schema_version":7,"coverage_key":"k","coverage_profile":"mode: set\nm/a.go:1.1,2.2 1 1\n"}`, "mode: set\nm/a.go:1.1,2.2 1 1\n"},
		"no profile":     {`{"schema_version":7}`, ""},
		"not JSON":       {`{`, ""},
		"an absent file": {"", ""},
	} {
		if got := GoCachedProfile(c.raw); got != c.want {
			t.Errorf("%s: got %q", name, got)
		}
	}
}

func TestGoCachedProfileOfTheWrongType(t *testing.T) {
	if got := GoCachedProfile(`{"coverage_profile":7,"x":`); got != "" {
		t.Fatalf("got %q", got)
	}
}
