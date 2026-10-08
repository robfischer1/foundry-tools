package main

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// moduleImportsOf is the module's own packages a directory's non-test files
// import, as directories relative to the module root.
func moduleImportsOf(t *testing.T, dir string) []string {
	t.Helper()
	const prefix = "dagger/foundry-tools/"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if rel, ok := strings.CutPrefix(strings.Trim(imp.Path.Value, `"`), prefix); ok {
				out = append(out, rel)
			}
		}
	}
	return out
}

// importClosure is every module package reached from dir, dir excluded, sorted.
func importClosure(t *testing.T, dir string) []string {
	t.Helper()
	reached := map[string]bool{dir: true}
	queue := []string{dir}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, rel := range moduleImportsOf(t, next) {
			if !reached[rel] {
				reached[rel] = true
				queue = append(queue, rel)
			}
		}
	}
	delete(reached, dir)
	out := make([]string, 0, len(reached))
	for rel := range reached {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// THE FILTERED SOURCE IS ONLY AS GOOD AS ITS LIST, for each of the six helpers
// as for the atoms binary (TestAtomsSourceCoversImports): a package the helper
// imports that its include list omits is a `go build` that fails in the engine
// where no unit test can see it, and a package the list names that the helper
// does not import is a rebuild for an edit the helper never reads.
func TestHelperSourcesCoverTheirImports(t *testing.T) {
	if len(helperSources) != 6 {
		t.Errorf("%d helpers, want castpin, hadescall, verdict, witnesscall, pgroupps and execmem", len(helperSources))
	}
	for name, src := range helperSources {
		t.Run(name, func(t *testing.T) {
			want := importClosure(t, name)
			got := slices.Clone(src.internal)
			sort.Strings(got)
			if !slices.Equal(got, want) {
				t.Errorf("%s imports %v and its include list names %v", name, want, got)
			}
			include := helperInclude(name)
			if include[0] != "go.mod" || include[1] != "go.sum" || include[2] != name+"/**" || len(include) != 3+len(want) {
				t.Errorf("include list %v", include)
			}
			for _, rel := range want {
				if !slices.Contains(include, rel+"/**") {
					t.Errorf("%s is imported and not included: %v", rel, include)
				}
			}
		})
	}
}

// The helpers that exist are the helpers that are built: a helper directory
// nobody builds through helperBinary is a build that still mounts the whole
// module, and an entry nobody uses is a list that is never held to anything.
func TestEveryHelperIsBuiltThroughHelperBinary(t *testing.T) {
	call := regexp.MustCompile(`helperBinary\("([a-z]+)"\)`)
	used := map[string]bool{}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range call.FindAllStringSubmatch(string(body), -1) {
			used[m[1]] = true
		}
	}
	for name := range helperSources {
		if !used[name] {
			t.Errorf("helper %s is in helperSources and nothing builds it through helperBinary", name)
		}
	}
	for name := range used {
		if _, ok := helperSources[name]; !ok {
			t.Errorf("helperBinary(%q) has no entry in helperSources", name)
		}
	}
	// No build of a module program mounts the module whole any more.
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "helpers.go" {
			continue
		}
		body, _ := os.ReadFile(f)
		if strings.Contains(string(body), `"/src", dag.CurrentModule().Source())`) {
			t.Errorf("%s mounts the module's whole source", f)
		}
	}
	for dir := range helperSources {
		if _, err := os.Stat(filepath.Join(dir, "main.go")); err != nil {
			t.Errorf("helper %s has no main.go: %v", dir, err)
		}
	}
}

// Every helper builds in the Go toolchain with the cache volumes, from its
// filtered source; the three that import only the standard library also refuse
// the network and keep the image's own toolchain.
func TestHelperBinaryBuildsInTheGoToolchainFromItsFilteredSource(t *testing.T) {
	for name, src := range helperSources {
		t.Run(name, func(t *testing.T) {
			engine.reset()
			if _, err := dag.Container().From("scratch").WithFile("/b", helperBinary(name)).Stdout(context.Background()); err != nil {
				t.Fatal(err)
			}
			c := engine.chain(`"go","build","-trimpath","-o","/out/`+name+`","./`+name+`"`, `from(address:"`+checks.ImageGo+`")`)
			if c == "" {
				t.Fatalf("no build chain; the engine saw:\n%s", strings.Join(engine.chains(), "\n"))
			}
			if !strings.Contains(c, `withMountedCache`) {
				t.Errorf("the build mounts no Go cache volume:\n%s", c)
			}
			if offline := strings.Contains(c, `"GOPROXY"`) && strings.Contains(c, `value:"off"`); offline != src.offline {
				t.Errorf("offline build: %v, want %v", offline, src.offline)
			}
			f := engine.chain(`"`+name+`/**"`, `"**/*_test.go"`)
			if f == "" {
				t.Fatalf("no filter carried the include list; the engine saw:\n%s", strings.Join(engine.chains(), "\n"))
			}
			for _, inc := range helperInclude(name) {
				if !strings.Contains(f, `"`+inc+`"`) {
					t.Errorf("the filter lacks %q:\n%s", inc, f)
				}
			}
		})
	}
}
