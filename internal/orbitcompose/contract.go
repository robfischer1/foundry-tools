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
	Version string   `toml:"version"`
	Status  string   `toml:"status"`
	Verbs   []string `toml:"verbs"`
	Via     string   `toml:"via"`
}

// ident is what may appear between the quotes this package writes: every
// star, verb, status and version in the fleet is one. Holding
// values to it is what lets render quote without escaping.
var ident = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)

// starName is a star as a two-part file name reads it: lowercase, no hyphen,
// so producer-consumer splits one way only.
var starName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// rosterStar is a star the roster names: starName, hyphens allowed
// (blade-runner, stellar-core-go). A roster directory outside it is never a
// party, so a name cannot smuggle a character render could not quote.
var rosterStar = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// JobPrefix names a CI lane as a contract's consumer: job.<lane>, a run of
// that lane (spiffe://notusmi.com/job/<lane>/<id>; Rob, 2026-10-04). The
// endpoint PEP holds a job peer to the [[produces]] entry for job.<lane>
// (stellar-core-go policy.ConsumerOf). No star name holds a dot, so the two
// never collide.
const JobPrefix = "job."

// jobConsumer is a job consumer as a file name carries it: job.<lane>, the
// lane a k8s label value Daedalus stamps (gate, mutation-bg, …).
var jobConsumer = regexp.MustCompile(`^job\.[a-z][a-z0-9-]*$`)

// IsJob reports whether a contract party is a CI lane rather than a star.
func IsJob(party string) bool { return strings.HasPrefix(party, JobPrefix) }

// splitJob answers <producer>-job.<lane> as its producer and job consumer.
func splitJob(name string) (producer, consumer string, ok bool) {
	// A name with no "-job." cuts to an empty lane, which jobConsumer refuses.
	p, lane, _ := strings.Cut(name, "-"+JobPrefix)
	consumer = JobPrefix + lane
	return p, consumer, rosterStar.MatchString(p) && jobConsumer.MatchString(consumer)
}

// SplitName answers the producer and consumer a contract file is named for.
// A consumer job.<lane> is a CI lane (splitJob); every other name is two stars.
//
// With a roster (stars: every star on foundry-dies' fleet/stars) a star name
// may carry hyphens, so the split is the one (producer, consumer) with
// producer-consumer == the name and BOTH on the roster. Two such readings are
// an error naming both. No such reading falls back to the two-part rule, so a
// name that holds no hyphenated star splits exactly as it always did (and a
// party off the roster is orbit:contracts' party-not-on-roster, not a
// refusal to read the file). A nil roster is the two-part rule alone.
func SplitName(file string, stars map[string]bool) (producer, consumer string, err error) {
	name := strings.TrimSuffix(filepath.Base(file), ".toml")
	if p, c, ok := splitJob(name); ok {
		return p, c, nil
	}
	reads := rosterReads(name, stars)
	if len(reads) > 1 {
		return "", "", fmt.Errorf("%s: ambiguous, it reads as %s and as %s — both are stars on the roster",
			file, reads[0], reads[1])
	}
	if len(reads) == 1 {
		return reads[0].producer, reads[0].consumer, nil
	}
	parts := strings.Split(name, "-")
	if len(parts) != 2 || !starName.MatchString(parts[0]) || !starName.MatchString(parts[1]) {
		return "", "", fmt.Errorf("%s: a contract is named <producer>-<consumer>.toml, two star names", file)
	}
	return parts[0], parts[1], nil
}

// reading is one way a contract name splits into two roster stars.
type reading struct{ producer, consumer string }

// String is the reading as the error names it: producer->consumer.
func (r reading) String() string { return r.producer + "->" + r.consumer }

// rosterReads is every split of name at a hyphen whose two halves are both
// stars on the roster, in left-to-right order.
func rosterReads(name string, stars map[string]bool) []reading {
	var out []reading
	for i := 0; i < len(name); i++ {
		if name[i] != '-' {
			continue
		}
		p, c := name[:i], name[i+1:]
		if stars[p] && stars[c] && rosterStar.MatchString(p) && rosterStar.MatchString(c) {
			out = append(out, reading{p, c})
		}
	}
	return out
}

// ParseContract reads one contract file's bytes; stars is SplitName's roster.
func ParseContract(file string, raw []byte, stars map[string]bool) (Contract, error) {
	producer, consumer, err := SplitName(file, stars)
	if err != nil {
		return Contract{}, err
	}
	var doc contractDoc
	if _, err := toml.Decode(string(raw), &doc); err != nil {
		return Contract{}, fmt.Errorf("%s: does not parse: %w", file, err)
	}
	c := Contract{
		Name: producer + "-" + consumer, Producer: producer, Consumer: consumer,
		Version: doc.Version, Status: doc.Status,
		Verbs: sortedSet(doc.Verbs), Via: cmp.Or(doc.Via, ViaMCP), Digest: Digest(raw),
	}
	return c, validate(file, c)
}

// validate refuses what the reader would refuse or misread: a blank version
// or status (a block with no status is never approved, so emitting one would
// state a grant nobody can climb), no verbs, or a value
// render cannot quote verbatim.
func validate(file string, c Contract) error {
	for _, f := range []struct{ key, val string }{
		{"version", c.Version}, {"status", c.Status}, {"via", c.Via},
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
