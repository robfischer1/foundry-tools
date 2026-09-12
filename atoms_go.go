package main

import (
	"context"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE REFERENCE ATOMS. go:vet and go:test-race are the exemplar every other
// lane's port follows; read runtime.go's rules first, then these.

func init() {
	register("go:vet", goVet)
	register("go:test-race", goTestRace)
}

// goModules is the go lane's provisioned base: the lane container with the
// module graph downloaded. IT IS ONE EXEC, ON ITS OWN, so the engine caches it
// as a layer keyed on the tree — and with the module cache a volume, a tree
// whose go.sum did not move downloads nothing.
//
// The download is its own step under the default Expect for the reason the
// old script guarded it: `go vet` and `go build` exit 1 for a module the
// proxy would not serve exactly as they exit 1 for a finding, and an atom
// that reads exit 1 as FINDINGS turns a proxy's bad hour into a terminal red
// the sweep will not re-ask (measured 2026-09-11 17:26Z, foundry-tools#29:
// `proxy.golang.org …: 502 Bad Gateway` filed as a finding). Here a failed
// download is a failed exec — state 2, could not run, re-asked.
func (r *run) goModules() *dagger.Container {
	return r.lane(checks.ImageGo).
		WithExec([]string{"go", "mod", "download"})
}

// go vet ./... reports nothing.
func goVet(ctx context.Context, r *run) checks.Verdict {
	return verdict(ctx, checks.AtomByID("go:vet"),
		r.goModules().WithExec([]string{"go", "vet", "./..."}, anyExit))
}

// go test -race ./... passes, and there is something for it to pass.
//
// NO TESTS IS A FINDING. `go test ./...` prints "[no test files]" per package
// and exits 0, so a module with no test anywhere read green. Rob, 2026-09-11:
// nothing is built without tests. The count is go's own (TestGoFiles +
// XTestGoFiles per package), read in Go rather than in shell; a module where
// every package answers 0 is red before the suite runs.
//
// THE FLEET RECORD TREE RIDES ALONG. hephaestus's internal/slag goldens grade
// every record committed in foundry-dies; a lane checks out one repo, so
// without the mount they resolved nothing and SKIPPED, which `go test` prints
// as ok (#2453, #8118). A repo with no such test reads FOUNDRY_DIES and does
// nothing with it.
func goTestRace(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("go:test-race")
	mods := r.withDies(r.goModules())

	counts, code, err := output(ctx, mods.WithExec([]string{
		"go", "list", "-f", "{{len .TestGoFiles}}{{len .XTestGoFiles}}", "./...",
	}, anyExit))
	if err != nil {
		return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	if code != 0 {
		// A module that will not even list is a red about the module, and
		// the old atom filed it as FINDINGS on purpose: the tests cannot be
		// counted, so they cannot be shown to exist.
		return checks.VerdictOf(a, 1, "go:test-race: FINDINGS - go list ./... failed, so the tests cannot be counted: "+lastLine(counts))
	}
	if !checks.GoHasTestFiles(counts) {
		return checks.VerdictOf(a, 1, "go:test-race: FINDINGS - no test file in any package; nothing is built without tests")
	}
	return verdict(ctx, a, mods.WithExec([]string{"go", "test", "-race", "./..."}, anyExit))
}

// lastLine is the tail of a tool's output — the line a human reads first.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
