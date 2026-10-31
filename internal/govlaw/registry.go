// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package govlaw

import (
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	diesFile      = "dies.toml"
	consumersFile = "consumers.toml"
	matrixFile    = "matrix.toml"
	// ownRepo is the repository name of foundry-stocks in a die's source URL: a die
	// sourced from it is packaged from THIS tree, so its path can be checked here.
	ownRepo = "foundry-stocks"
)

// matrixCells is the vocabulary of a capability cell (matrix.toml's header).
var matrixCells = []string{"native", "shim", "degrade", "drop+warn"}

var (
	dieKeys      = []string{"kit", "source", "path", "exclude", "wheel"}
	consumerKeys = []string{"path", "kit", "provider"}
)

// registryResult is what lintRegistry answers: findings, and one line saying what
// it did not resolve, so a pass never claims more than was looked at.
type registryResult struct {
	findings   []string
	unresolved int // source-form dies whose repository is not this tree
	dies       int
	consumers  int
	cells      int
}

// lintRegistry grades the three registry files beside kits.toml: dies.toml,
// consumers.toml and matrix.toml.
//
// A FILE THAT IS NOT THERE is a could-not-run, as a missing block library is: the
// lane cannot tell a retired registry from a moved one. A file that is there and
// does not parse is a FINDING, not a 2: the lane read it and it is wrong.
//
// MATRIX. Every table is a capability; its keys are providers and its cells are
// one of native | shim | degrade | drop+warn. The providers are the columns the
// matrix itself declares, and a consumer's provider must be one of them.
//
// CONSUMERS. Every entry has a path; `kit`, when present, names a kit in kits.toml;
// `provider`, when present, is a matrix column. An entry with no kit renders
// nothing (consumers.toml's header) and is allowed. Unknown keys are findings.
//
// DIES. An entry is either a kit die (`kit` alone, naming a kit) or a tree die
// (`source` = "<url>@<ref>" with a relative `path`, and optionally `exclude`, a
// list of strings, and `wheel`, a bool). When the source is foundry-stocks itself
// the path must exist in this tree. A source in another repository is NOT resolved:
// that is a network fetch, and a gate that fetched would owe a 2 for every outage.
// They are counted and the pass says so.
func (t Tree) lintRegistry(kits map[string]struct{}) (registryResult, *Result) {
	var r registryResult
	read := func(file string, into any) (ok bool, res *Result) {
		if !t.has(file) {
			c := cannotRun(LintID, "%s is not in the tree: this lane grades the registry and does not know where it moved", file)
			return false, &c
		}
		body, err := t.Read(file)
		if err != nil {
			c := cannotRun(LintID, "%s would not read: %v", file, err)
			return false, &c
		}
		if _, err := toml.Decode(body, into); err != nil {
			r.findings = append(r.findings, fmt.Sprintf("%s: does not parse: %v", file, err))
			return false, nil
		}
		return true, nil
	}

	var matrix map[string]map[string]any
	okMatrix, res := read(matrixFile, &matrix)
	if res != nil {
		return r, res
	}
	providers := map[string]struct{}{}
	if okMatrix {
		for _, capab := range sortedKeys(matrix) {
			for _, prov := range sortedKeys(matrix[capab]) {
				providers[prov] = struct{}{}
				r.cells++
				cell, isStr := matrix[capab][prov].(string)
				if !isStr || !slices.Contains(matrixCells, cell) {
					r.findings = append(r.findings, fmt.Sprintf("%s: [%s] %s = %v is not one of %s", matrixFile, capab, prov, matrix[capab][prov], strings.Join(matrixCells, " | ")))
				}
			}
		}
		if len(matrix) == 0 {
			r.findings = append(r.findings, matrixFile+": declares no capability")
		}
	}

	var consumers struct {
		Consumers map[string]map[string]any `toml:"consumers"`
	}
	okConsumers, res := read(consumersFile, &consumers)
	if res != nil {
		return r, res
	}
	if okConsumers {
		r.consumers = len(consumers.Consumers)
		if r.consumers == 0 {
			r.findings = append(r.findings, consumersFile+": declares no consumer")
		}
		for _, name := range sortedKeys(consumers.Consumers) {
			c := consumers.Consumers[name]
			at := fmt.Sprintf("%s: consumer %q", consumersFile, name)
			r.findings = append(r.findings, unknownKeys(at, c, consumerKeys)...)
			if p, ok := c["path"].(string); !ok || strings.TrimSpace(p) == "" {
				r.findings = append(r.findings, at+" has no path")
			}
			if v, has := c["kit"]; has {
				if k, ok := v.(string); !ok {
					r.findings = append(r.findings, at+" has a kit that is not a string")
				} else if _, known := kits[k]; !known {
					r.findings = append(r.findings, fmt.Sprintf("%s names kit %q, which is not in %s", at, k, kitsFile))
				}
			}
			if v, has := c["provider"]; has && okMatrix {
				if p, ok := v.(string); !ok {
					r.findings = append(r.findings, at+" has a provider that is not a string")
				} else if _, known := providers[p]; !known {
					r.findings = append(r.findings, fmt.Sprintf("%s names provider %q, which is not a column of %s", at, p, matrixFile))
				}
			}
		}
	}

	var dies struct {
		Dies map[string]map[string]any `toml:"dies"`
	}
	okDies, res := read(diesFile, &dies)
	if res != nil {
		return r, res
	}
	if okDies {
		r.dies = len(dies.Dies)
		if r.dies == 0 {
			r.findings = append(r.findings, diesFile+": declares no die")
		}
		for _, name := range sortedKeys(dies.Dies) {
			f, unresolved := t.lintDie(name, dies.Dies[name], kits)
			r.findings = append(r.findings, f...)
			if unresolved {
				r.unresolved++
			}
		}
	}
	return r, nil
}

