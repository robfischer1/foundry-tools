package checks

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/grafana/alloy/syntax/ast"
	"github.com/grafana/alloy/syntax/parser"
	"github.com/grafana/alloy/syntax/token"
	"go.yaml.in/yaml/v3"
)

// THE ALLOY PASS. flux 1243cf43 passed the gate green and crashlooped
// hemera-alloy-node on llm01: config.alloy:480 gave a stage.match selector a
// backtick raw string where Alloy expects a quoted STRING, and nothing between
// the commit and the pod parsed the file. AlloyConfigs gathers every Alloy
// source the tree carries and AlloyReport parses each with Alloy's own syntax
// parser (github.com/grafana/alloy/syntax, the package `alloy fmt` runs), in
// process: no binary to pin, no network. It is a SYNTAX check only. A component
// or argument that does not exist, or the LogQL inside a selector, is Alloy's
// to refuse at load and is out of scope here.
//
// THE SYNTAX PARSER ALONE WOULD NOT HAVE CAUGHT 1243cf43. Alloy's grammar
// accepts the backtick as a raw string, and the selector there was a quoted
// Alloy string with a LogQL backtick string INSIDE it; the refusal ("parse
// error at line 1, col 22: unexpected IDENTIFIER, expecting STRING", flux
// fee514d7) came from loki.process's own selector parser at component load.
// So the one component-level rule this pass carries is the cheap, observed one:
// a stage.match selector whose value holds a backtick.
//
// Two sources: a ConfigMap data value whose key ends ".alloy" in the built
// trees (the node agent's config.alloy, and the modules a configMapGenerator
// lays), and every tracked *.alloy file (the .on/.off variants the generator
// does not build are still files an operator flips to).

// AlloyKey is the suffix that marks a ConfigMap data key, and a tracked file,
// as Alloy source.
const AlloyKey = ".alloy"

// AlloyConfig is one Alloy source: where it came from and its text.
type AlloyConfig struct{ Name, Src string }

// AlloyConfigs answers the Alloy sources of the built streams (a ConfigMap's
// data values whose key ends AlloyKey, named "<kind> <metadata.name>:<key>")
// and of the tracked files (named by path), sorted by name. A stream that does
// not parse is an error: the tree could not be read, not a tree without Alloy.
func AlloyConfigs(streams []string, files map[string]string) ([]AlloyConfig, error) {
	var out []AlloyConfig
	for _, stream := range streams {
		dec := yaml.NewDecoder(strings.NewReader(stream))
		for {
			var doc struct {
				Kind     string `yaml:"kind"`
				Metadata struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
				Data map[string]any `yaml:"data"`
			}
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("a built tree did not parse: %w", err)
			}
			if doc.Kind != "ConfigMap" {
				continue
			}
			for k, v := range doc.Data {
				if src, ok := v.(string); ok && strings.HasSuffix(k, AlloyKey) {
					out = append(out, AlloyConfig{Name: "ConfigMap " + doc.Metadata.Name + ":" + k, Src: src})
				}
			}
		}
	}
	for name, src := range files {
		if strings.HasSuffix(name, AlloyKey) {
			out = append(out, AlloyConfig{Name: name, Src: src})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// AlloyReport parses each config and answers the report and whether any did
// not parse. Each failure is named with the parser's own line:column message.
func AlloyReport(cfgs []AlloyConfig) (string, bool) {
	var b strings.Builder
	bad := 0
	for _, c := range cfgs {
		file, err := parser.ParseFile(c.Name, []byte(c.Src))
		if err != nil {
			bad++
			fmt.Fprintf(&b, "  ALLOY PARSE %s: %v\n", c.Name, err)
			continue
		}
		for _, pos := range alloyMatchBackticks(file.Body) {
			bad++
			fmt.Fprintf(&b, "  ALLOY SELECTOR %s:%d: a stage.match selector holds a backtick; loki.process refuses it at load (flux fee514d7); write the filter as a quoted string\n", c.Name, pos.Line)
		}
	}
	fmt.Fprintf(&b, "Alloy pass: %d config(s) parsed, %d failed\n", len(cfgs), bad)
	return b.String(), bad > 0
}

// alloyMatchBackticks answers the position of every stage.match selector, at
// any depth of the body, that is a literal string holding a backtick once its
// Alloy quoting is undone. A selector that is not a literal is not read.
func alloyMatchBackticks(body ast.Body) []token.Position {
	var out []token.Position
	for _, stmt := range body {
		blk, ok := stmt.(*ast.BlockStmt)
		if !ok {
			continue
		}
		out = append(out, alloyMatchBackticks(blk.Body)...)
		if strings.Join(blk.Name, ".") != "stage.match" {
			continue
		}
		for _, inner := range blk.Body {
			attr, ok := inner.(*ast.AttributeStmt)
			if !ok || attr.Name.Name != "selector" {
				continue
			}
			lit, ok := attr.Value.(*ast.LiteralExpr)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			if val, err := strconv.Unquote(lit.Value); err == nil && strings.Contains(val, "`") {
				out = append(out, lit.ValuePos.Position())
			}
		}
	}
	return out
}
