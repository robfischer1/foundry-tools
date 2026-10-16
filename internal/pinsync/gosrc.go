package pinsync

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// goSource is internal/checks as source: every non-test file, parsed, and its
// top-level string constants by name. The constants are evaluated from the
// source rather than compiled in, because the command runs on a tree Renovate
// has just edited and must read the values that tree holds.
type goSource struct {
	dir    string
	fset   *token.FileSet
	files  map[string][]byte    // file name -> bytes
	parsed map[string]*ast.File // file name -> AST
	consts map[string]constDecl // constant name -> its declaration
}

// constDecl is one top-level constant: the file it is in, the expression it
// is spelled as, and the comment above it.
type constDecl struct {
	file string
	expr ast.Expr
	doc  string
}

func loadGoSource(dir string) (*goSource, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	s := &goSource{dir: dir, files: map[string][]byte{}}
	for _, ent := range entries {
		name := ent.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		s.files[name] = body
	}
	return s, s.reparse()
}

// reparse rebuilds the ASTs and the constant index from the current bytes.
func (s *goSource) reparse() error {
	s.fset = token.NewFileSet()
	s.parsed = map[string]*ast.File{}
	s.consts = map[string]constDecl{}
	for name, body := range s.files {
		f, err := parser.ParseFile(s.fset, name, body, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parsing %w", err)
		}
		s.parsed[name] = f
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				doc := vs.Doc.Text()
				if len(gen.Specs) == 1 {
					// A lone `const X = …` carries its marker on the declaration.
					doc += gen.Doc.Text()
				}
				for i, id := range vs.Names {
					if i < len(vs.Values) {
						s.consts[id.Name] = constDecl{file: name, expr: vs.Values[i], doc: doc}
					}
				}
			}
		}
	}
	return nil
}

// eval answers a constant's string value: a literal, another constant, or a
// `+` of those. Anything else is not a pin this command can follow.
func (s *goSource) eval(name string) (string, error) {
	return s.evalExpr(&ast.Ident{Name: name}, 0)
}

func (s *goSource) evalExpr(e ast.Expr, depth int) (string, error) {
	if depth > 32 {
		return "", fmt.Errorf("a constant in %s refers to itself", s.dir)
	}
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", fmt.Errorf("%s is not a string", e.Value)
		}
		return strconv.Unquote(e.Value)
	case *ast.Ident:
		d, ok := s.consts[e.Name]
		if !ok {
			return "", fmt.Errorf("no constant %s in %s", e.Name, s.dir)
		}
		return s.evalExpr(d.expr, depth+1)
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			l, err := s.evalExpr(e.X, depth+1)
			if err != nil {
				return "", err
			}
			r, err := s.evalExpr(e.Y, depth+1)
			return l + r, err
		}
	}
	return "", fmt.Errorf("%T is not a string constant", e)
}

// literal answers the string literal a constant is spelled as, its value, and
// its file; a join is an error, because a follower is rewritten in place.
func (s *goSource) literal(name string) (*ast.BasicLit, string, string, error) {
	d, ok := s.consts[name]
	lit, isLit := d.expr.(*ast.BasicLit)
	if !ok || !isLit || lit.Kind != token.STRING {
		return nil, "", "", fmt.Errorf("%s is not a string literal constant in %s", name, s.dir)
	}
	value, err := strconv.Unquote(lit.Value)
	return lit, value, d.file, err
}

// markerRe reads a `// renovate:` marker's datasource and depName.
var markerRe = regexp.MustCompile(`renovate:\s*datasource=(\S+)\s+depName=(\S+)`)

// byMarker answers the constant whose renovate marker names this datasource
// and depName.
func (s *goSource) byMarker(datasource, depName string) (string, bool) {
	for name, d := range s.consts {
		for _, m := range markerRe.FindAllStringSubmatch(d.doc, -1) {
			if m[1] == datasource && m[2] == depName {
				return name, true
			}
		}
	}
	return "", false
}

// varLit answers a top-level `var name = T{…}`'s composite literal and file.
func (s *goSource) varLit(name string) (string, *ast.CompositeLit, error) {
	for file, f := range s.parsed {
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				if vs.Names[0].Name != name || len(vs.Values) == 0 {
					continue
				}
				if lit, ok := vs.Values[0].(*ast.CompositeLit); ok {
					return file, lit, nil
				}
			}
		}
	}
	return "", nil, fmt.Errorf("no var %s = T{…} in %s", name, s.dir)
}

// mapEntries answers a `var name = map[string]string{Const: "literal", …}`
// as constant name -> value literal.
func (s *goSource) mapEntries(name string) (string, map[string]*ast.BasicLit, error) {
	file, lit, err := s.varLit(name)
	if err != nil {
		return "", nil, err
	}
	out := map[string]*ast.BasicLit{}
	for _, elt := range lit.Elts {
		kv, _ := elt.(*ast.KeyValueExpr)
		if kv == nil {
			return "", nil, fmt.Errorf("%s has an element that is not key: value", name)
		}
		key, kok := kv.Key.(*ast.Ident)
		val, vok := kv.Value.(*ast.BasicLit)
		if !kok || !vok {
			return "", nil, fmt.Errorf("%s has an entry that is not Const: \"literal\"", name)
		}
		out[key.Name] = val
	}
	return file, out, nil
}

// sliceFields answers, for each struct literal in a `var name = []T{…}`, the
// named fields' expressions: PythonLaneTools' Name and Version.
func (s *goSource) sliceFields(name string, fields ...string) ([]map[string]ast.Expr, error) {
	_, lit, err := s.varLit(name)
	if err != nil {
		return nil, err
	}
	var out []map[string]ast.Expr
	for _, elt := range lit.Elts {
		row := map[string]ast.Expr{}
		if el, ok := elt.(*ast.CompositeLit); ok {
			for _, fe := range el.Elts {
				if kv, ok := fe.(*ast.KeyValueExpr); ok {
					if id, ok := kv.Key.(*ast.Ident); ok && slices.Contains(fields, id.Name) {
						row[id.Name] = kv.Value
					}
				}
			}
		}
		for _, f := range fields {
			if row[f] == nil {
				return nil, fmt.Errorf("%s has an element with no %s: field", name, f)
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// edit is one literal to replace with a new value.
type edit struct {
	file  string
	lit   *ast.BasicLit
	value string
}

// apply rewrites each literal in place, formats the files it touched and
// re-reads the constants, so a later step sees the new values. It answers the
// files it touched.
func (s *goSource) apply(edits []edit) ([]string, error) {
	byFile := map[string][]edit{}
	for _, e := range edits {
		byFile[e.file] = append(byFile[e.file], e)
	}
	var touched []string
	for file, es := range byFile {
		slices.SortFunc(es, func(a, b edit) int { return int(b.lit.Pos() - a.lit.Pos()) })
		body := slices.Clone(s.files[file])
		for _, e := range es {
			start := s.fset.Position(e.lit.Pos()).Offset
			body = slices.Concat(body[:start], []byte(strconv.Quote(e.value)), body[start+len(e.lit.Value):])
		}
		// Only a string literal moved in a file that parsed, so it formats;
		// gofmt re-aligns any trailing comment the new length pushed out.
		if formatted, err := format.Source(body); err == nil {
			body = formatted
		}
		s.files[file] = body
		touched = append(touched, file)
	}
	slices.Sort(touched)
	return touched, s.reparse()
}
