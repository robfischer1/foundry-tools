package checks

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// fleet:node-kinds-declared's judgement.
//
// THE DEFECT IS A KIND A STAR MINTS THAT CHAOS NEVER DECLARED. nodes.kind is a
// foreign key onto node_kinds, so a star that captures `nyx-contract`,
// `nyx-diagram` or `ChronicleChapter` before the vocabulary names it is refused
// at runtime (`capture_refused: kind(s) [...] not in node_kinds`) and ONLY at
// runtime: its own tests ran against a fake graph that accepted anything.
// Measured 2026-10-06: nyx#15055 and mnemosyne#15171, every dream chronicle
// refused for the length of the gap.
//
// The answer lives in chaos's DDL seed, the one copy of the vocabulary
// (bigintschema/schema.go: "a kind is added here, by a migration, or nowhere"),
// so the atom reads it from the door and compares.

// NodeKindsRepo and NodeKindsPath are where the vocabulary is declared.
const (
	NodeKindsRepo = "rob/chaos"
	NodeKindsPath = "bigintschema/schema.go"
)

var (
	kindInsertRe = regexp.MustCompile(`INSERT INTO \{schema\}\.node_kinds`)
	kindArrayRe  = regexp.MustCompile(`(?s)ARRAY\[(.*?)\]`)
	kindValuesRe = regexp.MustCompile(`(?s)VALUES\s*\(\s*'((?:[^']|'')*)'`)
	sqlQuotedRe  = regexp.MustCompile(`'((?:[^']|'')*)'`)
)

// DeclaredNodeKinds reads every kind the DDL seeds into node_kinds out of
// chaos's schema.go: the bootstrap and each later `unnest(ARRAY[...])` block,
// and the single-row `VALUES ('X', 'X', ...)` registrations. SQL comment lines
// are dropped first, so prose that names a kind declares nothing.
func DeclaredNodeKinds(schemaGo string) map[string]bool {
	lines := strings.Split(schemaGo, "\n")
	for i, line := range lines {
		lines[i] = stripSQLComment(line)
	}
	text := strings.Join(lines, "\n")
	out := map[string]bool{}
	locs := kindInsertRe.FindAllStringIndex(text, -1)
	for _, loc := range locs {
		stmt, _, _ := strings.Cut(text[loc[1]:], "ON CONFLICT")
		if m := kindArrayRe.FindStringSubmatch(stmt); m != nil {
			for _, q := range sqlQuotedRe.FindAllStringSubmatch(m[1], -1) {
				out[strings.ReplaceAll(q[1], "''", "'")] = true
			}
		} else if m := kindValuesRe.FindStringSubmatch(stmt); m != nil {
			out[strings.ReplaceAll(m[1], "''", "'")] = true
		}
	}
	return out
}

// stripSQLComment drops a `--` comment from one line, but only a `--` outside a
// quoted string: a kind may spell two dashes.
func stripSQLComment(line string) string {
	inQuote := false
	for i, c := range line {
		if c == '\'' {
			inQuote = !inQuote
		} else if !inQuote && strings.HasPrefix(line[i:], "--") {
			return line[:i]
		}
	}
	return line
}

// KindUse is one place a Go source captures a node of a kind.
type KindUse struct {
	Kind string
	File string
	Line int
}

// capturedKindSkip reports a path whose captures are not the star's own: tests,
// vendored code, fixtures, and fakes (a fake graph's createNode handler carries
// no kind of its own).
func capturedKindSkip(p string) bool {
	if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
		return true
	}
	for _, seg := range strings.Split(p, "/") {
		l := strings.ToLower(seg)
		if seg == "vendor" || strings.Contains(l, "fake") || testSegment(l) {
			return true
		}
	}
	return false
}

// notTestDirs end in "test" and are not test packages.
var notTestDirs = map[string]bool{"latest": true, "contest": true, "attest": true, "protest": true}

