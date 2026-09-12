package checks

import (
	"os"
	"strconv"
	"testing"
)

// The defaulting init leaves a real session alone and supplies one where
// there is none. This binary never had one, so both read the defaults.
func TestASessionIsDefaultedForTheTests(t *testing.T) {
	port, err := strconv.Atoi(os.Getenv("DAGGER_SESSION_PORT"))
	if err != nil || port <= 0 || port > 65535 {
		t.Errorf("session port not defaulted to a port: %q", os.Getenv("DAGGER_SESSION_PORT"))
	}
	if os.Getenv("DAGGER_SESSION_TOKEN") != TestSessionToken {
		t.Errorf("session token not defaulted: %q", os.Getenv("DAGGER_SESSION_TOKEN"))
	}
	if p := freeLoopbackPort(); p == "1" {
		t.Errorf("no free loopback port: %s", p)
	}
}
