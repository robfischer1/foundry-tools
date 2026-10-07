package checks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// The repositories a TypeScript core's regeneration reads: the WIT and the tape
// come from stellar-core at the commits provenance.json records, the guest's
// source from stellar-core-rust at its recorded commit. Both are read without
// credentials, as scripts/regen-check.sh's own `git clone` of them is.
const (
	TSRegenWitGit  = "https://git.notusmi.com/stellar-core.git"
	TSRegenCoreGit = "https://git.notusmi.com/stellar-core-rust.git"
)

var (
	regenCallRe  = regexp.MustCompile(`(?m)^[ \t]+scripts/regen-check\.sh[ \t]+([a-z0-9]+)[ \t]*$`)
	regenCommitR = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// RegenWorlds reads which worlds a TypeScript star regenerates from its own
// justfile: the `scripts/regen-check.sh <world>` lines of its recipes, in order,
// once each. THE REPO'S OWN `just regen-check` IS THE DECLARATION. The lane runs
// what a person would run, no more: stellar-core-ts has fifteen *core-gen
// directories and its recipe names four, so the lane holds four, and a world
// joins the lane by joining the recipe.
func RegenWorlds(justfile string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range regenCallRe.FindAllStringSubmatch(justfile, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// RegenPin is one file pinned at a commit of stellar-core.
type RegenPin struct {
	Commit string `json:"commit"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// RegenProv is what a world's provenance.json pins, read the way regen-check.sh
// reads it.
type RegenProv struct {
	WIT       RegenPin  `json:"wit"`
	WITResult *RegenPin `json:"wit_result"`
	Tape      RegenPin  `json:"tape"`
	Core      struct {
		Commit    string `json:"commit"`
		Toolchain string `json:"toolchain"`
	} `json:"core"`
	Tools struct {
		WasmTools string `json:"wasm-tools"`
		JCO       string `json:"jco"`
	} `json:"tools"`
	GuestWasm     struct{ SHA256 string } `json:"guest_wasm"`
	ComponentWasm struct{ SHA256 string } `json:"component_wasm"`
}

// ParseRegenProv reads a provenance.json and refuses one that cannot be
// regenerated from: every pin the script reads has to be there, and a commit has
// to be 40 hex, because a missing pin read as "" would compare equal to a
// missing measurement and pass.
func ParseRegenProv(body string) (RegenProv, error) {
	var p RegenProv
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		return p, fmt.Errorf("provenance.json is not JSON: %v", err)
	}
	for _, f := range []struct{ name, v string }{
		{"wit.path", p.WIT.Path}, {"wit.sha256", p.WIT.SHA256},
		{"tape.path", p.Tape.Path}, {"tape.sha256", p.Tape.SHA256},
		{"core.toolchain", p.Core.Toolchain},
		{"tools.wasm-tools", p.Tools.WasmTools}, {"tools.jco", p.Tools.JCO},
		{"guest_wasm.sha256", p.GuestWasm.SHA256}, {"component_wasm.sha256", p.ComponentWasm.SHA256},
	} {
		if f.v == "" {
			return p, fmt.Errorf("provenance.json pins no %s", f.name)
		}
	}
	commits := []struct{ name, v string }{
		{"wit.commit", p.WIT.Commit}, {"tape.commit", p.Tape.Commit}, {"core.commit", p.Core.Commit},
	}
	if p.WITResult != nil {
		commits = append(commits, struct{ name, v string }{"wit_result.commit", p.WITResult.Commit})
		if p.WITResult.Path == "" || p.WITResult.SHA256 == "" {
			return p, fmt.Errorf("provenance.json's wit_result pins no path or sha256")
		}
	}
	for _, c := range commits {
		if !regenCommitR.MatchString(c.v) {
			return p, fmt.Errorf("provenance.json's %s is %q, not a 40-hex commit", c.name, c.v)
		}
	}
	return p, nil
}

// SHA256Hex is the digest of a string's bytes, as regen-check.sh's `sha256sum`.
// FOR TEXT ONLY: a file read through the SDK as a string comes back as UTF-8, so
// a binary (guest.wasm) hashed here reads as a different file. Binaries are
// hashed by sha256sum in the engine (ParseSha256sum).
func SHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ParseSha256sum reads `sha256sum` output (`<64 hex>  <path>` per line) into
// path -> digest. A line that is not that shape is an error rather than a
// skipped file: a digest that was not read is not a match.
func ParseSha256sum(out string) (map[string]string, error) {
	sums := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		sum, path, ok := strings.Cut(line, "  ")
		if !ok || len(sum) != 64 || path == "" {
			return nil, fmt.Errorf("sha256sum printed %q, not `<digest>  <path>`", line)
		}
		sums[path] = sum
	}
	return sums, nil
}

// RegenExpect compares a measurement with its pin the way the script's
// `expect` does: "" and an "ok" line when they agree, a problem line when not.
func RegenExpect(label, got, pinned string) (ok, problem string) {
	if got != pinned {
		return "", fmt.Sprintf("%s: got %s, pinned %s", label, got, pinned)
	}
	return fmt.Sprintf("ok  %s %s", label, got), ""
}

// RegenToolVersion reads `wasm-tools --version` as the script does: the name
// dropped, the rest as the version the pin is compared with.
func RegenToolVersion(out string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "wasm-tools "))
}

// RegenWorldEnvelope is the one-line summary a world prints when it holds.
func RegenWorldEnvelope(world string, checked int) string {
	return fmt.Sprintf("regen-check(%s): PASS - guest, component, tool versions and generated files byte-identical (%d pins checked)", world, checked)
}
