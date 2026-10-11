package checks

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
)

// The Go half of fleet:kafka-flows: franz-go's kgo options and records, and the
// stellar-core-go helpers that produce on a star's behalf. This file finds
// the sites; kafkaflows_goresolve.go says what their names evaluate to.

const (
	kgoPath        = "github.com/twmb/franz-go/pkg/kgo"
	kafkatopicsPkg = "/kafkatopics"
	heartbeatPkg   = "stellar-core-go/heartbeat"
	requestlogPkg  = "stellar-core-go/requestlog"
	coreModule     = "stellar-core-go"
)

// goFile is one parsed source file.
type goFile struct {
	path    string
	dir     string
	imports map[string]string // local name -> import path
}

// funcCtx is one function body: a declaration or a literal. Locals are every
// assignment to a name inside it.
type funcCtx struct {
	decl   *ast.FuncDecl
	lit    *ast.FuncLit
	outer  *funcCtx
	file   *goFile
	locals map[string][]ast.Expr
	// groups and regex are kept on the outermost function: kgo options are
	// built in one function, often through an append in a closure.
	groups []site
	regex  bool
	// bound holds a call's own arguments when a function is evaluated for
	// that call (inline), so its parameters answer that call alone.
	bound map[string]site
}

// root is the outermost function a context sits in.
func (fc *funcCtx) root() *funcCtx {
	for fc != nil && fc.outer != nil {
		fc = fc.outer
	}
	return fc
}

// site is an expression and where it sits.
type site struct {
	expr ast.Expr
	file *goFile
	fc   *funcCtx
	// owner is the struct type a field write names, when it names one (a
	// composite literal's type); "" for an assignment or an elided type.
	owner string
}

// flowSite is a call or literal that states a flow, before resolution.
type flowSite struct {
	site
	direction  string
	family     bool
	partitions bool
	pos        token.Pos
}

// keyed is a composite literal's keyed values, for pairing two fields of one
// struct (a lane's Topic with its Group).
type keyed struct {
	vals map[string]ast.Expr
	file *goFile
	fc   *funcCtx
}

// goIndex is everything resolution reads, built once over the tree.
type goIndex struct {
	fset    *token.FileSet
	modules map[string]string // module path -> its directory
	pkg     map[string]map[string][]site
	funcs   map[string][]*funcCtx
	calls   map[string][]site   // callee name -> call expression sites
	fields  map[string][]site   // field name -> value sites
	aliases map[string][]string // function name -> the names it is stored under
	lits    map[*ast.FuncLit][]string
	keyeds  []keyed
	known   KnownTopics
	flows   []flowSite
}

// goFlows parses the tree's Go and extracts its flows. bad names the files
// that would not parse.
func goFlows(files map[string]string, known KnownTopics) (flows []KafkaFlow, bad []string) {
	ix := &goIndex{
		fset: token.NewFileSet(), modules: map[string]string{}, pkg: map[string]map[string][]site{},
		funcs: map[string][]*funcCtx{}, calls: map[string][]site{}, fields: map[string][]site{},
		aliases: map[string][]string{}, lits: map[*ast.FuncLit][]string{}, known: known,
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if path.Base(p) == "go.mod" {
			if mod := goModulePath(files[p]); mod != "" {
				ix.modules[mod] = path.Dir(p)
			}
			continue
		}
		if !strings.HasSuffix(p, ".go") {
			continue
		}
		f, err := parser.ParseFile(ix.fset, p, files[p], parser.SkipObjectResolution)
		if err != nil {
			bad = append(bad, p)
			continue
		}
		ix.index(&goFile{path: p, dir: path.Dir(p), imports: importsOf(f)}, f)
	}
	for _, fs := range ix.flows {
		flows = append(flows, ix.flowsAt(fs)...)
	}
	return flows, bad
}

// goModulePath is a go.mod's module path.
func goModulePath(mod string) string {
	for _, line := range strings.Split(mod, "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "module" {
			return strings.Trim(f[1], `"`)
		}
	}
	return ""
}

// importsOf maps each import's local name to its path.
func importsOf(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, im := range f.Imports {
		// A path the parser accepted is a valid Go string literal, so it
		// always unquotes.
		p, _ := strconv.Unquote(im.Path.Value)
		name := path.Base(p)
		if im.Name != nil {
			name = im.Name.Name
		}
		out[name] = p
	}
	return out
}

