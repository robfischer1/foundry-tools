package main

import (
	"testing"
	"time"
)

// The attestation's delivery policy is attest.py's: six attempts, backing off
// from four seconds, inside three minutes.
func TestTheAttestationPolicyIsAttestPys(t *testing.T) {
	if attestAttempts != 6 || attestBackoff != 4*time.Second || attestBudget != 3*time.Minute {
		t.Fatalf("attempts %d, backoff %s, budget %s", attestAttempts, attestBackoff, attestBudget)
	}
	if duration("not a duration") != 0 {
		t.Fatal("an unreadable duration is not zero")
	}
}
