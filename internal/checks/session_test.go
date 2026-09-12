package checks

import (
	"os"
	"testing"
)

// The defaulting init leaves a real session alone and supplies one where
// there is none. This binary never had one, so both read the defaults.
func TestASessionIsDefaultedForTheTests(t *testing.T) {
	if os.Getenv("DAGGER_SESSION_PORT") != TestSessionPort || os.Getenv("DAGGER_SESSION_TOKEN") != TestSessionToken {
		t.Errorf("session not defaulted: %q %q", os.Getenv("DAGGER_SESSION_PORT"), os.Getenv("DAGGER_SESSION_TOKEN"))
	}
}
