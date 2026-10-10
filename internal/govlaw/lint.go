// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package govlaw

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// LintID is the lint lane's catalogue id.
const LintID = "law:lint"

var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

var spansPathRe = regexp.MustCompile(`^renders/([^/]+)/([^/]+)/` + regexp.QuoteMeta(spansFileInside) + `$`)

var contextPathRe = regexp.MustCompile(`^renders/([^/]+)/([^/]+)/` + regexp.QuoteMeta(contextDocName) + `$`)

// Lint grades the law tree's structure: the block library, the kit tables and
// the render span maps.
//
// BLOCKS. Every governance/blocks/*.md has text in it, and its file stem is a
// slug. The assembler names a block by its stem and kits.toml names it the same
// way, so a stem that is not a slug is a block no kit can spell reliably.
//
// KITS. A kit's `blocks` name blocks that exist (an error: the assembler
// refuses a dangling block). A kit's `includes` name a bundle or another kit; an
// unknown include is a WARNING, not an error, because the mothballed kits carry
// includes that name what was deleted, and the assembler only meets them when a
// consumer asks for that kit. A cycle through includes is an error.
//
// RENDERS. Every renders/<consumer>/<provider>/.furnace/spans.json parses, has
// the context document it maps, and its spans lie inside that document, in
// order, without overlap, each naming a block that exists. A non-blank context
// document with no span map is a document nobody can attribute to a block.
func Lint(t Tree) Result {
	if !IsStocks(t) {
		return absent(LintID)
	}
	stems := t.blockStems()
	if len(stems) == 0 {
		return cannotRun(LintID, "%s is here and no %s*.md is: the block library moved, so this lane grades nothing until it knows where", kitsFile, blocksDir)
	}
	var findings, warnings []string

	blockPaths, _ := t.matching(blockRe)
	for i, p := range blockPaths {
		body, err := t.Read(p)
		if err != nil {
			return cannotRun(LintID, "%s would not read: %v", p, err)
		}
		if strings.TrimSpace(body) == "" {
			findings = append(findings, p+": the block is empty")
		}
		if !slugRe.MatchString(stems[i]) {
			findings = append(findings, fmt.Sprintf("%s: the stem %q is not a slug (lowercase words joined by single hyphens)", p, stems[i]))
		}
	}

	kitsBody, err := t.Read(kitsFile)
	if err != nil {
		return cannotRun(LintID, "%s would not read: %v", kitsFile, err)
	}
	var kitsDoc struct {
		Kits map[string]struct {
			Blocks   []string `toml:"blocks"`
			Includes []string `toml:"includes"`
		} `toml:"kits"`
	}
	if _, err := toml.Decode(kitsBody, &kitsDoc); err != nil {
		return cannotRun(LintID, "%s is not a kit library this lane can read: %v", kitsFile, err)
	}
	bundles, res := t.bundleNames()
	if res != nil {
		return *res
	}
	for _, name := range sortedKeys(kitsDoc.Kits) {
		kit := kitsDoc.Kits[name]
		for _, b := range kit.Blocks {
			if !slices.Contains(stems, b) {
				findings = append(findings, fmt.Sprintf("%s: kit %q names block %q, which is not in %s", kitsFile, name, b, blocksDir))
			}
		}
		for _, inc := range kit.Includes {
			_, isKit := kitsDoc.Kits[inc]
			if !isKit && !slices.Contains(bundles, inc) {
				warnings = append(warnings, fmt.Sprintf("%s: kit %q includes %q, which is neither a kit nor a bundle", kitsFile, name, inc))
			}
		}
	}
	graph := map[string][]string{}
	for name, kit := range kitsDoc.Kits {
		graph[name] = kit.Includes
	}
	for _, cycle := range includeCycles(graph) {
		findings = append(findings, fmt.Sprintf("%s: kits include each other in a cycle: %s", kitsFile, strings.Join(cycle, " -> ")))
	}

	rf, res := t.lintRenders(stems)
	if res != nil {
		return *res
	}
	findings = append(findings, rf...)

	return verdict(LintID, fmt.Sprintf("%d block(s), %d kit(s) and %d span map(s) are sound", len(stems), len(kitsDoc.Kits), t.countSpans()), findings, warnings)
}

func (t Tree) countSpans() int {
	paths, _ := t.matching(spansPathRe)
	return len(paths)
}

