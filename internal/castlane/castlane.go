// Package castlane is the cast lane's decisions as pure functions: what a
// record asks the lane to cast (its name), the binaries and payload the tree
// names, the staged payload's content pin, and how
// hades's answer to the layer_cast mint settles. cast.go is the chain.
//
// WHAT IT REPLACES. infra's ca-cast script, which every binary repo reached
// through a one-line ci/cast.sh (`exec ca-cast --artifact app/<name>:stable
// --binaries …`) and which pip-installed a Python caster from a hephaestus pin
// 580 commits old. The arguments moved into the record (tools.cast,
// foundry-dies#226, hephaestus#97); the caster is hephaestus's cast, served
// on the wire as layer_cast (forge_mold until D12), which consumes a STAGED
// payload by reference, re-pins what it pulls and signs.
package castlane

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/buildlane"
)

// PayloadDir is the repo's root directory that ships verbatim beside the
// binaries: payload/x lands as x, payload/d/ as d/. A repo with nothing to
// ship beside its binaries has none. There is no per-repo list anywhere.
const PayloadDir = "payload"

// Cast is what one record asks the lane to cast: the star's name. The
// binaries are the tree's (BinariesFromGoList, BinariesFromCargoMetadata) and
// the payload is the tree's payload/ directory; neither is declared.
type Cast struct {
	// Kind is the bundle kind the channel rides: app for every binary repo (the
	// zero value), runtime-gov for a governance render.
	Kind string
	Name string
}

// KindApp is the bundle kind a binary repo's channel rides.
const KindApp = "app"

// kind is the channel's kind, app unless the cast names another.
func (c Cast) kind() string {
	if c.Kind == "" {
		return KindApp
	}
	return c.Kind
}

// Artifact is the bundle channel the cast mints into. It is app/<name>:stable
// for every binary repo, which is why the record carries no artifact field.
func (c Cast) Artifact() string { return c.kind() + "/" + c.Name + ":stable" }

// Repo is the channel's repository on host, without a tag or digest.
func (c Cast) Repo(host string) string { return host + "/" + c.kind() + "/" + c.Name }

// Stage is the reference the payload is pushed to before mold consumes it:
// furnace stage's {host}/staging/{name}:{pin}, the channel's kind and name
// joined so the staging name stays one bare segment.
func (c Cast) Stage(host, pin string) string {
	return host + "/staging/" + c.kind() + "-" + c.Name + ":" + pin
}

