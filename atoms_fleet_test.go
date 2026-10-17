package main

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

// The needles for the two questions the origin lookup asks git.
const (
	sjLsNeedle     = `"git","ls-files"`
	sjOriginNeedle = `"git","remote","get-url","origin"`
)
