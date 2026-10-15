package checks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CAPABILITIES (Capabilities plan S3). Every star declares the verbs it serves in
// OPA, as data.capabilities.stars[star] = {prefix, verbs, tier}, and a capability
// nobody declared is denied. The declaration is RENDERED from what each star's
// code serves (stellar-core mcpserve.ServedExport), never written by hand, and
// dies:capabilities-served is the gate that says the render and the code still
// agree. A verb the code serves and the document does not name ships denied to
// every caller, which is the failure this exists to catch before the deploy.
//
// Everything here is pure: bytes in, bytes or findings out. The atom reads the
// tree; the judgement is on this side so a table test can hold each case.

// Paths in the policy die's tree. CapabilitiesData is the document S1 will land
// in the bundle; CapabilitiesServedDir is where each star's export is committed
// (<star>.json), kept OUTSIDE policy/ so no bundle build can load it.
const (
	CapabilitiesDir       = "policy/capabilities"
	CapabilitiesData      = CapabilitiesDir + "/data.json"
	CapabilitiesServedDir = "capabilities/served"
)

// ServedSchema is the version of the export document stellar-core writes.
const ServedSchema = 1

// Tier names, per verb. They are the audience classes of Session Authz F10
// (policy/authz_audience): a verb listed under star_only is visible to star
// principals only, one under session_hidden is hidden from session principals,
// and every unlisted verb is client-visible, which is "default".
const (
	TierDefault       = "default"
	TierStarOnly      = "star_only"
	TierSessionHidden = "session_hidden"
)

// Served is one star's export: BARE verb names, as the star registers them.
type Served struct {
	Schema int      `json:"schema"`
	Star   string   `json:"star"`
	Verbs  []string `json:"verbs"`
}

// ParseServed reads an export and refuses what it cannot vouch for: another
// schema version, no star, no verbs list, an empty or duplicated name.
// `name` is the file's star (its stem); an export that names another star was
// copied from one and is refused rather than believed.
func ParseServed(name string, raw []byte) (Served, error) {
	var s Served
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Served{}, fmt.Errorf("not a served-verbs export: %w", err)
	}
	if s.Schema != ServedSchema {
		return Served{}, fmt.Errorf("schema %d, this gate reads schema %d", s.Schema, ServedSchema)
	}
	if s.Star == "" {
		return Served{}, fmt.Errorf("no star")
	}
	if name != "" && s.Star != name {
		return Served{}, fmt.Errorf("names star %q but is committed as %q", s.Star, name)
	}
	if s.Verbs == nil {
		return Served{}, fmt.Errorf("no verbs list (an empty surface is [], not absent)")
	}
	seen := map[string]bool{}
	for _, v := range s.Verbs {
		if v == "" {
			return Served{}, fmt.Errorf("an empty verb name")
		}
		if seen[v] {
			return Served{}, fmt.Errorf("verb %q listed twice", v)
		}
		seen[v] = true
	}
	return s, nil
}

// CapabilityStar is one star's declaration. Tier maps each bare verb to its
// audience class; it is per verb because F10 classifies verbs, not stars.
type CapabilityStar struct {
	Prefix string            `json:"prefix"`
	Verbs  []string          `json:"verbs"`
	Tier   map[string]string `json:"tier"`
}

// Capabilities is the document at CapabilitiesData: data.capabilities.
type Capabilities struct {
	Stars map[string]CapabilityStar `json:"stars"`
}

// Audience is policy/authz_audience's two lists, keyed by star and holding
// GATEWAY names (prefix_verb).
type Audience struct {
	StarOnly      map[string][]string `json:"star_only"`
	SessionHidden map[string][]string `json:"session_hidden"`
}

// TierOf classifies a gateway verb of a star. star_only wins over session_hidden
// (a verb in both is already invisible to the wider audience).
func (a Audience) TierOf(star, gateway string) string {
	for _, g := range a.StarOnly[star] {
		if g == gateway {
			return TierStarOnly
		}
	}
	for _, g := range a.SessionHidden[star] {
		if g == gateway {
			return TierSessionHidden
		}
	}
	return TierDefault
}

