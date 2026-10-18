package main

import (
	"strings"
	"testing"
)

// missingBase itself: the door's tree is loud and names the base; a local
// snapshot gets the atom's own stand-down words back, untouched.
func TestMissingBaseIsLoudOnlyOnTheDoorsTree(t *testing.T) {
	state, why := newRun(dag.Directory(), "http://door:8215/star.git", "b4se").missingBase("stand down")
	if state != 2 || !strings.HasPrefix(why, "CANNOT RUN - the base b4se is not in this history") {
		t.Errorf("fetched tree: got %d %q", state, why)
	}
	state, why = newRun(dag.Directory(), "", "b4se").missingBase("stand down")
	if state != 0 || why != "stand down" {
		t.Errorf("local snapshot: got %d %q", state, why)
	}
}
