package checks

import "testing"

// NOTHING CHANGES NOW: each default answers the literal it replaced.
func TestKitSourcesDefaultToTodaysRepositories(t *testing.T) {
	if got := RustKit.GitURL(); got != "https://git.notusmi.com/stellar-core-rust.git" {
		t.Errorf("the rust kit's URL is %q", got)
	}
	if RustKit.Repo != "rob/stellar-core-rust" || RustKit.Subdir != "" {
		t.Errorf("%+v", RustKit)
	}
	if GoKit.Repo != "rob/stellar-core-go" || GoKit.Subdir != "" {
		t.Errorf("%+v", GoKit)
	}
	for _, c := range []struct{ name, got, want string }{
		{"path", RustKit.Path("Cargo.toml"), "Cargo.toml"},
		{"gopath", GoKit.Path("kafkatopics/topics.go"), "kafkatopics/topics.go"},
		{"tag", RustKit.TagRef("v0.89.0"), "refs/tags/v0.89.0"},
		{"tree", RustKit.Tree("abc"), "abc"},
	} {
		if c.got != c.want {
			t.Errorf("%s: %q, want %q", c.name, c.got, c.want)
		}
	}
}

// Pointing the one setting at the monorepo moves every reader with it.
func TestAKitInASubdirectoryMovesEveryDerivedForm(t *testing.T) {
	k := KitSource{Repo: "rob/stellar-core", Subdir: "rust"}
	for _, c := range []struct{ name, got, want string }{
		{"url", k.GitURL(), "https://git.notusmi.com/stellar-core.git"},
		{"name", k.Name(), "stellar-core"},
		{"path", k.Path("tools/compose/compose.sh"), "rust/tools/compose/compose.sh"},
		{"tag", k.TagRef("v0.89.0"), "refs/tags/rust/v0.89.0"},
		{"tree", k.Tree("abc"), "abc:rust"},
	} {
		if c.got != c.want {
			t.Errorf("%s: %q, want %q", c.name, c.got, c.want)
		}
	}
}