// lintDie grades one dies.toml entry; unresolved is true for a tree die sourced
// from a repository other than this one.
func (t Tree) lintDie(name string, d map[string]any, kits map[string]struct{}) (findings []string, unresolved bool) {
	at := fmt.Sprintf("%s: die %q", diesFile, name)
	findings = unknownKeys(at, d, dieKeys)
	kitV, hasKit := d["kit"]
	srcV, hasSource := d["source"]
	switch {
	case hasKit && hasSource:
		return append(findings, at+" names both a kit and a source: it is one or the other"), false
	case !hasKit && !hasSource:
		return append(findings, at+" names neither a kit nor a source"), false
	case hasKit:
		for _, k := range []string{"path", "exclude", "wheel"} {
			if _, has := d[k]; has {
				findings = append(findings, fmt.Sprintf("%s is a kit die and carries %q, which only a source die reads", at, k))
			}
		}
		k, ok := kitV.(string)
		if !ok {
			return append(findings, at+" has a kit that is not a string"), false
		}
		if _, known := kits[k]; !known {
			findings = append(findings, fmt.Sprintf("%s names kit %q, which is not in %s", at, k, kitsFile))
		}
		return findings, false
	}

	src, ok := srcV.(string)
	repo := ""
	if !ok {
		findings = append(findings, at+" has a source that is not a string")
	} else if at2 := strings.LastIndex(src, "@"); at2 <= 0 || at2 == len(src)-1 {
		findings = append(findings, fmt.Sprintf("%s: source %q is not <url>@<ref>", at, src))
	} else if u, err := url.Parse(src[:at2]); err != nil || u.Scheme == "" || u.Host == "" {
		findings = append(findings, fmt.Sprintf("%s: source %q has no absolute URL before the @", at, src))
	} else {
		repo = strings.TrimSuffix(path.Base(u.Path), ".git")
	}
	if v, has := d["exclude"]; has {
		list, isList := v.([]any)
		for _, e := range list {
			if _, isStr := e.(string); !isStr {
				isList = false
			}
		}
		if !isList {
			findings = append(findings, at+" has an exclude that is not a list of strings")
		}
	}
	if v, has := d["wheel"]; has {
		if _, isBool := v.(bool); !isBool {
			findings = append(findings, at+" has a wheel that is not a bool")
		}
	}
	p, ok := d["path"].(string)
	switch {
	case !ok || strings.TrimSpace(p) == "":
		return append(findings, at+" has no path"), false
	case strings.HasPrefix(p, "/") || slices.Contains(strings.Split(p, "/"), ".."):
		return append(findings, fmt.Sprintf("%s: path %q is not relative to its source's root", at, p)), false
	case repo != ownRepo:
		return findings, repo != ""
	}
	if clean := path.Clean(p); clean != "." && !t.hasTree(clean) {
		findings = append(findings, fmt.Sprintf("%s: path %q is not in this tree, and the source is %s", at, p, ownRepo))
	}
	return findings, false
}

// hasTree reports whether a tracked file is at p or under it.
func (t Tree) hasTree(p string) bool {
	for _, q := range t.Paths {
		if q == p || strings.HasPrefix(q, p+"/") {
			return true
		}
	}
	return false
}

// unknownKeys names each key of m not in allowed.
func unknownKeys(at string, m map[string]any, allowed []string) []string {
	var out []string
	for _, k := range sortedKeys(m) {
		if !slices.Contains(allowed, k) {
			out = append(out, fmt.Sprintf("%s has the key %q; the keys are %s", at, k, strings.Join(allowed, ", ")))
		}
	}
	return out
}
