package checks

import (
	"strings"
	"testing"
)

// THE RUST KIT IS THE MONOREPO'S rust/ (D-B5); the Go kit has not moved.
func TestTheRustKitIsTheMonoreposRustDirectory(t *testing.T) {
	if RustKit.Repo != "rob/stellar-core" || RustKit.Subdir != "rust" {
		t.Errorf("%s %s", RustKit.Repo, RustKit.Subdir)
	}
	if GoKit.Repo != "rob/stellar-core" || GoKit.Subdir != "go" {
		t.Errorf("%+v", GoKit)
	}
	for _, c := range []struct{ name, got, want string }{
		{"url", RustKit.GitURL(), "https://git.notusmi.com/stellar-core.git"},
		{"name", RustKit.Name(), "stellar-core"},
		{"path", RustKit.Path("tools/compose/compose.sh"), "rust/tools/compose/compose.sh"},
		{"tag", RustKit.TagRef("v0.89.0"), "refs/tags/rust/v0.89.0"},
		{"gopath", GoKit.Path("kafkatopics/topics.go"), "go/kafkatopics/topics.go"},
		{"gotag", GoKit.TagRef("v0.1.0"), "refs/tags/go/v0.1.0"},
	} {
		if c.got != c.want {
			t.Errorf("%s: %q, want %q", c.name, c.got, c.want)
		}
	}
}

// A PIN NAMING stellar-core-rust RESOLVES: the import tip every live
// provenance.json pins (01337847, v0.89.0) is stellar-core's c735c1f (rust/v0.89.0).
func TestAStellarCoreRustPinTranslatesThroughTheCommitMap(t *testing.T) {
	if got := RustKit.Commit("01337847eb0e8e3ffd1caac7764d69f097e5983d"); got != "c735c1f792013022e651986ca34e7ab8af0ef7f3" {
		t.Errorf("the import tip answered %q", got)
	}
	if got := RustKit.Commit("00143d349fe145cb676db9408e0cc185ee7ef42c"); got != "3f6638b7949071eda359d43fa30b59d48d0ab7b4" {
		t.Errorf("the map's first line answered %q", got)
	}
	// a commit of the monorepo itself, and anything else, answers itself
	for _, c := range []string{"f5d570e3a1b2c3d4e5f60718293a4b5c6d7e8f90", "c0ffee", ""} {
		if got := RustKit.Commit(c); got != c {
			t.Errorf("%q answered %q", c, got)
		}
	}
	if got := GoKit.Commit("01337847eb0e8e3ffd1caac7764d69f097e5983d"); got != "01337847eb0e8e3ffd1caac7764d69f097e5983d" {
		t.Errorf("a kit with no map translated: %q", got)
	}
}

// THE EMBEDDED MAP IS THE WHOLE MAP: every line of stellar-core-rust's main at
// the import (688), one-to-one, and nothing but 40-hex commits.
func TestTheEmbeddedCommitMapIsWholeAndOneToOne(t *testing.T) {
	if len(rustCommitMap) != 688 {
		t.Errorf("%d entries, want 688", len(rustCommitMap))
	}
	seen := map[string]string{}
	for old, nw := range rustCommitMap {
		if prev, dup := seen[nw]; dup {
			t.Errorf("%s and %s both map to %s", prev, old, nw)
		}
		seen[nw] = old
	}
	var data int
	for _, l := range strings.Split(rustCommitMapText, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "old ") {
			continue
		}
		data++
		if !commitMapLine.MatchString(l) {
			t.Errorf("a line the parser skips: %q", l)
		}
	}
	if data != len(rustCommitMap) {
		t.Errorf("%d data lines, %d entries", data, len(rustCommitMap))
	}
}

func TestParseCommitMapSkipsCommentsAndTheHeader(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	m := parseCommitMap("# a comment " + a + " " + b + "\nold   new\n" + a + " " + b + "\n\n" + "short " + b + "\n")
	if len(m) != 1 || m[a] != b {
		t.Errorf("%v", m)
	}
}