// testSegment reports a directory named for tests: test, testdata, testsupport,
// and the fleet's boardtest / pgtest / orbitstest family.
func testSegment(l string) bool {
	return strings.HasPrefix(l, "test") || (strings.HasSuffix(l, "test") && !notTestDirs[l])
}

// constSet is the string constants of a tree, by name, each with the directory
// that declares it.
type constSet map[string][]constDef

type constDef struct{ dir, value string }

func (c constSet) add(dir, name, value string) {
	c[name] = append(c[name], constDef{dir, value})
}

// resolve answers the values a name can hold. A package-qualified reference
// (`vocabulary.Kind`) prefers the constants declared in a directory of that
// name, because `Kind` alone is declared in several packages; with no such
// directory, or no qualifier, it is the union.
func (c constSet) resolve(qualifier, name string) []string {
	defs := c[name]
	if qualifier != "" {
		var narrowed []constDef
		for _, d := range defs {
			if path.Base(d.dir) == qualifier {
				narrowed = append(narrowed, d)
			}
		}
		if len(narrowed) > 0 {
			defs = narrowed
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, d := range defs {
		if !seen[d.value] {
			seen[d.value] = true
			out = append(out, d.value)
		}
	}
	return out
}

// stringLit is the value of a string literal expression, or ok false.
func stringLit(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	return s, err == nil
}

// constValue is a const's value when it is a string literal or a conversion of
// one (`Kind("x")`).
func constValue(e ast.Expr) (string, bool) {
	if s, ok := stringLit(e); ok {
		return s, true
	}
	if call, ok := e.(*ast.CallExpr); ok && len(call.Args) == 1 {
		return stringLit(call.Args[0])
	}
	return "", false
}

// paramIndex maps a function's parameter names to their position.
func paramIndex(fd *ast.FuncDecl) map[string]int {
	params := map[string]int{}
	i := 0
	for _, fld := range fd.Type.Params.List {
		for _, nm := range fld.Names {
			params[nm.Name] = i
			i++
		}
	}
	return params
}

// typeName is the bare name of a composite literal's type (`ops.CreateNode`
// answers "CreateNode"), or "".
func typeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	}
	return ""
}

// calleeName is the bare name of the function a call invokes, or "".
func calleeName(c *ast.CallExpr) string {
	switch fn := c.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

// createKindExpr is the kind expression of a node that captures one: a
// CreateNode(kind, ...) call, or a createNode composite literal's kind field.
// Anything else answers nil.
func createKindExpr(n ast.Node, resolve func(ast.Expr) ([]string, bool)) ast.Expr {
	switch x := n.(type) {
	case *ast.CallExpr:
		if calleeName(x) == "CreateNode" && len(x.Args) > 0 {
			return x.Args[0]
		}
	case *ast.CompositeLit:
		var kind ast.Expr
		creates := typeName(x.Type) == "CreateNode"
		for _, el := range x.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key := strings.ToLower(keyName(kv.Key))
			vals, _ := resolve(kv.Value)
			isCreate := false
			for _, v := range vals {
				if v == "createNode" {
					isCreate = true
				}
			}
			switch {
			case isCreate:
				creates = true
			case key == "kind" || key == "node_type" || key == "nodetype" || key == "createkind":
				kind = kv.Value
			}
		}
		if creates {
			return kind
		}
	}
	return nil
}

