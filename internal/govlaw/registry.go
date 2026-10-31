// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package govlaw

import (
	"errors"
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
	unresolved int // source-form dies whose repository is not this tree and was not looked up
	resolved   int // source-form dies in another repository whose ref and path were found
	unreached  []string
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
// the path must exist in this tree. A source in another repository is looked up
// through Tree.Resolve when the lane was given one: a ref the repository does not
// hold, or a path it holds nothing under at that ref, is a finding. A lookup that
// failed (the door down) is a could-not-run, never a pass and never a finding.
// A source Resolve cannot read (another host), or any source when no Resolve was
// given, is counted and the pass says so.
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
			f, how := t.lintDie(name, dies.Dies[name], kits)
			r.findings = append(r.findings, f...)
			switch how.kind {
			case foreign:
				r.unresolved++
			case looked:
				r.resolved++
			case unreached:
				r.unreached = append(r.unreached, how.why)
			}
		}
	}
	return r, nil
}

// lookup is how lintDie settled a tree die sourced from another repository.
type lookup struct {
	kind int
	why  string
}

// What lintDie did with a die: nothing to look up (own tree, a kit die, or a
// die already wrong); counted and not looked up (foreign); its ref and path
// found (looked); the lookup could not be made (unreached, with why).
const (
	none = iota
	foreign
	looked
	unreached
)

// lintDie grades one dies.toml entry; the lookup says what became of a tree die
// sourced from a repository other than this one.
func (t Tree) lintDie(name string, d map[string]any, kits map[string]struct{}) (findings []string, how lookup) {
	at := fmt.Sprintf("%s: die %q", diesFile, name)
	findings = unknownKeys(at, d, dieKeys)
	kitV, hasKit := d["kit"]
	srcV, hasSource := d["source"]
	switch {
	case hasKit && hasSource:
		return append(findings, at+" names both a kit and a source: it is one or the other"), lookup{}
	case !hasKit && !hasSource:
		return append(findings, at+" names neither a kit nor a source"), lookup{}
	case hasKit:
		for _, k := range []string{"path", "exclude", "wheel"} {
			if _, has := d[k]; has {
				findings = append(findings, fmt.Sprintf("%s is a kit die and carries %q, which only a source die reads", at, k))
			}
		}
		k, ok := kitV.(string)
		if !ok {
			return append(findings, at+" has a kit that is not a string"), lookup{}
		}
		if _, known := kits[k]; !known {
			findings = append(findings, fmt.Sprintf("%s names kit %q, which is not in %s", at, k, kitsFile))
		}
		return findings, lookup{}
	}

	src, ok := srcV.(string)
	repo, srcURL, ref := "", "", ""
	if !ok {
		findings = append(findings, at+" has a source that is not a string")
	} else if at2 := strings.LastIndex(src, "@"); at2 <= 0 || at2 == len(src)-1 {
		findings = append(findings, fmt.Sprintf("%s: source %q is not <url>@<ref>", at, src))
	} else if u, err := url.Parse(src[:at2]); err != nil || u.Scheme == "" || u.Host == "" {
		findings = append(findings, fmt.Sprintf("%s: source %q has no absolute URL before the @", at, src))
	} else {
		repo, srcURL, ref = strings.TrimSuffix(path.Base(u.Path), ".git"), src[:at2], src[at2+1:]
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
		return append(findings, at+" has no path"), lookup{}
	case strings.HasPrefix(p, "/") || slices.Contains(strings.Split(p, "/"), ".."):
		return append(findings, fmt.Sprintf("%s: path %q is not relative to its source's root", at, p)), lookup{}
	case repo == "":
		return findings, lookup{}
	case repo != ownRepo:
		return t.lookUp(at, findings, srcURL, ref, p)
	}
	if clean := path.Clean(p); clean != "." && !t.hasTree(clean) {
		findings = append(findings, fmt.Sprintf("%s: path %q is not in this tree, and the source is %s", at, p, ownRepo))
	}
	return findings, lookup{}
}

// lookUp asks the source's own repository whether the die's ref exists and
// holds the path. A repository, ref or path that is not there is a finding. A
// lookup that could not be made is never one and never a pass: it is unreached,
// and the lane settles could-not-run over it.
func (t Tree) lookUp(at string, findings []string, srcURL, ref, p string) ([]string, lookup) {
	if t.Resolve == nil {
		return findings, lookup{kind: foreign}
	}
	got, err := t.Resolve(srcURL, ref, p)
	switch {
	case errors.Is(err, ErrForeign):
		return findings, lookup{kind: foreign}
	case err != nil:
		return findings, lookup{kind: unreached, why: fmt.Sprintf("%s: %s@%s could not be read: %v", at, srcURL, ref, err)}
	case got == NoRef:
		return append(findings, fmt.Sprintf("%s: ref %q does not exist in %s", at, ref, srcURL)), lookup{}
	case got == NoPath:
		return append(findings, fmt.Sprintf("%s: path %q is not in %s at %q", at, p, srcURL, ref)), lookup{}
	}
	return findings, lookup{kind: looked}
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