// index records the file's package-level values, then walks its bodies.
func (ix *goIndex) index(gf *goFile, f *ast.File) {
	if ix.pkg[gf.dir] == nil {
		ix.pkg[gf.dir] = map[string][]site{}
	}
	w := &walker{ix: ix, file: gf, skip: map[ast.Node]bool{}}
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || (g.Tok != token.CONST && g.Tok != token.VAR) {
			continue
		}
		for _, s := range g.Specs {
			vs := s.(*ast.ValueSpec)
			if len(vs.Names) != len(vs.Values) {
				continue
			}
			for i, n := range vs.Names {
				ix.pkg[gf.dir][n.Name] = append(ix.pkg[gf.dir][n.Name], site{expr: vs.Values[i], file: gf})
				w.named(n.Name, vs.Values[i])
			}
		}
	}
	ast.Walk(w, f)
}

// walker visits one file, carrying the function it is inside.
type walker struct {
	ix   *goIndex
	file *goFile
	fc   *funcCtx
	skip map[ast.Node]bool
}

// Visit records what resolution needs from each node.
func (w *walker) Visit(n ast.Node) ast.Visitor {
	switch x := n.(type) {
	case *ast.FuncDecl:
		fc := &funcCtx{decl: x, file: w.file, locals: map[string][]ast.Expr{}}
		w.ix.funcs[x.Name.Name] = append(w.ix.funcs[x.Name.Name], fc)
		return &walker{ix: w.ix, file: w.file, fc: fc, skip: w.skip}
	case *ast.FuncLit:
		fc := &funcCtx{lit: x, outer: w.fc, file: w.file, locals: map[string][]ast.Expr{}}
		return &walker{ix: w.ix, file: w.file, fc: fc, skip: w.skip}
	case *ast.AssignStmt:
		w.assign(x)
	case *ast.DeclStmt:
		w.declare(x)
	case *ast.CompositeLit:
		w.composite(x)
	case *ast.CallExpr:
		w.call(x)
	}
	return w
}

// named records a name a value is stored under: a function stored in a field
// or variable is called by that name, and so is a function literal.
func (w *walker) named(name string, v ast.Expr) {
	switch t := v.(type) {
	case *ast.FuncLit:
		w.ix.lits[t] = append(w.ix.lits[t], name)
	case *ast.Ident:
		w.ix.aliases[t.Name] = append(w.ix.aliases[t.Name], name)
	case *ast.SelectorExpr:
		w.ix.aliases[t.Sel.Name] = append(w.ix.aliases[t.Sel.Name], name)
	}
}

// assign records a local assignment, or a field write.
func (w *walker) assign(a *ast.AssignStmt) {
	if len(a.Lhs) != len(a.Rhs) {
		return
	}
	for i, l := range a.Lhs {
		switch t := l.(type) {
		case *ast.Ident:
			if w.fc != nil {
				w.fc.locals[t.Name] = append(w.fc.locals[t.Name], a.Rhs[i])
			}
			w.named(t.Name, a.Rhs[i])
		case *ast.SelectorExpr:
			w.ix.fields[t.Sel.Name] = append(w.ix.fields[t.Sel.Name], site{expr: a.Rhs[i], file: w.file, fc: w.fc})
			w.named(t.Sel.Name, a.Rhs[i])
		}
	}
}

// declare records a function-local var or const.
func (w *walker) declare(d *ast.DeclStmt) {
	// A declaration statement sits in a function body and holds a GenDecl.
	g := d.Decl.(*ast.GenDecl)
	for _, s := range g.Specs {
		vs, ok := s.(*ast.ValueSpec)
		if !ok || len(vs.Names) != len(vs.Values) {
			continue
		}
		for i, n := range vs.Names {
			w.fc.locals[n.Name] = append(w.fc.locals[n.Name], vs.Values[i])
			w.named(n.Name, vs.Values[i])
		}
	}
}

// pkgOf is the import path a selector's left side names, if it is an import.
func (w *walker) pkgOf(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return w.file.imports[id.Name]
	}
	return ""
}