// keyName is the name a composite-literal key spells: a string literal
// ("kind") or an identifier (Kind).
func keyName(e ast.Expr) string {
	if s, ok := stringLit(e); ok {
		return s
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// CapturedKinds finds the node kinds the Go files in files (path -> source)
// capture, and how many capture sites named a kind it could not resolve.
//
// A SITE IS ONE OF TWO SHAPES, and both are the fleet's own:
//
//   - a call to a function named CreateNode, whose first argument is the kind
//     (mnemosyne and nyx's op builders);
//   - a composite literal that is a createNode, by its type (`ops.CreateNode{}`)
//     or by a field that says so (op = "createNode", or Kind: OpCreate), beside
//     a kind field: the wire shape tartarus, urania, cql, techne and the rest
//     write by hand. `node_type` and `CreateKind` are the same field under
//     other spellings.
//
// A kind that is a variable or a parameter is UNRESOLVED, not a finding: the
// kind a helper forwards is declared at its call sites, which are sites too.
// Unresolved is counted so a report can say how much it did not see.
func CapturedKinds(files map[string]string) (uses []KindUse, unresolved int) {
	fset := token.NewFileSet()
	type parsed struct {
		name string
		f    *ast.File
	}
	var all []parsed
	names := make([]string, 0, len(files))
	for p := range files {
		if !capturedKindSkip(p) {
			names = append(names, p)
		}
	}
	sort.Strings(names)
	consts := constSet{}
	for _, p := range names {
		f, err := parser.ParseFile(fset, p, files[p], parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		all = append(all, parsed{p, f})
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, id := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					if v, ok := constValue(vs.Values[i]); ok {
						consts.add(path.Dir(p), id.Name, v)
					}
				}
			}
		}
	}

	resolve := func(e ast.Expr) ([]string, bool) {
		if v, ok := constValue(e); ok {
			return []string{v}, true
		}
		var vals []string
		switch x := e.(type) {
		case *ast.Ident:
			vals = consts.resolve("", x.Name)
		case *ast.SelectorExpr:
			q := ""
			if id, ok := x.X.(*ast.Ident); ok {
				q = id.Name
			}
			vals = consts.resolve(q, x.Sel.Name)
		}
		return vals, len(vals) > 0
	}

	// A HELPER FORWARDS ITS KIND. `func create(kind, label string) Op` names no
	// kind of its own: its callers do, and they are the capture sites. So a kind
	// that is a parameter registers the helper (name, parameter index), and every
	// call to it anywhere in the tree is read as the site.
	helpers := map[string]int{}
	for _, pf := range all {
		for _, d := range pf.f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			params := paramIndex(fd)
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if id, ok := createKindExpr(n, resolve).(*ast.Ident); ok {
					if idx, isParam := params[id.Name]; isParam {
						helpers[fd.Name.Name] = idx
					}
				}
				return true
			})
		}
	}

	for _, pf := range all {
		note := func(e ast.Expr) {
			vals, ok := resolve(e)
			if !ok {
				unresolved++
				return
			}
			line := fset.Position(e.Pos()).Line
			for _, v := range vals {
				uses = append(uses, KindUse{Kind: v, File: pf.name, Line: line})
			}
		}
		for _, d := range pf.f.Decls {
			var params map[string]int
			if fd, ok := d.(*ast.FuncDecl); ok {
				params = paramIndex(fd)
			}
			ast.Inspect(d, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if idx, isHelper := helpers[calleeName(call)]; isHelper && idx < len(call.Args) {
						note(call.Args[idx])
					}
				}
				e := createKindExpr(n, resolve)
				if e == nil {
					return true
				}
				if id, ok := e.(*ast.Ident); ok {
					if _, forwarded := params[id.Name]; forwarded {
						return true // a forwarded kind: its callers are the sites
					}
				}
				note(e)
				return true
			})
		}
	}
	return uses, unresolved
}

// nonGoCaptureRe is a capture spelled in a language the AST reader does not
// parse: chaos's own python client, calliope and theia in TypeScript, anvil in
// Rust (measured over the fleet's checkouts, foundry-tools#15344).
var nonGoCaptureRe = regexp.MustCompile(`(?i)\b(?:graph_capture|create_?node)\b|\bop["']?\s*[:=]\s*["']createNode["']`)

