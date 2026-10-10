// Package governlane is the governance cast's decisions as pure functions: which
// dies.toml entries are a consumer's governance, whether they may be cast at all,
// which channel each rides, and how the dies' verdicts fold into the lane's one.
// govern.go is the chain.
//
// WHAT IT CASTS. A governance die is no longer composed at cast time: since Nomos
// F6 it is the verbatim subtree renders/<consumer>/<provider> of foundry-stocks,
// which Nomos assembled and a door landing inscribed (dies.toml says so, one
// source+path stanza per consumer). The lane is constructed on the landing, so
// the tree it reads IS that commit and the payload is exactly the render.
//
// WHICH CHANNEL. runtime-gov/governance.<consumer>:stable. tongs and anvil know
// three bundle kinds (app, runtime-gov, repo-gov), layer_cast mints only those,
// and anvil skips runtime-gov, so nothing flips a governance payload into place:
// tongs stages it and gavel lays from the staged slot. The dotted name keeps
// these disjoint from the memory.<lane> channels on the same kind, the way
// memory.<lane> was kept disjoint from everything else.
package governlane

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/castlane"
)

const (
	// Prefix is the die class a consumer's governance rides in dies.toml.
	Prefix = "governance/"
	// RendersRoot is where the tablet keeps what Nomos assembled.
	RendersRoot = "renders/"
	// BundleKind is the bundle kind every governance channel rides.
	BundleKind = "runtime-gov"
	// NamePrefix is the channel name's prefix: governance.<consumer>.
	NamePrefix = "governance."
)

// DefaultConsumers are the consumers whose governance is cast when the caller
// names none. home is not among them and cannot be added: see Held.
var DefaultConsumers = []string{"forge-root", "vault"}

// Held are the consumers whose render may not enter the registry, and why. The
// refusal is in the code and not in the default list alone, so a caller that
// names the consumer is refused too: lifting a hold is an edit to this map,
// reviewed, and nothing else.
var Held = map[string]string{
	"home": "its render carries personal data and has not been approved to enter the registry",
}

// consumerRE is a consumer's name: one lowercase segment, no dots, so a name can
// never collide with a dotted channel name or leave its directory.
var consumerRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Die is one consumer's governance die: the stanza dies.toml holds for it.
type Die struct {
	Consumer string
	// Path is the subtree of the tablet the die is: renders/<consumer>/<provider>.
	Path string
}

// Name is the die's name in dies.toml: governance/<consumer>.
func (d Die) Name() string { return Prefix + d.Consumer }

// Cast is the channel the die is minted into.
func (d Die) Cast() castlane.Cast {
	return castlane.Cast{Kind: BundleKind, Name: NamePrefix + d.Consumer}
}

// diesFile is the part of dies.toml this lane reads.
type diesFile struct {
	Dies map[string]struct {
		Source  string   `toml:"source"`
		Path    string   `toml:"path"`
		Kit     string   `toml:"kit"`
		Exclude []string `toml:"exclude"`
	} `toml:"dies"`
}

// RepoOf is the repository a git URL names: its path without the ref a source
// pins (@main) or the .git suffix, so a source stanza and the URL a lane was
// constructed on compare. A URL that does not parse names none.
func RepoOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	repo, _, _ := strings.Cut(strings.Trim(u.Path, "/"), "@")
	return strings.TrimSuffix(repo, ".git")
}

// Plan answers the dies to cast for the consumers asked, in the order asked,
// read from dies.toml. self is the repository the lane was constructed on: a
// governance die whose source is another repository is not a subtree of the tree
// in hand, and casting the tree in hand under its name would sign the wrong
// bytes with the right name. Every refusal is about the request or the registry,
// so the lane settles it as a finding.
func Plan(diesToml string, consumers []string, self string) ([]Die, error) {
	if RepoOf(self) == "" {
		return nil, errors.New("the lane was constructed on no repository, so no die's source can be matched to the tree in hand")
	}
	if len(consumers) == 0 {
		return nil, errors.New("no consumer was asked for")
	}
	var file diesFile
	if _, err := toml.Decode(diesToml, &file); err != nil {
		return nil, fmt.Errorf("dies.toml does not parse: %v", err)
	}
	var out []Die
	seen := map[string]bool{}
	for _, c := range consumers {
		if why, held := Held[c]; held {
			return nil, fmt.Errorf("%s%s is held: %s", Prefix, c, why)
		}
		if !consumerRE.MatchString(c) {
			return nil, fmt.Errorf("%q is not a consumer name: one lowercase segment of [a-z0-9-]", c)
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		e, ok := file.Dies[Prefix+c]
		if !ok {
			return nil, fmt.Errorf("dies.toml has no [dies.%q] stanza", Prefix+c)
		}
		if e.Kit != "" || e.Source == "" || e.Path == "" {
			return nil, fmt.Errorf("%s%s is not a path die: it needs a source and a path and no kit", Prefix, c)
		}
		if len(e.Exclude) > 0 {
			return nil, fmt.Errorf("%s%s carries excludes, which a governance render never does: the payload is the render's files and nothing filtered", Prefix, c)
		}
		if RepoOf(e.Source) != RepoOf(self) {
			return nil, fmt.Errorf("%s%s is sourced from %s, not from this repository (%s)", Prefix, c, RepoOf(e.Source), RepoOf(self))
		}
		if path.Clean(e.Path) != e.Path || !strings.HasPrefix(e.Path, RendersRoot+c+"/") {
			return nil, fmt.Errorf("%s%s has path %q, which is not a subtree of %s%s/", Prefix, c, e.Path, RendersRoot, c)
		}
		out = append(out, Die{Consumer: c, Path: e.Path})
	}
	return out, nil
}

// Outcome is what one die's cast settled on.
type Outcome struct {
	Die    string
	Code   int
	Reason string
}

// Fold is the lane's one verdict over its dies: the worst code any die settled
// on, with every die's reason. Every die is attempted, so one that cannot be cast
// does not starve the others, and the lane still settles on the failure.
func Fold(outs []Outcome) (int, string) {
	if len(outs) == 0 {
		return buildlane.CouldNotRun, "could not run: no governance die was cast"
	}
	code := buildlane.Clean
	parts := make([]string, 0, len(outs))
	for _, o := range outs {
		code = max(code, o.Code)
		parts = append(parts, o.Die+": "+o.Reason)
	}
	return code, strings.Join(parts, "; ")
}

// digestRE is one manifest digest, whole.
var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Current answers the digest a channel's head carries when that head IS the
// render's own manifest, and "" otherwise. resolved is what the registry said
// for the channel tag and then for the render's pin tag, one digest a line:
// the two agree only when the head was minted from this very render.
//
// WHY THE LANE ASKS BEFORE IT MINTS. hephaestus answers a mint of the head's
// own pin as a no-op, but it re-signs that digest on the way (it heals a head a
// crashed cast left unsigned), and a cosign v3 signature is a new referrer
// every time: app/gavel's one digest carried six on 2026-10-10. The trigger
// polls every ten minutes, so minting an unchanged render would add two
// referrers per channel per period, forever, for tongs to wade through on each
// verify. A head that is current and verifies is left alone; anything else —
// a tag that does not resolve, a head on another pin, a registry that did not
// answer — answers "" and the cast runs in full, which says why if it fails.
func Current(resolved string) string {
	lines := strings.Fields(resolved)
	if len(lines) != 2 || lines[0] != lines[1] || !digestRE.MatchString(lines[0]) {
		return ""
	}
	return lines[0]
}
