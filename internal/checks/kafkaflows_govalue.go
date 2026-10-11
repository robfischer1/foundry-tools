package checks

import (
	"go/ast"
	"go/token"
	"path"
	"regexp"
	"strings"
)

// What one Go expression evaluates to, statically: the single-name half of
// fleet:kafka-flows' resolution (kafkaflows_goresolve.go pairs names).

// resolve answers every static value an expression can take. Empty means
// none is known.
func (ix *goIndex) resolve(s site, depth int, seen map[ast.Expr]bool) []rval {
	if depth > resolveDepth || s.expr == nil || seen[s.expr] {
		return nil
	}
	seen[s.expr] = true
	defer delete(seen, s.expr)
	switch e := s.expr.(type) {
	case *ast.BasicLit:
		if v, ok := stringLit(e); ok {
			return []rval{{s: v}}
		}
	case *ast.ParenExpr:
		return ix.resolve(site{expr: e.X, file: s.file, fc: s.fc}, depth+1, seen)
	case *ast.CompositeLit:
		// A list of names (`[]string{a, b}` spread into ConsumeTopics).
		var out []rval
		for _, el := range e.Elts {
			out = append(out, ix.resolve(site{expr: el, file: s.file, fc: s.fc}, depth+1, seen)...)
		}
		return out
	case *ast.Ident:
		return ix.ident(e.Name, s, depth, seen)
	case *ast.SelectorExpr:
		return ix.selector(e, s, depth, seen)
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			return ix.concat(e, s, depth, seen)
		}
	case *ast.CallExpr:
		return ix.callValue(e, s, depth, seen)
	}
	return nil
}

// via tags values with how they were reached, keeping the first tag.
func via(vals []rval, how string) []rval {
	for i := range vals {
		if vals[i].via == "" {
			vals[i].via = how
		}
	}
	return vals
}

// ident resolves a bare name: a local, a parameter (through its callers), a
// package-level value, then the core's constant table. A parameter that is
// also reassigned (`if group == "" { group = Default }`) answers both.
func (ix *goIndex) ident(name string, s site, depth int, seen map[ast.Expr]bool) []rval {
	for fc := s.fc; fc != nil; fc = fc.outer {
		if b, ok := fc.bound[name]; ok {
			return ix.resolve(b, depth+1, seen)
		}
		var out []rval
		for _, r := range fc.locals[name] {
			out = append(out, ix.resolve(site{expr: r, file: s.file, fc: fc}, depth+1, seen)...)
		}
		idx, isParam := paramPos(fc, name)
		if isParam {
			for _, c := range ix.callers(fc) {
				out = append(out, ix.resolve(site{expr: c.expr.(*ast.CallExpr).Args[idx], file: c.file, fc: c.fc}, depth+1, seen)...)
			}
			return via(out, "param "+name)
		}
		if len(fc.locals[name]) > 0 {
			return out
		}
	}
	if vs := ix.pkg[s.file.dir][name]; len(vs) > 0 {
		var out []rval
		for _, v := range vs {
			out = append(out, ix.resolve(v, depth+1, seen)...)
		}
		return via(out, "const "+name)
	}
	return ix.knownTopic(name)
}

// knownTopic reads the core's constant table.
func (ix *goIndex) knownTopic(name string) []rval {
	if ix.known == nil {
		return nil
	}
	if v, ok := ix.known(name); ok {
		return []rval{{s: v, via: "core " + name}}
	}
	return nil
}

