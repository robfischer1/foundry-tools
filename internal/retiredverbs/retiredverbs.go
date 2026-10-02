// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

// Package retiredverbs reads the fleet's ledger of retired MCP verb names and
// finds the units that still name one.
//
// The ledger is foundry-stocks' retired-verbs.toml. A verb that leaves the
// gateway's surface does not fail its callers loudly: a skill's allowed-tools
// row, a hook's hardcoded name and a governance line all go quiet. The ledger
// records each retired wire name with the call that replaces it, and this
// package is what makes the ledger a check instead of a note.
package retiredverbs

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Entry is one retired wire name: who served it, and what to call instead.
type Entry struct {
	Star      string `toml:"star"`
	Successor string `toml:"successor"`
	RetiredBy string `toml:"retired_by"`
}

// Ledger maps a retired wire name to its entry.
type Ledger map[string]Entry

// Parse reads a ledger. An entry with no successor is refused: the successor
// is copied into the finding, and a finding that cannot say what to call
// instead only tells its reader that something is wrong.
func Parse(body string) (Ledger, error) {
	var doc struct {
		Retired Ledger `toml:"retired"`
	}
	if _, err := toml.Decode(body, &doc); err != nil {
		return nil, fmt.Errorf("not TOML: %v", err)
	}
	if len(doc.Retired) == 0 {
		return nil, fmt.Errorf("no [retired.<name>] entry")
	}
	for _, name := range Names(doc.Retired) {
		if strings.TrimSpace(doc.Retired[name].Successor) == "" {
			return nil, fmt.Errorf("[retired.%s] names no successor", name)
		}
	}
	return doc.Retired, nil
}

// Names is the ledger's wire names, sorted.
func Names(l Ledger) []string {
	names := make([]string, 0, len(l))
	for name := range l {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// scannedDirs is the composition surface: the directories whose files reach a
// session as a skill, an agent, a command, a hook, a setting or a rule.
var scannedDirs = []string{"skills", "agents", "commands", "hooks", "settings", "governance", "rules"}

// scannedSuffixes is what a unit is written in.
var scannedSuffixes = []string{".md", ".toml", ".py", ".json"}

// Scanned reports whether path is a unit this check reads.
func Scanned(path string) bool {
	path = strings.TrimPrefix(path, "./")
	dir, _, nested := strings.Cut(path, "/")
	if !nested {
		return false
	}
	inDir := false
	for _, d := range scannedDirs {
		if dir == d {
			inDir = true
		}
	}
	if !inDir {
		return false
	}
	for _, s := range scannedSuffixes {
		if strings.HasSuffix(path, s) {
			return true
		}
	}
	return false
}

// Hit is one place a unit still names a retired verb.
type Hit struct {
	Path string
	Line int
	Verb string
}

// Find returns every whole-name occurrence of a retired verb in text, in line
// order, one hit per verb per line.
//
// WHOLE NAME, NOT SUBSTRING. The check this replaces matched substrings, and a
// retired `ourea_pr` would have fired on the metric `ourea_pr_node_mint_total`.
// A name matches when nothing that could extend an identifier sits on either
// side of it. The one exception is on the left: a session spells a gateway
// verb `mcp__<server>__<verb>`, so a name preceded by `__` is that verb and is
// exactly the stale grant this check exists to catch.
func Find(path, text string, l Ledger) []Hit {
	names := Names(l)
	var hits []Hit
	for i, line := range strings.Split(text, "\n") {
		for _, name := range names {
			if namesWhole(line, name) {
				hits = append(hits, Hit{Path: path, Line: i + 1, Verb: name})
			}
		}
	}
	return hits
}

// namesWhole reports whether line holds name as a whole identifier.
func namesWhole(line, name string) bool {
	for from := 0; ; {
		at := strings.Index(line[from:], name)
		if at < 0 {
			return false
		}
		start := from + at
		end := start + len(name)
		if leftOpen(line, start) && (end == len(line) || !identByte(line[end])) {
			return true
		}
		from = start + 1
	}
}

// leftOpen reports whether an identifier could start at start.
func leftOpen(line string, start int) bool {
	if start == 0 || !identByte(line[start-1]) {
		return true
	}
	return start >= 2 && line[start-2:start] == "__"
}

// identByte reports whether b can be part of a verb name.
func identByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
