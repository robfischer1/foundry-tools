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

// RegenLabeledPin is a stellar-core file pin with the name its line carries.
type RegenLabeledPin struct {
	Label string
	Pin   RegenPin
}

// Pins are the files read at their commits of stellar-core, in the script's
// order: the WIT, the result WIT where there is one, then the tape.
func (p RegenProv) Pins() []RegenLabeledPin {
	out := []RegenLabeledPin{{"wit", p.WIT}}
	if p.WITResult != nil {
		out = append(out, RegenLabeledPin{"wit", *p.WITResult})
	}
	return append(out, RegenLabeledPin{"tape", p.Tape})
}

// CoreCopy is a file the guest's own checkout must carry byte for byte.
type CoreCopy struct{ Path, SHA256 string }

// CoreCopies are the WIT files the core at its recorded commit carries for the
// guest to be built against: the guest is built against its own copy, so the
// copy has to be the pinned file.
func (p RegenProv) CoreCopies(world string) []CoreCopy {
	out := []CoreCopy{{"wit/aiws-" + world + ".wit", p.WIT.SHA256}}
	if p.WITResult != nil {
		out = append(out, CoreCopy{"wit/aiws-result.wit", p.WITResult.SHA256})
	}
	return out
}

// RegenProbe is one measurement and the pin it is held to.
type RegenProbe struct{ Label, Got, Pinned string }

// RegenCompare holds every probe to its pin: an "ok" line for each that agrees,
// a problem line for each that does not. Nothing stops at the first, so one run
// names every pin that moved.
func RegenCompare(probes []RegenProbe) (lines, problems []string) {
	for _, p := range probes {
		ok, problem := RegenExpect(p.Label, p.Got, p.Pinned)
		if problem != "" {
			problems = append(problems, problem)
			continue
		}
		lines = append(lines, ok)
	}
	return lines, problems
}

// RegenWorldResult is what one world's regeneration answered: lines it
// verified, pins and files that disagree, or an error that kept it from
// looking at all.
type RegenWorldResult struct {
	World    string
	Lines    []string
	Problems []string
	Err      string
}

// RegenFold settles the worlds into one exit and one output: 2 (could not run)
// if any world could not look, else 1 (findings) if any pin or file disagrees,
// else 0. A world that could not run is not a world that passed, and it is not
// a finding either, so it is the louder code. The lines are the script's own.
func RegenFold(results []RegenWorldResult) (int, string) {
	code := 0
	var out []string
	for _, r := range results {
		for _, l := range r.Lines {
			out = append(out, "regen-check("+r.World+"): "+l)
		}
		for _, p := range r.Problems {
			out = append(out, "regen-check("+r.World+"): FAIL: "+p)
		}
		if r.Err != "" {
			out = append(out, "regen-check("+r.World+"): CANNOT RUN: "+r.Err)
			code = 2
		} else if len(r.Problems) > 0 && code == 0 {
			code = 1
		}
	}
	if code == 1 {
		out = append([]string{"ts:regen: FINDINGS - a core does not regenerate byte-identical from its pins"}, out...)
	}
	return code, strings.Join(out, "\n")
}