// selector resolves pkg.Name through an import, or x.Field through the
// tree's writes of that field.
func (ix *goIndex) selector(e *ast.SelectorExpr, s site, depth int, seen map[ast.Expr]bool) []rval {
	if id, ok := e.X.(*ast.Ident); ok {
		if imp, ok := s.file.imports[id.Name]; ok {
			if dir, inTree := ix.dirOf(imp); inTree {
				var out []rval
				for _, v := range ix.pkg[dir][e.Sel.Name] {
					out = append(out, ix.resolve(v, depth+1, seen)...)
				}
				return via(out, "const "+id.Name+"."+e.Sel.Name)
			}
			if strings.Contains(imp, coreModule) {
				return ix.knownTopic(e.Sel.Name)
			}
			return nil
		}
	}
	owner := ""
	if id, ok := e.X.(*ast.Ident); ok {
		owner = ix.typeOfName(id.Name, s.fc)
	}
	return ix.field(e.Sel.Name, owner, s, depth, seen)
}

// dirOf is the tree directory an import path names, when this tree holds it.
func (ix *goIndex) dirOf(imp string) (string, bool) {
	for mod, dir := range ix.modules {
		if imp == mod || strings.HasPrefix(imp, mod+"/") {
			return path.Clean(path.Join(dir, strings.TrimPrefix(imp, mod))), true
		}
	}
	return "", false
}

// field resolves a struct field through every write of it: a composite
// literal key, an assignment or a flag default. The writes in the reader's own
// package win; when they say nothing, only writes from ONE other package are
// trusted, since a field name two packages share says nothing about which
// struct this is. When the reader's type can be read off the code (owner),
// the writes in literals of that type are the only ones asked.
func (ix *goIndex) field(name, owner string, s site, depth int, seen map[ast.Expr]bool) []rval {
	var local, foreign []site
	dirs := map[string]bool{}
	sites := ix.fields[name]
	if owner != "" {
		var typed []site
		for _, f := range sites {
			if f.owner == owner {
				typed = append(typed, f)
			}
		}
		if len(typed) > 0 {
			sites = typed
		}
	}
	for _, f := range sites {
		if f.file.dir == s.file.dir {
			local = append(local, f)
		} else {
			foreign = append(foreign, f)
			dirs[f.file.dir] = true
		}
	}
	var out []rval
	for _, f := range local {
		out = append(out, ix.resolve(f, depth+1, seen)...)
	}
	if len(out) == 0 && len(dirs) == 1 {
		for _, f := range foreign {
			out = append(out, ix.resolve(f, depth+1, seen)...)
		}
	}
	return via(out, "field "+name)
}

// concat resolves a + b; an unknown side becomes `*`, a family glob, as long
// as the other side is known.
func (ix *goIndex) concat(e *ast.BinaryExpr, s site, depth int, seen map[ast.Expr]bool) []rval {
	l := ix.resolve(site{expr: e.X, file: s.file, fc: s.fc}, depth+1, seen)
	r := ix.resolve(site{expr: e.Y, file: s.file, fc: s.fc}, depth+1, seen)
	if len(l) == 0 && len(r) == 0 {
		return nil
	}
	if len(l) == 0 {
		l = []rval{{s: "*", pattern: true}}
	}
	if len(r) == 0 {
		r = []rval{{s: "*", pattern: true}}
	}
	var out []rval
	for _, a := range l {
		for _, b := range r {
			how := a.via
			if how == "" {
				how = b.via
			}
			out = append(out, rval{s: a.s + b.s, pattern: a.pattern || b.pattern, via: how})
		}
	}
	return out
}

