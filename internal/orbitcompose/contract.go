// Package orbitcompose composes each star's orbit sidecar from the canonical
// seam contracts (foundry-dies/orbits/<producer>-<consumer>.toml), and renders
// the set as the machine-owned Flux directory that mounts it (Constellation
// Mesh W3).
//
// THE READER IS stellar-core-go policy.ParseACL, and it is strict: a document
// that does not decode, a [[produces]] block missing `to` or `contract`, a
// non-string version, or two blocks for one consumer is a refused boot for a
// star in witness mode. So this package refuses first: a contract whose
// version is not a string, whose status is blank, or whose names are not
// plain identifiers stops the composition instead of emitting a sidecar the
// star would trip on.
//
// ORIENTATION. In a contract named producer-consumer the PRODUCER serves the
// verbs (the callee) and the CONSUMER calls them. The producer's sidecar
// carries the edge as [[produces]] to = consumer (its inbound ACL); the
// consumer's carries it as [[consumes]] from = producer (its outbound
// reference). Both blocks inline the same contract fields.
package orbitcompose

import (
	"cmp"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// Contract is one seam: Consumer may call Verbs on Producer.
type Contract struct {
	Name     string
	Producer string
	Consumer string
	Version  string
	Status   string
	WireForm string
	Verbs    []string
	// Via is the transport: "mcp" when the file names none, which is every
	// contract today. Carried into the die's index, not into a sidecar.
	Via string
	// Digest is sha256 over the file's bytes (Digest), the die's per-edge
	// pin.
	Digest string
}

// contractDoc is the on-disk contract. Typed fields make an integer version
// a decode error rather than a silently empty string.
type contractDoc struct {
	Version  string   `toml:"version"`
	Status   string   `toml:"status"`
	WireForm string   `toml:"wire_form"`
	Verbs    []string `toml:"verbs"`
	Via      string   `toml:"via"`
}

// ident is what may appear between the quotes this package writes: every
// star, verb, status, version and wire form in the fleet is one. Holding
// values to it is what lets render quote without escaping.
var ident = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)

// starName is a star: lowercase, no hyphen, so producer-consumer splits one
// way only.
var starName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// SplitName answers the producer and consumer a contract file is named for.
func SplitName(file string) (producer, consumer string, err error) {
	name := strings.TrimSuffix(filepath.Base(file), ".toml")
	parts := strings.Split(name, "-")
	if len(parts) != 2 || !starName.MatchString(parts[0]) || !starName.MatchString(parts[1]) {
		return "", "", fmt.Errorf("%s: a contract is named <producer>-<consumer>.toml, two star names", file)
	}
	return parts[0], parts[1], nil
}

// ParseContract reads one contract file's bytes.
func ParseContract(file string, raw []byte) (Contract, error) {
	producer, consumer, err := SplitName(file)
	if err != nil {
		return Contract{}, err
	}
	var doc contractDoc
	if _, err := toml.Decode(string(raw), &doc); err != nil {
		return Contract{}, fmt.Errorf("%s: does not parse: %w", file, err)
	}
	c := Contract{
		Name: producer + "-" + consumer, Producer: producer, Consumer: consumer,
		Version: doc.Version, Status: doc.Status, WireForm: doc.WireForm,
		Verbs: sortedSet(doc.Verbs), Via: cmp.Or(doc.Via, ViaMCP), Digest: Digest(raw),
	}
	return c, validate(file, c)
}

// validate refuses what the reader would refuse or misread: a blank version
// or status (a block with no status is never approved, so emitting one would
// state a grant nobody can climb), a blank wire form, no verbs, or a value
// render cannot quote verbatim.
func validate(file string, c Contract) error {
	for _, f := range []struct{ key, val string }{
		{"version", c.Version}, {"status", c.Status}, {"wire_form", c.WireForm}, {"via", c.Via},
	} {
		if !ident.MatchString(f.val) {
			return fmt.Errorf("%s: %s %q is blank or not a plain identifier", file, f.key, f.val)
		}
	}
	if len(c.Verbs) == 0 {
		return fmt.Errorf("%s: carries no verbs", file)
	}
	for _, v := range c.Verbs {
		if !ident.MatchString(v) {
			return fmt.Errorf("%s: verb %q is not a plain identifier", file, v)
		}
	}
	return nil
}

// sortedSet is verbs sorted with duplicates dropped, so a contract that
// lists a verb twice composes to the same bytes as one that lists it once.
func sortedSet(verbs []string) []string {
	out := slices.Clone(verbs)
	slices.Sort(out)
	return slices.Compact(out)
}
