package castcache

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

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

// The AWS documentation's "GET Object" example, byte for byte: the signature
// pins the canonical request, the scope and the key derivation.
func TestSignMatchesTheAWSDocumentationVector(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	Sign(req, Creds{AccessKeyID: "AKIAIOSFODNN7EXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"},
		emptyHash, time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization\n got %s\nwant %s", got, want)
	}
}

func TestSignCoversTheSessionTokenAndEncodesThePath(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPut, "http://seaweedfs:8333/cast-cache/a/b c+d.tar.gz", nil)
	req.Header.Set("Content-Type", "application/gzip")
	Sign(req, Creds{AccessKeyID: "K", SecretAccessKey: "S", SessionToken: "TOK"}, "abc", time.Unix(0, 0))
	if req.Header.Get("X-Amz-Security-Token") != "TOK" {
		t.Error("the session token was not sent")
	}
	auth := req.Header.Get("Authorization")
	if !strings.Contains(auth, "SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date;x-amz-security-token,") {
		t.Errorf("signed headers: %s", auth)
	}
	if got := uriEncode("/cast-cache/a/b c+d~.tar.gz"); got != "/cast-cache/a/b%20c%2Bd~.tar.gz" {
		t.Errorf("uriEncode = %q", got)
	}
}
