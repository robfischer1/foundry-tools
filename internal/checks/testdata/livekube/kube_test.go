package kubefixture

import "testing"

// An ordinary test, behind no tag: the lane counts the tests a module has with no
// tag compiled in, and a module whose only tests sit behind live_kube would be
// reported as having none. The live suite is in kube_live_test.go.fixture.
func TestVersionNamesTheFixture(t *testing.T) {
	if Version == "" {
		t.Fatal("no version")
	}
}
