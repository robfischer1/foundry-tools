package main

import (
	"strings"
	"testing"
)

// THE FLEET LANE'S SHARED TEST HELPERS. The fleet atoms' own tests sit beside
// their files (atoms/fleet/<name>/<name>_test.go); what other lanes' chain tests
// read of the paper engine stays here.

// fleetTree copies everyLaneTree, deletes the paths named, and overlays extra
// — so a test declares only the difference its branch needs.
func fleetTree(extra map[string]string, drop ...string) map[string]string {
	out := map[string]string{}
	for k, v := range everyLaneTree {
		out[k] = v
	}
	for _, d := range drop {
		delete(out, d)
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// fleetNoContainer is rule 3's observable half: an absence decided from the
// Directory costs an entries read, never an image pull.
func fleetNoContainer(t *testing.T, why string) {
	t.Helper()
	for _, q := range engine.chains() {
		if strings.Contains(q, "container{") {
			t.Errorf("%s: a container was provisioned:\n%s", why, q)
		}
	}
}

// The needles for the two questions the origin lookup asks git.
const (
	sjLsNeedle     = `"git","ls-files"`
	sjOriginNeedle = `"git","remote","get-url","origin"`
)