// composite records a literal's keyed fields, and a produce for a kgo.Record
// with a Topic.
func (w *walker) composite(c *ast.CompositeLit) {
	k := keyed{vals: map[string]ast.Expr{}, file: w.file, fc: w.fc}
	owner := typeNameOf(c.Type)
	for _, el := range c.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id, ok := kv.Key.(*ast.Ident); ok {
			k.vals[id.Name] = kv.Value
			w.ix.fields[id.Name] = append(w.ix.fields[id.Name], site{expr: kv.Value, file: w.file, fc: w.fc, owner: owner})
			w.named(id.Name, kv.Value)
		}
	}
	if len(k.vals) > 1 {
		w.ix.keyeds = append(w.ix.keyeds, k)
	}
	sel, ok := c.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Record" || w.pkgOf(sel.X) != kgoPath || w.skip[c] {
		return
	}
	if t, ok := k.vals["Topic"]; ok {
		w.flow(t, FlowProduce, false, false, c.Pos())
	}
}

// flow queues a flow site for resolution once the index is whole.
func (w *walker) flow(e ast.Expr, dir string, family, partitions bool, pos token.Pos) {
	w.ix.flows = append(w.ix.flows, flowSite{site: site{expr: e, file: w.file, fc: w.fc},
		direction: dir, family: family, partitions: partitions, pos: pos})
}

// call records a call site and any flow it states.
func (w *walker) call(c *ast.CallExpr) {
	name := calleeName(c)
	if name != "" {
		w.ix.calls[name] = append(w.ix.calls[name], site{expr: c, file: w.file, fc: w.fc})
	}
	if name == "StringVar" && len(c.Args) >= 3 {
		// flag.StringVar(&cfg.Field, name, default, usage): the default is
		// the field's static value.
		if u, ok := c.Args[0].(*ast.UnaryExpr); ok {
			if s, ok := u.X.(*ast.SelectorExpr); ok {
				w.ix.fields[s.Sel.Name] = append(w.ix.fields[s.Sel.Name], site{expr: c.Args[2], file: w.file, fc: w.fc})
			}
		}
	}
	if name == "CommitRecords" || name == "CommitOffsets" {
		// A record handed back to commit is a consumer's offset, not a produce.
		for _, a := range c.Args {
			ast.Inspect(a, func(n ast.Node) bool {
				if cl, ok := n.(*ast.CompositeLit); ok {
					w.skip[cl] = true
				}
				return true
			})
		}
	}
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	pkg := w.pkgOf(sel.X)
	switch {
	case pkg == kgoPath:
		w.kgo(sel.Sel.Name, c)
	case strings.HasSuffix(pkg, kafkatopicsPkg) && (sel.Sel.Name == "Record" || sel.Sel.Name == "Tombstone") && len(c.Args) > 0:
		w.flow(c.Args[0], FlowProduce, false, false, c.Pos())
	case strings.HasSuffix(pkg, heartbeatPkg) && sel.Sel.Name == "Start" && len(c.Args) > 2:
		w.flow(c.Args[2], FlowProduce, true, false, c.Pos())
	case strings.HasSuffix(pkg, requestlogPkg) && sel.Sel.Name == "TopicSink" && len(c.Args) > 0:
		w.flow(c.Args[0], FlowProduce, true, false, c.Pos())
	}
}

// kgo reads franz-go's client options.
func (w *walker) kgo(opt string, c *ast.CallExpr) {
	root := w.fc.root()
	switch opt {
	case "ConsumeTopics":
		for _, a := range c.Args {
			w.flow(a, FlowConsume, false, false, c.Pos())
		}
	case "ConsumePartitions":
		if len(c.Args) != 1 {
			return
		}
		if m, ok := c.Args[0].(*ast.CompositeLit); ok {
			for _, el := range m.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					w.flow(kv.Key, FlowConsume, false, true, c.Pos())
				}
			}
			return
		}
		w.flow(c.Args[0], FlowConsume, false, true, c.Pos())
	case "ConsumerGroup":
		if root != nil && len(c.Args) == 1 {
			root.groups = append(root.groups, site{expr: c.Args[0], file: w.file, fc: w.fc})
		}
	case "ConsumeRegex":
		if root != nil {
			root.regex = true
		}
	case "DefaultProduceTopic":
		if len(c.Args) == 1 {
			w.flow(c.Args[0], FlowProduce, false, false, c.Pos())
		}
	}
}
