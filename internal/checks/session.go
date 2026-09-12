package checks

import (
	"net"
	"os"
	"strconv"
)

// THE MODULE'S OWN TESTS NEED A DAGGER SESSION TO EXIST, and this is where it
// comes from.
//
// internal/dagger's generated init() reads DAGGER_SESSION_PORT and
// DAGGER_SESSION_TOKEN and panics without them, so `go test` on the module's
// package main could not start at all — the mutation lane then read every
// line in atoms_*.go as NOT COVERED (measured 2026-09-12, PR #31: 370
// mutants). The tests answer on the port with a paper engine
// (engine_fake_test.go); what they need is for the variables to be set before
// internal/dagger initialises.
//
// THEY ARE, BECAUSE OF THE IMPORT PATH. Go ≥ 1.21 initialises packages with
// no dependency between them in import-path order, and
// dagger/foundry-tools/internal/checks sorts before
// dagger/foundry-tools/internal/dagger. Package main imports both, so this
// init runs first — under `go test`, under gremlins, under the door's old
// and new mutation scripts alike, with no environment anyone has to remember
// to export.
//
// INERT IN A REAL ENGINE: the module runtime sets both variables before the
// binary starts, and this touches neither when they are present.
//
// THE PORT IS WHATEVER IS FREE, NOT A CONSTANT. The first cut pinned 41421
// and two checkouts testing at once on one host raced for it — measured
// 2026-09-12, 4 of 12 full runs on rob02 dying with "bind: address already
// in use" while a sibling worktree's test binary held the port. A listener
// on :0 hands back a port the kernel knows is free; it is closed at once and
// the test binary's TestMain re-opens it. The gap between the two is
// microseconds on a loopback nobody else is racing for.
const TestSessionToken = "paper-engine"

func init() {
	if os.Getenv("DAGGER_SESSION_PORT") == "" {
		_ = os.Setenv("DAGGER_SESSION_PORT", freeLoopbackPort())
	}
	if os.Getenv("DAGGER_SESSION_TOKEN") == "" {
		_ = os.Setenv("DAGGER_SESSION_TOKEN", TestSessionToken)
	}
}

// freeLoopbackPort asks the kernel for an unused loopback port. On the one
// host with no loopback at all it answers "1", which the paper engine will
// refuse to listen on — loudly, in TestMain, rather than here at init.
func freeLoopbackPort() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "1"
	}
	defer ln.Close()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}
