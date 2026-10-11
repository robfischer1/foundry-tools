package castcache

import "testing"

func TestKeyIsRepoToolchainAndTriple(t *testing.T) {
	const image = "docker.io/library/rust:1.97.0-bookworm@sha256:8fa55b2f3ddf97471ab6a767bfa3f37e6bad0986ba823e75fea57e2a2a5c3073"
	tc := ToolchainKey(image)
	if tc != "rust-1.97.0-bookworm-8fa55b2f3ddf" {
		t.Errorf("toolchain key %q", tc)
	}
	cases := map[string]struct {
		repo, toolchain, triple string
		want                    string
		err                     bool
	}{
		"a door URL":         {"http://ourea:8215/rob/cerberus.git", tc, "x86_64-unknown-linux-gnu", "cerberus/rust-1.97.0-bookworm-8fa55b2f3ddf/x86_64-unknown-linux-gnu.tar.gz", false},
		"no .git suffix":     {"http://ourea:8215/rob/bellows", tc, "aarch64-unknown-linux-gnu", "bellows/rust-1.97.0-bookworm-8fa55b2f3ddf/aarch64-unknown-linux-gnu.tar.gz", false},
		"a trailing slash":   {"http://ourea:8215/rob/bellows.git/", tc, "t", "bellows/" + tc + "/t.tar.gz", false},
		"odd characters":     {"http://x/a b?c.git", "tc/../x", "t", "a_b_c/tc_.._x/t.tar.gz", false},
		"no repo":            {"", tc, "t", "", true},
		"a repo that is dot": {"..", tc, "t", "", true},
		"no toolchain":       {"http://x/a.git", "", "t", "", true},
		"no triple":          {"http://x/a.git", tc, "", "", true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Key(c.repo, c.toolchain, c.triple)
			if (err != nil) != c.err || got != c.want {
				t.Errorf("Key = %q, %v; want %q (err %v)", got, err, c.want, c.err)
			}
		})
	}
	// Two repos, two toolchains and two triples are three different objects.
	a, _ := Key("http://x/a.git", "tc1", "t1")
	for _, other := range [][3]string{{"http://x/b.git", "tc1", "t1"}, {"http://x/a.git", "tc2", "t1"}, {"http://x/a.git", "tc1", "t2"}} {
		if b, _ := Key(other[0], other[1], other[2]); b == a {
			t.Errorf("%v collides with %q", other, a)
		}
	}
}

func TestToolchainKeyWithoutADigestOrARegistry(t *testing.T) {
	for in, want := range map[string]string{
		"rust:1.97.0":                      "rust-1.97.0",
		"rust":                             "rust",
		"r.io/a/b:1@sha256:abc":            "b-1",
		"r.io/a/b:1@sha256:0123456789abcd": "b-1-0123456789ab",
	} {
		if got := ToolchainKey(in); got != want {
			t.Errorf("ToolchainKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTripleByArchitecture(t *testing.T) {
	for arch, want := range map[string]string{"amd64": "x86_64-unknown-linux-gnu", "arm64": "aarch64-unknown-linux-gnu"} {
		if got, err := Triple(arch); err != nil || got != want {
			t.Errorf("Triple(%s) = %q, %v", arch, got, err)
		}
	}
	if _, err := Triple("riscv64"); err == nil {
		t.Error("an unknown architecture got a triple")
	}
}

func TestURINamesTheBucket(t *testing.T) {
	if got := URI("a/b.tar.gz"); got != "s3://cast-cache/a/b.tar.gz" {
		t.Errorf("URI = %q", got)
	}
}
