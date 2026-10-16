package checks

import (
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE LANE-LEVEL PROOF'S FIXTURE IS A REAL GO MODULE, even though go cannot
// compile it in place: it ships as kube_live_test.go.fixture so foundry-tools' own
// lanes (which read build tags recursively) do not bind an API server for this
// tree, and `just live-kube-proof` copies it into place. A fixture nothing
// compiles can rot silently, so this parses it, holds it gofmt-clean, and pins
// the properties the proof stands on.
func TestLiveKubeFixtureIsAWellFormedModule(t *testing.T) {
	dir := filepath.Join("testdata", "livekube")
	raw, err := os.ReadFile(filepath.Join(dir, "kube_live_test.go.fixture"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if !strings.HasPrefix(src, "//go:build live_kube\n") {
		t.Error("the fixture's test is not behind live_kube")
	}
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "kube_live_test.go", raw, parser.AllErrors); err != nil {
		t.Fatalf("the fixture does not parse: %v", err)
	}
	formatted, err := format.Source(raw)
	if err != nil || string(formatted) != src {
		t.Errorf("the fixture is not gofmt-clean (err %v)", err)
	}
	// the three variables, a ConfigMap, a ServiceAccount token and a TokenReview:
	// what the substrate has to be able to do
	for _, need := range []string{
		"TEST_KUBE_URL", "TEST_KUBE_TOKEN", "TEST_KUBE_CA_FILE",
		"/configmaps", "/serviceaccounts", "/token", "/tokenreviews",
		"system:serviceaccount:default:", "authenticated",
	} {
		if !strings.Contains(src, need) {
			t.Errorf("the fixture never mentions %q", need)
		}
	}
	// no test double: the fixture speaks HTTPS to the URL it is handed
	for _, banned := range []string{"httptest", "fake.NewSimpleClientset", "envtest"} {
		if strings.Contains(src, banned) {
			t.Errorf("the fixture uses %q, a stand-in for the API server", banned)
		}
	}
	// the module around it: a go.mod, a non-test file, and an ordinary test (the
	// lane reports a module with no untagged test as having none). No file Go would
	// read carries a build tag — which is what keeps this tree's own lanes from
	// seeing live_kube and binding an API server for it.
	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil || !strings.HasPrefix(string(mod), "module example.com/kubefixture\n") {
		t.Errorf("go.mod: %q (%v)", mod, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "//go:build") {
			t.Errorf("%s carries a build constraint this tree's own lanes would read", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "kube_test.go")); err != nil {
		t.Errorf("the fixture module has no untagged test: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "kube.go")); err != nil {
		t.Errorf("the fixture module has no non-test file: %v", err)
	}
}