// FromRecord reads a record's cast arguments: the star's name. Every refusal
// is about the record, so the lane settles it as a finding.
func FromRecord(slag string) (Cast, error) {
	var rec struct {
		Meta struct {
			Name string `json:"name"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(slag), &rec); err != nil {
		return Cast{}, fmt.Errorf("the record does not parse: %v", err)
	}
	if rec.Meta.Name == "" {
		return Cast{}, errors.New("the record names no meta.name")
	}
	return Cast{Name: rec.Meta.Name}, nil
}

// BinariesFromGoList reads `go list -find -f '{{.Name}} {{.ImportPath}}'
// ./cmd/...`: every main package directly under cmd/, by directory name. A
// binary repo's binaries are its product, so every main ships (an image's are
// its Dockerfile's COPY lines instead, see buildlane.ReleaseCopies). None is a
// finding, not a silent empty cast.
func BinariesFromGoList(out string) ([]string, error) {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || f[0] != "main" {
			continue
		}
		// Cut answers an empty name when the import path has no /cmd/ in it.
		_, name, _ := strings.Cut(f[1], "/cmd/")
		if name == "" || strings.Contains(name, "/") {
			continue
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, errors.New("the tree has no main package under cmd/, so a binary repo has nothing to cast")
	}
	slices.Sort(names)
	return names, nil
}

// BinariesFromCargoMetadata reads `cargo metadata --no-deps --format-version
// 1`: the bin targets of the workspace's DEFAULT members, which are exactly
// what `cargo build --release` builds. cargo defines the answer (a [[bin]]
// named apart from its package, src/bin/*.rs, a workspace of member crates),
// so nothing here parses a manifest. default-members and not members: gravity's
// members include wasm-guest examples that its default members leave out.
func BinariesFromCargoMetadata(out string) ([]string, error) {
	var meta struct {
		Packages []struct {
			ID      string `json:"id"`
			Targets []struct {
				Name string   `json:"name"`
				Kind []string `json:"kind"`
			} `json:"targets"`
		} `json:"packages"`
		Default []string `json:"workspace_default_members"`
	}
	if err := json.Unmarshal([]byte(out), &meta); err != nil {
		return nil, fmt.Errorf("cargo metadata answered something that is not JSON: %v", err)
	}
	var names []string
	for _, p := range meta.Packages {
		if !slices.Contains(meta.Default, p.ID) {
			continue
		}
		for _, t := range p.Targets {
			if slices.Contains(t.Kind, "bin") && !slices.Contains(names, t.Name) {
				names = append(names, t.Name)
			}
		}
	}
	if len(names) == 0 {
		return nil, errors.New("the workspace's default members have no bin target, so a binary repo has nothing to cast")
	}
	slices.Sort(names)
	return names, nil
}

// Claims checks what lands at the payload's root. Every binary and every
// payload entry lands under one name; two claims on a name would ship
// whichever was written last. payload holds the top-level names of the
// payload tree (payload/ or the legacy extras' basenames).
func Claims(binaries, payload []string) error {
	lands := map[string]string{}
	claim := func(name, what string) error {
		if prior, ok := lands[name]; ok {
			return fmt.Errorf("%s and %s both land at %q in the payload", prior, what, name)
		}
		lands[name] = what
		return nil
	}
	for _, b := range binaries {
		if b == "" || strings.ContainsAny(b, `/\`) || strings.HasPrefix(b, ".") || strings.HasPrefix(b, "-") {
			return fmt.Errorf("%q is not a file name out of target/release", b)
		}
		if err := claim(b, "binary "+b); err != nil {
			return err
		}
	}
	for _, p := range payload {
		if err := claim(p, PayloadDir+"/"+p); err != nil {
			return err
		}
	}
	return nil
}

// ContentPin computes the immutable g{sha12} pin of a payload tree, byte for
// byte the pin hephaestus's mold re-derives from the tree it pulls
// (internal/bundle/pin.go). A pin that differs is a mint mold refuses, so the
// golden vectors in the test are hephaestus's, which were the Python
// reference's.
//
// The formula: sha256 over, for each regular file in parts-tuple order,
// relpath-as-posix, NUL, file bytes, NUL; then "g" + hex[:12].
func ContentPin(tree string) (pin string, files []string, err error) {
	err = filepath.WalkDir(tree, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == tree {
			return nil
		}
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(tree, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", nil, fmt.Errorf("content pin over %s: %w", tree, err)
	}
	// Mapping the separator below every legal name byte makes a byte sort
	// order paths as Python's parts tuples do: "a/x" before "a-b/x".
	slices.SortFunc(files, func(a, b string) int {
		return strings.Compare(strings.ReplaceAll(a, "/", "\x00"), strings.ReplaceAll(b, "/", "\x00"))
	})
	digest := sha256.New()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(tree, filepath.FromSlash(rel)))
		if err != nil {
			return "", nil, fmt.Errorf("content pin over %s: %w", tree, err)
		}
		digest.Write([]byte(rel))
		digest.Write([]byte{0})
		digest.Write(data)
		digest.Write([]byte{0})
	}
	return "g" + hex.EncodeToString(digest.Sum(nil))[:12], files, nil
}

// RunPin is castpin's process: it prints a payload tree's pin, then its files
// one per line in pin order, which is the staging push's argument list.
func RunPin(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: castpin <dir>")
		return 2
	}
	pin, files, err := ContentPin(args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if len(files) == 0 {
		// An empty tree pins to a constant, so every empty bundle would collide;
		// mold refuses one, and the lane refuses it first.
		fmt.Fprintf(stderr, "%s holds no files\n", args[0])
		return 1
	}
	fmt.Fprintln(stdout, pin)
	for _, f := range files {
		fmt.Fprintln(stdout, f)
	}
	return 0
}

var pinRE = regexp.MustCompile(`^g[0-9a-f]{12}$`)

// ParseListing reads castpin's output back.
func ParseListing(out string) (pin string, files []string, err error) {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if !pinRE.MatchString(lines[0]) {
		return "", nil, fmt.Errorf("castpin answered no pin: %.200q", out)
	}
	if len(lines) < 2 {
		return "", nil, fmt.Errorf("castpin pinned %s and listed no files", lines[0])
	}
	return lines[0], lines[1:], nil
}

// Result is hephaestus's answer to a bundle mint (hephaestus internal/bundle Result).
type Result struct {
	Channel string `json:"channel"`
	Index   int    `json:"version_index"`
	Pin     string `json:"pin"`
	Digest  string `json:"digest"`
	Signed  bool   `json:"signed"`
	NoOp    bool   `json:"noop"`
}

// Verb is the mint's wire name: hephaestus's cast under the gateway's layer
// prefix (D17), asked signed (D12). It is the only name the lane asks.
const Verb = "layer_cast"

// Minted folds hades's answer to Verb into a verdict. staged is the pin
// the lane pushed; hephaestus re-derives the pin from what it pulls and
// refuses a mismatch itself, so an answer carrying another pin is one the lane
// cannot account for.
func Minted(status int, raw, staged string) (Result, int, string) {
	if status == 403 && strings.Contains(raw, "unidentifiable caller") {
		return Result{}, buildlane.CouldNotRun, "could not run: hades did not derive a principal from the peer SVID, so the PDP was never consulted — check the deployed hades revision, not the policy"
	}
	if status == 403 {
		return Result{}, buildlane.Findings, "findings: hades identified the caller and the policy refused it: the cast lane's SVID is not granted " + Verb + " — check policy/authz_grants in foundry-dies"
	}
	if status == 401 {
		return Result{}, buildlane.CouldNotRun, "could not run: hades rejected the caller at the door (401): the client certificate was not presented or did not verify against the SPIRE bundle"
	}
	if status != 200 {
		return Result{}, buildlane.CouldNotRun, fmt.Sprintf("could not run: hades returned HTTP %d", status)
	}
	var answer struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(raw), &answer); err != nil || len(answer.Content) == 0 {
		return Result{}, buildlane.CouldNotRun, "could not run: hades returned a 200 with a body that is not a tool answer"
	}
	text := answer.Content[0].Text
	if answer.IsError {
		code, why := buildlane.ToolFailed(Verb, text)
		return Result{}, code, why
	}
	var r Result
	if err := json.Unmarshal([]byte(text), &r); err != nil {
		return Result{}, buildlane.CouldNotRun, fmt.Sprintf("could not run: "+Verb+"'s answer is not a cast result: %.200q", text)
	}
	if !strings.HasPrefix(r.Digest, "sha256:") {
		return Result{}, buildlane.CouldNotRun, fmt.Sprintf("could not run: "+Verb+" answered no digest: %.200q", text)
	}
	if r.Pin != staged {
		return Result{}, buildlane.CouldNotRun, fmt.Sprintf("could not run: hephaestus minted pin %s, and the lane staged %s", r.Pin, staged)
	}
	if !r.Signed {
		return Result{}, buildlane.Findings, fmt.Sprintf("findings: hephaestus minted %s@%s and did not sign it", r.Channel, r.Digest)
	}
	return r, buildlane.Clean, ""
}

// Doorbell is the record the cast rings hephaestus.releases with, in the
// redpanda REST proxy's JSON shape: the hosts' delivery timer reads the same
// fields, so a rung bell only makes a deploy faster.
func Doorbell(c Cast, r Result) string {
	b, _ := json.Marshal(map[string]any{"records": []map[string]any{{"value": map[string]any{
		"kind": c.kind(), "name": c.Name, "channel": r.Channel, "index": r.Index, "pin": r.Pin, "digest": r.Digest,
	}}}})
	return string(b)
}
