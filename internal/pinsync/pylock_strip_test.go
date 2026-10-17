package pinsync

import "testing"

// A version edited in place under its predecessor's hashes must not carry them
// into the regenerated lock: the hashes go, the versions and comments stay.
func TestStripHashesKeepsVersionsAndDropsEveryHash(t *testing.T) {
	in := "# header\n" +
		"pydantic-core==2.50.1 \\\n    --hash=sha256:00963fde \\\n    --hash=sha256:01340f4f\n    # via pydantic\n" +
		"copier==9.19.1 \\\n\t--hash=sha256:abc123\n"
	want := "# header\npydantic-core==2.50.1\n    # via pydantic\ncopier==9.19.1\n"
	if got := string(stripHashes([]byte(in))); got != want {
		t.Errorf("stripHashes =\n%q\nwant\n%q", got, want)
	}
	if got := string(stripHashes([]byte(want))); got != want {
		t.Errorf("a lock with no hashes must pass through unchanged, got %q", got)
	}
}