// RenderCapabilities renders the declaration from the stars' exports.
//
// prefixes maps star to its route namespace (fleet/repos/<star>.json verb_prefix);
// a star with no entry has "no verbs of its own", so one that nevertheless serves
// verbs is an error, not a default: the gateway name cannot be derived from the
// star's name and a guess would declare verbs nobody can dial. The output is the
// canonical form (sorted keys at every depth, two-space indent, trailing LF) so
// the same inputs render the same bytes.
func RenderCapabilities(served []Served, prefixes map[string]string, aud Audience) ([]byte, error) {
	doc := Capabilities{Stars: map[string]CapabilityStar{}}
	var problems []string
	for _, s := range served {
		if _, dup := doc.Stars[s.Star]; dup {
			problems = append(problems, fmt.Sprintf("%s: exported twice", s.Star))
			continue
		}
		prefix := prefixes[s.Star]
		if prefix == "" {
			if len(s.Verbs) > 0 {
				problems = append(problems, fmt.Sprintf("%s: serves %d verb(s) but its record has no verb_prefix", s.Star, len(s.Verbs)))
				continue
			}
			continue // no verbs of its own, none served: nothing to declare
		}
		verbs := append([]string{}, s.Verbs...)
		sort.Strings(verbs)
		tier := map[string]string{}
		for _, v := range verbs {
			tier[v] = aud.TierOf(s.Star, prefix+"_"+v)
		}
		doc.Stars[s.Star] = CapabilityStar{Prefix: prefix, Verbs: verbs, Tier: tier}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("cannot render capabilities: %s", strings.Join(problems, "; "))
	}
	// Round-trip through the generic form so CanonicalJSON sorts every key.
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return CanonicalJSON(raw)
}

// CapabilitiesDrift compares the declared document with the stars' exports and
// names every difference, sorted: the findings of dies:capabilities-served.
//
// Declared and served must be the SAME SET, in both directions:
//   - a served verb the document does not name is denied to every caller;
//   - a declared verb nothing serves is a grant surface for a verb that is gone;
//   - a star on one side only is a surface nobody can reconcile.
//
// It also holds the document to its own shape: the prefix must be the star's
// record's, and every tier entry must name a declared verb with a known class.
func CapabilitiesDrift(declared Capabilities, served []Served, prefixes map[string]string) []string {
	var out []string
	servedBy := map[string]Served{}
	for _, s := range served {
		servedBy[s.Star] = s
	}
	for star := range declared.Stars {
		if _, ok := servedBy[star]; !ok {
			out = append(out, fmt.Sprintf("%s: declared in %s but no served-verbs export is committed under %s/", star, CapabilitiesData, CapabilitiesServedDir))
		}
	}
	for _, s := range served {
		decl, ok := declared.Stars[s.Star]
		if !ok {
			if len(s.Verbs) > 0 {
				out = append(out, fmt.Sprintf("%s: serves %d verb(s) %s but %s declares none - every one is denied", s.Star, len(s.Verbs), quoteList(s.Verbs), CapabilitiesData))
			}
			continue
		}
		if want := prefixes[s.Star]; decl.Prefix != want {
			out = append(out, fmt.Sprintf("%s: declared prefix %q but the star's record says %q", s.Star, decl.Prefix, want))
		}
		missing, extra := setDiff(s.Verbs, decl.Verbs)
		if len(missing) > 0 {
			out = append(out, fmt.Sprintf("%s: served but not declared (denied to every caller): %s", s.Star, quoteList(missing)))
		}
		if len(extra) > 0 {
			out = append(out, fmt.Sprintf("%s: declared but not served: %s", s.Star, quoteList(extra)))
		}
		declaredSet := map[string]bool{}
		for _, v := range decl.Verbs {
			declaredSet[v] = true
		}
		var tierKeys []string
		for v := range decl.Tier {
			tierKeys = append(tierKeys, v)
		}
		sort.Strings(tierKeys)
		for _, v := range tierKeys {
			switch {
			case !declaredSet[v]:
				out = append(out, fmt.Sprintf("%s: tier names %q, which is not a declared verb", s.Star, v))
			case !validTier(decl.Tier[v]):
				out = append(out, fmt.Sprintf("%s: tier of %q is %q, not one of %s, %s, %s", s.Star, v, decl.Tier[v], TierDefault, TierStarOnly, TierSessionHidden))
			}
		}
		for _, v := range decl.Verbs {
			if _, ok := decl.Tier[v]; !ok {
				out = append(out, fmt.Sprintf("%s: declared verb %q has no tier", s.Star, v))
			}
		}
	}
	sort.Strings(out)
	return out
}

func validTier(t string) bool {
	return t == TierDefault || t == TierStarOnly || t == TierSessionHidden
}

// setDiff answers what is in want but not in have, and what is in have but not
// in want, each sorted.
func setDiff(want, have []string) (missing, extra []string) {
	w, h := map[string]bool{}, map[string]bool{}
	for _, v := range want {
		w[v] = true
	}
	for _, v := range have {
		h[v] = true
	}
	for v := range w {
		if !h[v] {
			missing = append(missing, v)
		}
	}
	for v := range h {
		if !w[v] {
			extra = append(extra, v)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

func quoteList(vs []string) string {
	const shown = 8
	s := append([]string{}, vs...)
	sort.Strings(s)
	more := 0
	if len(s) > shown {
		more, s = len(s)-shown, s[:shown]
	}
	out := strings.Join(s, ", ")
	if more > 0 {
		out += fmt.Sprintf(" (+%d more)", more)
	}
	return out
}