// NonGoCaptureFiles answers the non-Go source files that look like they capture
// a node: the kinds a green verdict did not read. The atom is Go-only, and a
// pass that does not say so reads as "no undeclared kind in this tree".
func NonGoCaptureFiles(files map[string]string) []string {
	var out []string
	for p, body := range files {
		switch path.Ext(p) {
		case ".py", ".ts", ".tsx", ".rs":
		default:
			continue
		}
		skip := strings.HasSuffix(p, ".d.ts") || strings.Contains(p, ".test.") || strings.Contains(p, ".spec.") || strings.HasPrefix(path.Base(p), "test_")
		for _, seg := range strings.Split(p, "/") {
			l := strings.ToLower(seg)
			if seg == "vendor" || seg == "node_modules" || seg == "generated" || strings.Contains(l, "fake") || testSegment(l) || l == "tests" {
				skip = true
			}
		}
		if !skip && nonGoCaptureRe.MatchString(body) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// NonGoNote is the sentence that keeps a pass honest: empty when there is nothing
// unread to name.
func NonGoNote(files []string) string {
	if len(files) == 0 {
		return ""
	}
	return fmt.Sprintf("; NOT JUDGED: this check reads Go only, and %d non-Go file(s) look like captures (%s)", len(files), strings.Join(firstN(files, 3), ", "))
}

// NodeKindsDeclared judges one tree's captures against the vocabulary.
//
// schemaGo is chaos's schema.go; files is the tree's Go source. A tree that
// captures nothing passes without the door being asked (the caller does not
// fetch the vocabulary for it). A vocabulary that parsed to nothing is a
// CANNOT RUN: an empty list would condemn every kind there is.
func NodeKindsDeclared(files map[string]string, schemaGo string) (int, string) {
	const id = "fleet:node-kinds-declared"
	uses, unresolved := CapturedKinds(files)
	if len(uses) == 0 {
		return 0, id + ": no node kind is captured in this tree"
	}
	declared := DeclaredNodeKinds(schemaGo)
	if len(declared) == 0 {
		return 2, id + ": CANNOT RUN - " + NodeKindsPath + " in " + NodeKindsRepo + " parsed to no declared kinds, so nothing could be compared. An empty vocabulary is not a finding about this tree."
	}
	bad := map[string][]string{}
	kinds := map[string]bool{}
	for _, u := range uses {
		kinds[u.Kind] = true
		if !declared[u.Kind] {
			bad[u.Kind] = append(bad[u.Kind], fmt.Sprintf("%s:%d", u.File, u.Line))
		}
	}
	if len(bad) == 0 {
		return 0, fmt.Sprintf("%s: %d captured kind(s) are all declared in node_kinds (%d capture site(s), %d unresolved)",
			id, len(kinds), len(uses), unresolved)
	}
	names := make([]string, 0, len(bad))
	for k := range bad {
		names = append(names, k)
	}
	sort.Strings(names)
	var lines []string
	for _, k := range names {
		lines = append(lines, fmt.Sprintf("node-kinds: '%s' is captured (%s) but is not in node_kinds", k, strings.Join(firstN(bad[k], 3), ", ")))
	}
	lines = append(lines,
		"nodes.kind is a foreign key onto node_kinds, so chaos refuses this capture at runtime ('capture_refused: kind(s) [...] not in node_kinds') and a fake graph in the tests never will. "+
			"Declare the kind in "+NodeKindsRepo+" "+NodeKindsPath+" (a bigintschema migration) BEFORE this star mints it.")
	return 1, strings.Join(lines, "\n")
}

// FetchNodeKindsSchema reads chaos's schema.go through the door, as an error
// that names the CANNOT RUN when the door could not answer.
func FetchNodeKindsSchema(ctx context.Context, door Door) (string, error) {
	status, body, err := door.Get(ctx, NodeKindsRepo, NodeKindsPath)
	switch {
	case err != nil:
		return "", fmt.Errorf("the door is unreachable (%s): %w", door.URL(NodeKindsRepo, NodeKindsPath), err)
	case status != http.StatusOK:
		return "", fmt.Errorf("the door answered HTTP %d for %s", status, door.URL(NodeKindsRepo, NodeKindsPath))
	}
	return string(body), nil
}
