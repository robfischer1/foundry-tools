package livekube

import "testing"

// An ordinary test, behind no tag: the lane counts a package whose only tests sit
// behind a tag as having none. The live suite is in live_test.go.
func TestSubstrateNamesTheTag(t *testing.T) {
	if Substrate != "live_kube" {
		t.Fatalf("Substrate = %q", Substrate)
	}
}