// callValue resolves the calls a topic name is commonly built with: an env
// lookup with a literal default, cmp.Or, Sprintf, a glob-to-regex helper,
// requestlog.CallsTopic, and a tree function that returns a constant.
func (ix *goIndex) callValue(c *ast.CallExpr, s site, depth int, seen map[ast.Expr]bool) []rval {
	name := calleeName(c)
	arg := func(i int) []rval { return ix.resolve(site{expr: c.Args[i], file: s.file, fc: s.fc}, depth+1, seen) }
	switch {
	case name == "Sprintf" && len(c.Args) > 0:
		return ix.sprintf(c, s, depth, seen)
	case name == "QuoteMeta" && len(c.Args) == 1:
		return arg(0)
	case name == "TopicRegex" && len(c.Args) == 1:
		vals := arg(0)
		for i := range vals {
			vals[i].pattern = true
		}
		return vals
	case name == "CallsTopic" && len(c.Args) == 1:
		vals := arg(0)
		if len(vals) == 0 {
			return []rval{{s: "*._ops.calls", pattern: true}}
		}
		for i := range vals {
			vals[i].s += "._ops.calls"
		}
		return vals
	case len(c.Args) >= 2 && envLookup(name, c.Args[0]):
		env, _ := stringLit(c.Args[0])
		return via(arg(len(c.Args)-1), "env "+env+" default")
	case name == "Or" && len(c.Args) > 0:
		var out []rval
		for i := range c.Args {
			out = append(out, arg(i)...)
		}
		return via(out, "default")
	}
	return ix.inline(c, s, depth, seen)
}

// inline evaluates a tree function for this one call: its parameters are
// bound to the call's arguments and its return values resolved, so
// `cfg.Group("turns")` answers `<ConsumerGroup>-turns` and nothing else.
func (ix *goIndex) inline(c *ast.CallExpr, s site, depth int, seen map[ast.Expr]bool) []rval {
	var out []rval
	for _, fc := range ix.funcs[calleeName(c)] {
		if fc.decl.Body == nil || fc.arity() != len(c.Args) || !ix.reaches(c, s, fc) {
			continue
		}
		bound := &funcCtx{decl: fc.decl, file: fc.file, locals: fc.locals, bound: map[string]site{}}
		for i, a := range c.Args {
			if n := paramName(fc, i); n != "" {
				bound.bound[n] = site{expr: a, file: s.file, fc: s.fc}
			}
		}
		ast.Inspect(fc.decl.Body, func(n ast.Node) bool {
			if _, lit := n.(*ast.FuncLit); lit {
				return false
			}
			if r, ok := n.(*ast.ReturnStmt); ok && len(r.Results) == 1 {
				out = append(out, ix.resolve(site{expr: r.Results[0], file: fc.file, fc: bound}, depth+1, seen)...)
			}
			return true
		})
	}
	return via(out, "func "+calleeName(c))
}

// paramName is the name of a function's i-th parameter, or "".
func paramName(fc *funcCtx, i int) string {
	n := 0
	for _, fld := range fc.funcType().Params.List {
		for _, nm := range fld.Names {
			if n == i {
				return nm.Name
			}
			n++
		}
	}
	return ""
}

// envName is what an environment variable's name looks like.
var envName = regexp.MustCompile(`^[A-Z][A-Z0-9]*_[A-Z0-9_]+$`)

// envLookup says a call reads the environment with a default: its name says
// env, or its first argument is an environment variable's name
// (`r.str("DAEDALUS_JOBS_GROUP", "daedalus-jobs")`).
func envLookup(name string, first ast.Expr) bool {
	if strings.Contains(strings.ToLower(name), "env") {
		return true
	}
	v, ok := stringLit(first)
	return ok && envName.MatchString(v)
}

// sprintf substitutes each verb with its resolved argument, or `*`.
func (ix *goIndex) sprintf(c *ast.CallExpr, s site, depth int, seen map[ast.Expr]bool) []rval {
	format, ok := stringLit(c.Args[0])
	if !ok {
		return nil
	}
	var b strings.Builder
	pattern := false
	argi := 1
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			b.WriteByte(format[i])
			continue
		}
		i++
		if format[i] == '%' {
			b.WriteByte('%')
			continue
		}
		var vals []rval
		if argi < len(c.Args) {
			vals = ix.resolve(site{expr: c.Args[argi], file: s.file, fc: s.fc}, depth+1, seen)
		}
		argi++
		if len(vals) == 1 && !vals[0].pattern {
			b.WriteString(vals[0].s)
			continue
		}
		b.WriteByte('*')
		pattern = true
	}
	return []rval{{s: b.String(), pattern: pattern}}
}
