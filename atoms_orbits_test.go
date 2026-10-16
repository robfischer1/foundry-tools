package main

import (
	"os"
	"path"
	"regexp"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE READER'S MODULE IS THE ONE THE BUILD MOUNTS: the directory named by
// orbitParseDir holds a go.mod Renovate can read, a go.sum that pins the
// version it requires, and the main over policy.ParseACL. A rename that left
// the constant behind would build an empty directory on the cluster.
func TestTheOrbitReaderModuleIsARealPinnedModule(t *testing.T) {
	mod := readRepoFile(t, path.Join(orbitParseDir, "go.mod"))
	m := regexp.MustCompile(`(?m)^require git\.notusmi\.com/rob/stellar-core-go (v\d+\.\d+\.\d+)$`).FindStringSubmatch(mod)
	if m == nil {
		t.Fatalf("the reader's go.mod requires no stellar-core-go version on a line Renovate's gomod manager reads:\n%s", mod)
	}
	sum := readRepoFile(t, path.Join(orbitParseDir, "go.sum"))
	if !strings.Contains(sum, "git.notusmi.com/rob/stellar-core-go "+m[1]+" h1:") {
		t.Fatalf("go.sum pins no hash for stellar-core-go %s — the build would refuse it", m[1])
	}
	main := readRepoFile(t, path.Join(orbitParseDir, "main.go"))
	if !strings.Contains(main, `"git.notusmi.com/rob/stellar-core-go/policy"`) || !strings.Contains(main, "policy.ParseACL") {
		t.Fatal("the reader no longer parses with the star's own policy.ParseACL")
	}
	if dirs := checks.GoModuleDirs([]string{"go.mod", path.Join(orbitParseDir, "go.mod")}); len(dirs) != 1 || dirs[0] != "." {
		t.Fatalf("the reader's module must stay out of the lanes' module enumeration, got %v", dirs)
	}
}

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
