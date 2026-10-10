// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package govlaw

import (
	_ "embed"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"dagger/foundry-tools/internal/retiredverbs"
)

// VerbsID is the verb-liveness lane's catalogue id.
const VerbsID = "law:verb-liveness"

// embeddedSurface is the snapshot of hades's wire surface that ships with the
// lane. See the file's header for why a snapshot and how to refresh it.
//
//go:embed verb-surface.toml
var embeddedSurface string

// Surface is the verb names the law may call.
type Surface struct {
	verbs    map[string]bool
	prefixes map[string]bool
	notVerbs map[string]bool
}

var wireNameRe = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)+$`)

var verbNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ParseSurface reads a verb-surface.toml. A surface with no verb is refused: a
// lane graded against nothing calls every verb dead.
func ParseSurface(body string) (Surface, error) {
	var doc struct {
		Verbs          []string `toml:"verbs"`
		FormerPrefixes []string `toml:"former_prefixes"`
		NotVerbs       []string `toml:"not_verbs"`
	}
	if _, err := toml.Decode(body, &doc); err != nil {
		return Surface{}, fmt.Errorf("not TOML: %v", err)
	}
	if len(doc.Verbs) == 0 {
		return Surface{}, fmt.Errorf("names no verb")
	}
	s := Surface{verbs: map[string]bool{}, prefixes: map[string]bool{}, notVerbs: map[string]bool{}}
	for _, v := range doc.Verbs {
		if !verbNameRe.MatchString(v) {
			return Surface{}, fmt.Errorf("verb %q is not a wire name", v)
		}
		s.verbs[v] = true
		prefix, _, _ := strings.Cut(v, "_")
		s.prefixes[prefix] = true
	}
	for _, p := range doc.FormerPrefixes {
		s.prefixes[p] = true
	}
	for _, v := range doc.NotVerbs {
		s.notVerbs[v] = true
	}
	return s, nil
}

// Embedded is the snapshot that ships with the lane. It parses by construction;
// a test holds that.
func Embedded() (Surface, error) { return ParseSurface(embeddedSurface) }

// addPrefixes teaches the surface the prefixes of the retired wire names, so a
// bare `fleet_get_checkpoint` is read as a verb mention after the star left.
func (s Surface) addPrefixes(l retiredverbs.Ledger) {
	for _, name := range retiredverbs.Names(l) {
		if wireNameRe.MatchString(name) {
			prefix, _, _ := strings.Cut(name, "_")
			s.prefixes[prefix] = true
		}
	}
}

// live reports whether the surface serves name; a glob (`athena*`) is live when
// some verb is the name or sits under name_.
func (s Surface) live(name string, glob bool) bool {
	if !glob {
		return s.verbs[name]
	}
	if s.verbs[name] {
		return true
	}
	for v := range s.verbs {
		if strings.HasPrefix(v, name+"_") {
			return true
		}
	}
	return false
}

var (
	qualifiedRe = regexp.MustCompile(`mcp__([A-Za-z0-9-]+)__([a-z0-9_]+)(\*?)`)
	codeSpanRe  = regexp.MustCompile("`([^`\n]*)`")
)

// Mention is one verb a line of law names.
type Mention struct {
	Name string
	Glob bool
}

// Mentions finds the hades verbs a line names, in order: every
// `mcp__hades__<verb>` (a trailing `*` or `_` makes it a prefix glob), and every
// backticked word that is shaped like a wire name and carries the prefix of a
// star the fleet has had. A word in backticks that merely looks like snake_case
// (`rhyme_recall`, `session_uuid`) is not a verb mention unless its prefix is a
// star's.
func (s Surface) Mentions(line string) []Mention {
	var out []Mention
	for _, m := range qualifiedRe.FindAllStringSubmatch(line, -1) {
		if m[1] != "hades" {
			continue
		}
		name, glob := m[2], m[3] == "*"
		if strings.HasSuffix(name, "_") {
			name, glob = strings.TrimRight(name, "_"), true
		}
		out = append(out, Mention{name, glob})
	}
	for _, m := range codeSpanRe.FindAllStringSubmatch(line, -1) {
		word, _, _ := strings.Cut(strings.TrimSpace(m[1]), " ")
		word, _, _ = strings.Cut(word, "(")
		prefix, _, _ := strings.Cut(word, "_")
		if wireNameRe.MatchString(word) && s.prefixes[prefix] && !s.notVerbs[word] {
			out = append(out, Mention{word, false})
		}
	}
	return out
}

// VerbLiveness grades the verbs the law names: each must be served by hades's
// surface and none may be in the retired ledger.
//
// WHAT IS READ. The block library and the always-on documents of every render
// (AGENTS.md, CLAUDE.md, APPEND_SYSTEM.md). The skills, hooks and agents a
// render carries are graded for retired names by fleet:retired-verbs, which
// owns that surface.
//
// WHAT COUNTS AS A MENTION. See Surface.Mentions. A retired name is reported
// once, as retired, with the call the ledger gives instead; a name that is not
// retired and not served is reported as not served.
//
// THE SURFACE is the tree's own verb-surface.toml when it carries one, else the
// snapshot embedded in this lane. The live gateway cannot be asked from a gate
// atom (see the snapshot's header), so a verb hades gained since the snapshot
// is a finding until it is added.
func VerbLiveness(t Tree) Result {
	if !IsStocks(t) {
		return absent(VerbsID)
	}
	body, err := t.Read(ledgerFile)
	if err != nil {
		return cannotRun(VerbsID, "%s would not read: %v", ledgerFile, err)
	}
	ledger, err := retiredverbs.Parse(body)
	if err != nil {
		return cannotRun(VerbsID, "%s is not a ledger this lane can grade against: %v", ledgerFile, err)
	}
	surface, source, res := t.surface()
	if res != nil {
		return *res
	}
	surface.addPrefixes(ledger)

	units := t.verbUnits()
	if len(units) == 0 {
		return cannotRun(VerbsID, "%s is here and no block or render is: nothing under %s or %s was found to grade, so the law moved and this lane grades nothing until it knows where", ledgerFile, blocksDir, rendersDir)
	}
	var findings []string
	checked := 0
	for _, p := range units {
		text, err := t.Read(p)
		if err != nil {
			return cannotRun(VerbsID, "%s would not read: %v", p, err)
		}
		retiredAt := map[int]map[string]bool{}
		for _, h := range retiredverbs.Find(p, text, ledger) {
			findings = append(findings, fmt.Sprintf("%s:%d  %s is retired; call instead: %s", p, h.Line, h.Verb, ledger[h.Verb].Successor))
			if retiredAt[h.Line] == nil {
				retiredAt[h.Line] = map[string]bool{}
			}
			retiredAt[h.Line][h.Verb] = true
		}
		for i, line := range strings.Split(text, "\n") {
			for _, m := range surface.Mentions(line) {
				checked++
				if surface.live(m.Name, m.Glob) || retiredAt[i+1][m.Name] {
					continue
				}
				shown := m.Name
				if m.Glob {
					shown += "*"
				}
				findings = append(findings, fmt.Sprintf("%s:%d  %s is not a verb on hades's surface (%s)", p, i+1, shown, source))
			}
		}
	}
	return verdict(VerbsID, fmt.Sprintf("%d verb mention(s) in %d unit(s) are all served by hades (%s) and none is retired", checked, len(units), source), findings, nil)
}

// surface is the surface this tree is graded against and where it came from.
func (t Tree) surface() (Surface, string, *Result) {
	body, source := embeddedSurface, "the snapshot embedded in foundry-tools"
	if t.has(surfaceFile) {
		b, err := t.Read(surfaceFile)
		if err != nil {
			r := cannotRun(VerbsID, "%s would not read: %v", surfaceFile, err)
			return Surface{}, "", &r
		}
		body, source = b, surfaceFile
	}
	s, err := ParseSurface(body)
	if err != nil {
		r := cannotRun(VerbsID, "%s is not a surface this lane can grade against: %v", source, err)
		return Surface{}, "", &r
	}
	return s, source, nil
}

// verbUnits is the files that carry law prose: blocks, then the always-on
// documents of every render, each sorted.
func (t Tree) verbUnits() []string {
	units, _ := t.matching(blockRe)
	docs, _ := t.matching(renderDocRe)
	return slices.Concat(units, docs)
}
