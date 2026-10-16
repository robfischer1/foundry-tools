package pinsync

import (
	"strings"
	"testing"
)

func TestFollowMovesTheOneVersionToken(t *testing.T) {
	for _, tc := range []struct {
		name, literal, version, want string
		moved                        bool
	}{
		{"in step", "https://x/v1.261.0/wasm-tools-1.261.0-x86_64-linux.tar.gz", "1.261.0", "https://x/v1.261.0/wasm-tools-1.261.0-x86_64-linux.tar.gz", false},
		{"tag and asset both move", "https://x/v1.261.0/wasm-tools-1.261.0-x86_64-linux.tar.gz", "1.262.0", "https://x/v1.262.0/wasm-tools-1.262.0-x86_64-linux.tar.gz", true},
		{"a v-prefixed version moves the bare token", "https://x/v0.11.0/s-v0.11.0.tar.xz", "v0.12.0", "https://x/v0.12.0/s-v0.12.0.tar.xz", true},
		{"a member", "wasm-tools-1.261.0-x86_64-linux/wasm-tools", "1.262.0", "wasm-tools-1.262.0-x86_64-linux/wasm-tools", true},
		{"an escaped plus is not a token boundary problem", "https://x/20260623/cpython-3.14.6%2B20260623-x86_64.tar.gz", "3.14.7", "https://x/20260623/cpython-3.14.7%2B20260623-x86_64.tar.gz", true},
		{"two-part versions", "https://x/1.4/bun-1.4.tgz", "1.5", "https://x/1.5/bun-1.5.tgz", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, moved, err := follow(tc.literal, tc.version)
			if err != nil || got != tc.want || moved != tc.moved {
				t.Errorf("follow(%q, %q) = %q, %v, %v; want %q, %v", tc.literal, tc.version, got, moved, err, tc.want, tc.moved)
			}
		})
	}
}

func TestFollowRefusesAShapeItWasNotWrittenFor(t *testing.T) {
	for _, tc := range []struct{ name, literal, version, says string }{
		{"no version in the literal", "just", "1.59.0", "0 distinct"},
		{"two versions in the literal", "https://x/1.2.3/y-4.5.6", "1.2.4", "2 distinct"},
		{"a version that is not one", "https://x/1.2.3/y", "latest", "not dotted digits"},
		{"a version with a suffix", "https://x/1.2.3/y", "1.2.4-rc1", "not dotted digits"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := follow(tc.literal, tc.version)
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("follow(%q, %q) error %v, want one that says %q", tc.literal, tc.version, err, tc.says)
			}
		})
	}
}

func TestReplaceTokenTouchesOnlyTheWholeToken(t *testing.T) {
	if got := replaceToken("1.6.0 11.6.0 1.6.01 1.6.0", "1.6.0", "1.7.0"); got != "1.7.0 11.6.0 1.6.01 1.7.0" {
		t.Errorf("got %q", got)
	}
}

func TestParseReleaseSplitsATagWithASlash(t *testing.T) {
	u := "https://github.com/rustsec/rustsec/releases/download/cargo-audit/v0.22.2/cargo-audit-x86_64-unknown-linux-musl-v0.22.2.tgz"
	r, ok := parseRelease(u)
	if !ok || r.owner != "rustsec" || r.repo != "rustsec" || r.tag != "cargo-audit/v0.22.2" || r.asset != "cargo-audit-x86_64-unknown-linux-musl-v0.22.2.tgz" {
		t.Fatalf("parseRelease = %+v, %v", r, ok)
	}
	if r.String() != u {
		t.Errorf("String() = %q", r.String())
	}
	if _, ok := parseRelease("https://dl.k8s.io/release/v1.37.1/bin/linux/amd64/kubectl"); ok {
		t.Error("a non-GitHub URL parsed as a release")
	}
}

const sampleTOML = `# a comment that must survive
schema = "tools@1"

[tool.opa]
kind = "url"
spec = "https://a/1.0.0/opa"   # trailing comment
version = "1.0.0"

[tool.ruff]
kind = "uv"
version = "0.16.3"
`

func TestTOMLSetReplacesOnlyTheValue(t *testing.T) {
	d := parseTOML([]byte(sampleTOML))
	if err := d.set("tool.opa", "spec", "https://a/1.1.0/opa"); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(sampleTOML, `"https://a/1.0.0/opa"   # trailing`, `"https://a/1.1.0/opa"   # trailing`, 1)
	if got := string(d.bytes()); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if v, ok := d.get("tool.ruff", "version"); !ok || v != "0.16.3" {
		t.Errorf("get = %q, %v", v, ok)
	}
	if _, ok := d.get("tool.ruff", "spec"); ok {
		t.Error("a key the table does not hold was found")
	}
	if v, ok := d.get("", "schema"); !ok || v != "tools@1" {
		t.Errorf("the top-level key = %q, %v", v, ok)
	}
}

func TestTOMLSetRefusesWhatItCannotWrite(t *testing.T) {
	d := parseTOML([]byte(sampleTOML))
	if err := d.set("tool.opa", "sha256", "abc"); err == nil || !strings.Contains(err.Error(), "has no [tool.opa] sha256") {
		t.Errorf("a missing key: %v", err)
	}
	if err := d.set("tool.nope", "spec", "x"); err == nil {
		t.Error("a missing table was written")
	}
	for _, bad := range []string{`a"b`, `a\b`, "a\nb"} {
		if err := d.set("tool.opa", "spec", bad); err == nil || !strings.Contains(err.Error(), "needs escaping") {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if string(d.bytes()) != sampleTOML {
		t.Error("a refused set changed the document")
	}
}

func TestNormalizeIsTheLocksSpelling(t *testing.T) {
	for in, want := range map[string]string{"copier": "copier", "Ansible_Core": "ansible-core", "ruamel.yaml": "ruamel-yaml", "a-_.b": "a-b"} {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
	lock := []byte("# header\ncopier==9.19.1 \\\n    --hash=sha256:x\n")
	if !lockHolds(lock, "copier==9.19.1") || lockHolds(lock, "copier==9.19") || lockHolds(lock, "opier==9.19.1") {
		t.Error("lockHolds is not an exact line match")
	}
	if !lockHolds([]byte("copier==1.0 \\\n"), "copier==1.0") {
		t.Error("a pin on the first line is not found")
	}
}
