package catalogue

import (
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// daedalus#22977: lock-apt re-locks a star's apt packages.lock on the cluster.
// The verb reaches dev:<lang>-lock-apt for the star's own language, so each of
// the four languages carries the same atom.

var aptLockHead = []string{"deblock", "-relock", "-dockerfile", "Dockerfile", "-out", "packages.lock"}

func TestDEF_22977_EveryLanguageHasTheAptLockAtom(t *testing.T) {
	for _, lang := range []string{"go", "rust", "python", "ts"} {
		id := "dev:" + lang + "-lock-apt"
		m := manifestOf(t, id)
		if got := m.Compose(nil); !slices.Equal(got, aptLockHead) {
			t.Errorf("%s: argv %v, want %v", id, got, aptLockHead)
		}
		if m.Surface != "any:**/packages.lock" || m.Toolchain != "go" || !m.WritesTree || m.PerModule {
			t.Errorf("%s: %+v", id, m)
		}
		// the verb mends a lock the star's own preflight knows nothing of, and
		// the star's manifest need not be a Go module
		if m.RunsPreflight() {
			t.Errorf("%s runs the go preflight over a tree that need not be a Go module", id)
		}
	}
}

func TestDEF_22977_TheAptLockAtomTakesDeblocksOwnArguments(t *testing.T) {
	m := manifestOf(t, "dev:ts-lock-apt")
	for name, c := range map[string]struct{ args, want []string }{
		"upgrade the base's packages": {[]string{"-upgrade"}, append(slices.Clone(aptLockHead), "-upgrade")},
		"another package":             {[]string{"libfoo1"}, append(slices.Clone(aptLockHead), "libfoo1")},
		"another Dockerfile":          {[]string{"-dockerfile", "build/Dockerfile"}, append(slices.Clone(aptLockHead), "-dockerfile", "build/Dockerfile")},
	} {
		if got := m.Compose(c.args); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

// LOCKAPT2 (FLIP10, run forge/dev-theia-1af0ca0-kpztz): deblock pulls the
// Dockerfile's base from the registry under the pod's JWT-SVID, and the executor
// mounts the SPIRE socket (and sets SPIFFE_ENDPOINT_SOCKET) only into a container
// whose atoms need svid. Without the need deblock dialled a socket the go
// container did not have.
func TestLOCKAPT2_TheAptLockAtomNeedsTheSVID(t *testing.T) {
	for _, lang := range []string{"go", "rust", "python", "ts"} {
		id := "dev:" + lang + "-lock-apt"
		if m := manifestOf(t, id); !slices.Contains(m.Needs, "svid") || !slices.Contains(m.Needs, "tool:deblock") {
			t.Errorf("%s needs %v, want svid and tool:deblock", id, m.Needs)
		}
	}
}

// The tool is a go install of the daedalus command at the version the Go
// constant pins, and tools.toml agrees (TestToolsTomlMatchesGo holds the rest).
func TestDEF_22977_DeblockIsAPinnedGoTool(t *testing.T) {
	const module = "git.notusmi.com/rob/daedalus/cmd/deblock@"
	if !strings.HasPrefix(checks.DeblockModule, module) || len(checks.DeblockModule) <= len(module) {
		t.Errorf("DeblockModule = %q", checks.DeblockModule)
	}
}