// bundleNames is the names in bundles.toml; the file is optional.
func (t Tree) bundleNames() ([]string, *Result) {
	if !t.has(bundlesFile) {
		return nil, nil
	}
	body, err := t.Read(bundlesFile)
	if err != nil {
		r := cannotRun(LintID, "%s would not read: %v", bundlesFile, err)
		return nil, &r
	}
	var doc struct {
		Bundles map[string]any `toml:"bundles"`
	}
	if _, err := toml.Decode(body, &doc); err != nil {
		r := cannotRun(LintID, "%s is not a bundle library this lane can read: %v", bundlesFile, err)
		return nil, &r
	}
	return sortedKeys(doc.Bundles), nil
}

// includeCycles returns each cycle in the include graph once, as the path that
// closes it (a -> b -> a). A name that is not a key (a bundle, an unknown
// include) is a leaf. Roots and edges are walked in sorted order so a report is
// the same on every run.
func includeCycles(graph map[string][]string) [][]string {
	const (
		unseen = iota
		walking
		done
	)
	state := map[string]int{}
	var stack []string
	var cycles [][]string
	var walk func(n string)
	walk = func(n string) {
		state[n] = walking
		stack = append(stack, n)
		for _, next := range graph[n] {
			if _, isKit := graph[next]; !isKit {
				continue
			}
			switch state[next] {
			case walking:
				at := slices.Index(stack, next)
				cycles = append(cycles, append(slices.Clone(stack[at:]), next))
			case unseen:
				walk(next)
			}
		}
		stack = stack[:len(stack)-1]
		state[n] = done
	}
	for _, n := range sortedKeys(graph) {
		if state[n] == unseen {
			walk(n)
		}
	}
	return cycles
}

// lintRenders grades every render's span map and every render's lack of one.
func (t Tree) lintRenders(stems []string) ([]string, *Result) {
	docs, groups := t.matching(contextPathRe)
	spanPaths, spanGroups := t.matching(spansPathRe)
	var findings []string
	seen := map[string]bool{}
	for _, p := range spanPaths {
		key := renderKey(spanGroups[p][0], spanGroups[p][1])
		seen[key] = true
		doc := "renders/" + key + "/" + contextDocName
		if !t.has(doc) {
			findings = append(findings, p+": maps a context document that is not here ("+doc+")")
			continue
		}
		text, err := t.Read(doc)
		if err != nil {
			r := cannotRun(LintID, "%s would not read: %v", doc, err)
			return nil, &r
		}
		body, err := t.Read(p)
		if err != nil {
			r := cannotRun(LintID, "%s would not read: %v", p, err)
			return nil, &r
		}
		findings = append(findings, spanFindings(p, body, len(text), stems)...)
	}
	for _, p := range docs {
		key := renderKey(groups[p][0], groups[p][1])
		if seen[key] {
			continue
		}
		text, err := t.Read(p)
		if err != nil {
			r := cannotRun(LintID, "%s would not read: %v", p, err)
			return nil, &r
		}
		if strings.TrimSpace(text) != "" {
			findings = append(findings, p+": has text and no "+spansFileInside+" beside it, so no byte of it is attributed to a block")
		}
	}
	return findings, nil
}

// spanFindings grades one span map against the length of the document it maps.
func spanFindings(path, body string, docLen int, stems []string) []string {
	var rows []struct {
		Slug  *string `json:"slug"`
		Start *int    `json:"start_byte"`
		End   *int    `json:"end_byte"`
	}
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		return []string{fmt.Sprintf("%s: does not parse as a list of spans: %v", path, err)}
	}
	if len(rows) == 0 {
		return []string{path + ": maps no span of a document that has text"}
	}
	var out []string
	prevEnd := 0
	for i, r := range rows {
		at := fmt.Sprintf("%s: span %d", path, i+1)
		if r.Slug == nil || r.Start == nil || r.End == nil {
			out = append(out, at+" lacks slug, start_byte or end_byte")
			continue
		}
		switch {
		case *r.Start < 0 || *r.Start >= *r.End:
			out = append(out, fmt.Sprintf("%s (%s) is not a range: %d..%d", at, *r.Slug, *r.Start, *r.End))
		case *r.End > docLen:
			out = append(out, fmt.Sprintf("%s (%s) ends at byte %d, past the document's %d", at, *r.Slug, *r.End, docLen))
		case *r.Start < prevEnd:
			out = append(out, fmt.Sprintf("%s (%s) starts at byte %d, inside the span before it (ends at %d)", at, *r.Slug, *r.Start, prevEnd))
		}
		if *r.End > prevEnd {
			prevEnd = *r.End
		}
		if !slices.Contains(stems, *r.Slug) {
			out = append(out, fmt.Sprintf("%s names block %q, which is not in %s", at, *r.Slug, blocksDir))
		}
	}
	return out
}
