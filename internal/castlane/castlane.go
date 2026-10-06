// Package castlane is the cast lane's decisions as pure functions: what a
// record asks the lane to cast, the staged payload's content pin, and how
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
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/buildlane"
)

// Cast is what one record asks the lane to cast.
//
// Name is the CHANNEL's name: meta.name for a binary repo, and the kit's own
// name for a kit repo (Kit != ""), because one repo can cast a kit whose name
// is not its own — foundry-stocks casts forge-user, not foundry-stocks.
type Cast struct {
	Name         string
	Binaries     []string
	PayloadExtra []string
	// Kit, when set, is the stocks kit whose standalone render IS the payload
	// (under KitDir), in place of release binaries. A cast is binaries XOR a
	// kit, never both: two producers on one channel would ship whichever the
	// lane ran last.
	Kit string
}

// Ships names what the cast puts in the payload, for the record's one line and
// the lane's log: the binaries, or the kit whose render it is.
func (c Cast) Ships() string {
	if c.Kit != "" {
		return "the " + c.Kit + " kit's render"
	}
	return strings.Join(c.Binaries, ", ")
}

// Billet is the billet furnace renders from: the repository the lane's tree was
// fetched from, read back through the door's public address at the exact commit,
// as the URL@ref furnace's FURNACE_SOURCE takes. The lane's own address for the
// repo is the engine's, and a container furnace runs in holds no credential for
// it; the door serves anonymous read at the same path.
//
// IT CANNOT REFUSE. A repo that does not parse as a URL is taken as its own
// path, which is what the lane's star name was already read from.
func Billet(door, repo, sha string) string {
	u, err := url.Parse(repo)
	if err != nil {
		u = &url.URL{Path: repo}
	}
	p := "/" + strings.TrimPrefix(strings.TrimSuffix(u.Path, ".git"), "/") + ".git"
	return strings.TrimSuffix(door, "/") + p + "@" + sha
}

// KitDir is where a kit cast's render lands in the payload. The render goes in
// a directory of its own, never at the payload's root, because the consumer
// lays it with `gavel order --from <staged>/die` and everything else in the
// bundle (units/, beside it) is NOT governance and must not be laid at $HOME.
const KitDir = "die"

// kitRE is a channel name's grammar (hephaestus internal/bundle nameRE): one
// lowercase segment. A kit that cannot be a channel is refused here, not at
// the mint.
var kitRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Artifact is the bundle channel the cast mints into. It is app/<name>:stable
// for every cast, which is why the record carries no artifact field. The kind
// is app for a kit as well, on purpose: anvil flips only the app kind
// (bundle_reconcile.rs is_app), so a repo-gov channel would be staged by tongs
// and never flipped by anything.
func (c Cast) Artifact() string { return "app/" + c.Name + ":stable" }

// Stage is the reference the payload is pushed to before mold consumes it:
// furnace stage's {host}/staging/{name}:{pin}, the channel's kind and name
// joined so the staging name stays one bare segment.
func (c Cast) Stage(host, pin string) string { return host + "/staging/app-" + c.Name + ":" + pin }

// Target is where a payload_extra path lands in the payload: under its own
// basename, so .cerberus/hooks ships as hooks/ beside the binaries.
func Target(extra string) string { return path.Base(path.Clean(extra)) }

// FromRecord reads a v3 record's cast arguments. Every refusal is about the
// record, so the lane settles it as a finding.
func FromRecord(slag string) (Cast, error) {
	var rec struct {
		Schema string `json:"$schema"`
		Meta   struct {
			Name     string   `json:"name"`
			Produces []string `json:"produces"`
		} `json:"meta"`
		Tools struct {
			Cast *struct {
				Binaries     []string `json:"binaries"`
				PayloadExtra []string `json:"payload_extra"`
				Kit          string   `json:"kit"`
			} `json:"cast"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(slag), &rec); err != nil {
		return Cast{}, fmt.Errorf("the record does not parse: %v", err)
	}
	if !strings.Contains(rec.Schema, "slag-v3") {
		return Cast{}, errors.New("the record is not v3, and only a v3 record carries tools.cast")
	}
	if rec.Meta.Name == "" {
		return Cast{}, errors.New("the record names no meta.name")
	}
	if !slices.Contains(rec.Meta.Produces, "binary") {
		return Cast{}, fmt.Errorf("the record produces %v, not binary, so there is nothing to cast", rec.Meta.Produces)
	}
	if rec.Tools.Cast == nil {
		return Cast{}, errors.New("the record produces binary and carries no tools.cast, so the lane has no binaries to ship")
	}
	c := Cast{Name: rec.Meta.Name, Binaries: rec.Tools.Cast.Binaries, PayloadExtra: rec.Tools.Cast.PayloadExtra, Kit: rec.Tools.Cast.Kit}
	switch {
	case c.Kit != "" && len(c.Binaries) > 0:
		return Cast{}, errors.New("tools.cast names both binaries and a kit, and a channel has one producer")
	case c.Kit != "":
		if !kitRE.MatchString(c.Kit) {
			return Cast{}, fmt.Errorf("tools.cast.kit: %q is not a channel name (one lowercase segment)", c.Kit)
		}
		c.Name = c.Kit
	case len(c.Binaries) == 0:
		return Cast{}, errors.New("tools.cast.binaries is empty")
	}
	// Every binary and every extra lands at the payload's root under one name;
	// two claims on a name would ship whichever was written last.
	lands := map[string]string{}
	claim := func(name, what string) error {
		if prior, ok := lands[name]; ok {
			return fmt.Errorf("%s and %s both land at %q in the payload", prior, what, name)
		}
		lands[name] = what
		return nil
	}
	if c.Kit != "" {
		// The render holds its name before anything else can ask for it, so a
		// binary or an extra that lands at KitDir is the one refused.
		lands[KitDir] = "the kit's render"
	}
	for _, b := range c.Binaries {
		if b == "" || strings.ContainsAny(b, `/\`) || strings.HasPrefix(b, ".") || strings.HasPrefix(b, "-") {
			return Cast{}, fmt.Errorf("tools.cast.binaries: %q is not a file name out of target/release", b)
		}
		if err := claim(b, "binary "+b); err != nil {
			return Cast{}, err
		}
	}
	for _, p := range c.PayloadExtra {
		if p == "" || strings.HasPrefix(p, "/") || slices.Contains(strings.Split(p, "/"), "..") {
			return Cast{}, fmt.Errorf("tools.cast.payload_extra: %q is not a path inside the repo", p)
		}
		t := Target(p)
		if t == "." {
			return Cast{}, fmt.Errorf("tools.cast.payload_extra: %q names the repo itself", p)
		}
		if err := claim(t, "payload_extra "+p); err != nil {
			return Cast{}, err
		}
	}
	return c, nil
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
		"kind": "app", "name": c.Name, "channel": r.Channel, "index": r.Index, "pin": r.Pin, "digest": r.Digest,
	}}}})
	return string(b)
}
