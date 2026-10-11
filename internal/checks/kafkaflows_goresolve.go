package checks

import (
	"fmt"
	"go/ast"
	gotypes "go/types"
	"strings"
)

// What a Go flow site's names evaluate to.
//
// TOPIC AND GROUP ARE RESOLVED TOGETHER. One consumer function is commonly
// called twice — daedalus' newKafkaSourceOn(brokers, group, topic) joins
// ourea.ci.requests as one group and ourea.ci as another — so resolving each
// name on its own would pair every topic with every group, and two of the
// four answers would be flows nobody runs. When both names arrive through the
// same function's parameters, or as two fields of the same struct literal,
// they are followed one call site (one literal) at a time.

// resolveDepth bounds how many hops a name is followed through: deep enough
// for constant -> config field -> env default -> constant, through two calls.
const resolveDepth = 10

// rval is one resolved value.
type rval struct {
	s       string
	pattern bool
	via     string
}

// opt is one position of a joint answer: a value, or the expression that did
// not resolve.
type opt struct {
	v    rval
	ok   bool
	text string
}

// flowsAt resolves one site into its flows.
func (ix *goIndex) flowsAt(fs flowSite) []KafkaFlow {
	base := KafkaFlow{Direction: fs.direction, Family: fs.family,
		Where: fmt.Sprintf("%s:%d", fs.file.path, ix.fset.Position(fs.pos).Line)}
	root := fs.fc.root()
	consumer := fs.direction == FlowConsume && !fs.partitions && root != nil
	regex := consumer && root.regex
	var tuples [][]opt
	if !consumer || len(root.groups) == 0 {
		tuples = ix.joint([]site{fs.site}, 0)
	} else {
		for _, g := range root.groups {
			tuples = append(tuples, ix.joint([]site{fs.site, g}, 0)...)
		}
	}
	out := make([]KafkaFlow, 0, len(tuples))
	for _, t := range tuples {
		f := base
		if t[0].ok {
			f.Topic, f.Pattern, f.Via = t[0].v.s, t[0].v.pattern || regex, t[0].v.via
			if regex {
				f.Topic = RegexGlob(f.Topic)
			}
		} else {
			f.Unresolved = t[0].text
		}
		if len(t) > 1 {
			switch {
			case t[1].ok && !t[1].v.pattern:
				f.Group = t[1].v.s
			case t[1].ok:
				f.GroupUnresolved = t[1].v.s
			default:
				f.GroupUnresolved = t[1].text
			}
		}
		out = append(out, f)
	}
	return out
}

// regexParts are the regex spellings of "any name segment", read as `*`.
var regexParts = strings.NewReplacer(`[^.]+`, "*", `[^.]*`, "*", `.+`, "*", `.*`, "*", `\.`, ".")

// RegexGlob reads a subscription regex back as the glob it encodes
// (`^[^.]+\._ops\.heartbeat$` is `*._ops.heartbeat`), so a family reads the
// same whether a star subscribed by regex or by glob.
func RegexGlob(re string) string {
	re = strings.TrimSuffix(strings.TrimPrefix(re, "^"), "$")
	re = regexParts.Replace(re)
	for strings.Contains(re, "**") {
		re = strings.ReplaceAll(re, "**", "*")
	}
	return re
}

// joint resolves several expressions together (see the file comment).
func (ix *goIndex) joint(sites []site, depth int) [][]opt {
	if depth <= resolveDepth && len(sites) > 1 {
		if out, ok := ix.jointParams(sites, depth); ok {
			return out
		}
		if out, ok := ix.jointFields(sites, depth); ok {
			return out
		}
	}
	tuples := [][]opt{nil}
	for _, s := range sites {
		vals := ix.resolve(s, depth, map[ast.Expr]bool{})
		opts := make([]opt, 0, len(vals))
		text := gotypes.ExprString(s.expr)
		for _, v := range vals {
			// An empty name is no name: kgo reads "" as "none given".
			opts = append(opts, opt{v: v, ok: v.s != "", text: text})
		}
		if len(opts) == 0 {
			opts = []opt{{text: text}}
		}
		var next [][]opt
		for _, t := range tuples {
			for _, o := range opts {
				next = append(next, append(append([]opt(nil), t...), o))
			}
		}
		tuples = next
	}
	return tuples
}

// jointParams follows two or more names that are parameters of the same
// function through each of its call sites in turn.
func (ix *goIndex) jointParams(sites []site, depth int) ([][]opt, bool) {
	ctxs := make([]*funcCtx, len(sites))
	idxs := make([]int, len(sites))
	shared := map[*funcCtx]int{}
	for i, s := range sites {
		if fc, idx, ok := paramOf(s); ok {
			ctxs[i], idxs[i] = fc, idx
			shared[fc]++
		}
	}
	for fc, n := range shared {
		if n < 2 {
			continue
		}
		var out [][]opt
		for _, c := range ix.callers(fc) {
			call := c.expr.(*ast.CallExpr)
			next := append([]site(nil), sites...)
			for i := range sites {
				if ctxs[i] == fc {
					next[i] = site{expr: call.Args[idxs[i]], file: c.file, fc: c.fc}
				}
			}
			out = append(out, ix.joint(next, depth+1)...)
		}
		return out, len(out) > 0
	}
	return nil, false
}

// jointFields pairs x.A with x.B through every struct literal that sets both.
func (ix *goIndex) jointFields(sites []site, depth int) ([][]opt, bool) {
	var base string
	names := make([]string, len(sites))
	for i, s := range sites {
		sel, ok := s.expr.(*ast.SelectorExpr)
		if !ok {
			return nil, false
		}
		b := gotypes.ExprString(sel.X)
		if i > 0 && b != base {
			return nil, false
		}
		base, names[i] = b, sel.Sel.Name
	}
	var out [][]opt
	for _, k := range ix.keyeds {
		next := make([]site, len(sites))
		all := true
		for i, n := range names {
			v, ok := k.vals[n]
			if !ok {
				all = false
				break
			}
			next[i] = site{expr: v, file: k.file, fc: k.fc}
		}
		if all {
			out = append(out, ix.joint(next, depth+1)...)
		}
	}
	return out, len(out) > 0
}

// paramOf says an expression is a parameter name, and of which function. A
// local of the same name in a nearer function shadows it; a reassignment in
// the declaring function does not (it is a default, and the callers still
// decide the value).
func paramOf(s site) (*funcCtx, int, bool) {
	id, ok := s.expr.(*ast.Ident)
	if !ok {
		return nil, 0, false
	}
	for fc := s.fc; fc != nil; fc = fc.outer {
		if idx, ok := paramPos(fc, id.Name); ok {
			return fc, idx, true
		}
		if len(fc.locals[id.Name]) > 0 {
			return nil, 0, false
		}
	}
	return nil, 0, false
}

// funcType is a context's signature.
func (fc *funcCtx) funcType() *ast.FuncType {
	if fc.decl != nil {
		return fc.decl.Type
	}
	return fc.lit.Type
}

// paramPos is a parameter's position in its function's signature.
//
// Go names every parameter or none, so counting the names alone positions a
// named one; an unnamed list has no name to find.
func paramPos(fc *funcCtx, name string) (int, bool) {
	i := 0
	for _, fld := range fc.funcType().Params.List {
		for _, n := range fld.Names {
			if n.Name == name {
				return i, true
			}
			i++
		}
	}
	return 0, false
}

// arity is a function's parameter count.
func (fc *funcCtx) arity() int {
	n := 0
	for _, fld := range fc.funcType().Params.List {
		n += max(1, len(fld.Names))
	}
	return n
}

// callers are the call sites of a function: by its own name (in its own
// package, or through an import of it, or as a method), and by every name it
// is stored under — a field or variable holding it, which is how the fleet
// wires a dial it wants to fake in tests.
func (ix *goIndex) callers(fc *funcCtx) []site {
	var out []site
	take := func(name string, direct bool) {
		for _, c := range ix.calls[name] {
			call := c.expr.(*ast.CallExpr)
			if len(call.Args) != fc.arity() || (direct && !ix.reaches(call, c, fc)) {
				continue
			}
			out = append(out, c)
		}
	}
	if fc.decl != nil {
		take(fc.decl.Name.Name, true)
		for _, a := range ix.aliases[fc.decl.Name.Name] {
			take(a, false)
		}
	}
	if fc.lit != nil {
		for _, n := range ix.lits[fc.lit] {
			take(n, false)
		}
	}
	return out
}

// reaches says a call by name can be a call of this declaration: a bare call
// from its own package, a qualified call through an import of its package, or
// a method call when it is a method.
func (ix *goIndex) reaches(call *ast.CallExpr, c site, fc *funcCtx) bool {
	if _, bare := call.Fun.(*ast.Ident); bare {
		return c.file.dir == fc.file.dir && fc.decl.Recv == nil
	}
	// calls holds only bare and selector calls (calleeName), so this is one.
	sel := call.Fun.(*ast.SelectorExpr)
	if id, ok := sel.X.(*ast.Ident); ok {
		if imp, ok := c.file.imports[id.Name]; ok {
			dir, inTree := ix.dirOf(imp)
			return inTree && dir == fc.file.dir && fc.decl.Recv == nil
		}
	}
	return fc.decl.Recv != nil && ix.receiverMay(sel.X, c, fc)
}

// receiverMay says a method call's receiver may be the declaration's type.
// It is "yes" unless the receiver's type can be read off the code and is a
// different one: the caller's own receiver, or a local built from a literal of
// a type or from a constructor whose result type is declared. Without type
// checking this is a filter, not a proof — two Run methods of one arity are
// told apart only where the code says which value it holds.
func (ix *goIndex) receiverMay(x ast.Expr, c site, fc *funcCtx) bool {
	id, ok := x.(*ast.Ident)
	if !ok {
		return true
	}
	got := ix.typeOfName(id.Name, c.fc)
	return got == "" || got == typeNameOf(fc.decl.Recv.List[0].Type)
}

// typeNameOf is a type expression's bare name: *pkg.T and T[X] answer "T".
func typeNameOf(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return typeNameOf(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.IndexExpr:
		return typeNameOf(t.X)
	}
	return ""
}

// typeOfName reads a name's type off the code, or "": what a local was built
// from, a parameter's declared type, or the receiver of the method it is in.
func (ix *goIndex) typeOfName(name string, fc *funcCtx) string {
	for ; fc != nil; fc = fc.outer {
		if rhs := fc.locals[name]; len(rhs) == 1 {
			return ix.typeOfExpr(rhs[0])
		}
		if t := paramType(fc, name); t != "" {
			return t
		}
		if fc.decl != nil && fc.decl.Recv != nil {
			if r := fc.decl.Recv.List[0]; len(r.Names) == 1 && r.Names[0].Name == name {
				return typeNameOf(r.Type)
			}
		}
	}
	return ""
}

// paramType is a parameter's declared type name, or "".
func paramType(fc *funcCtx, name string) string {
	for _, fld := range fc.funcType().Params.List {
		for _, n := range fld.Names {
			if n.Name == name {
				return typeNameOf(fld.Type)
			}
		}
	}
	return ""
}

// typeOfExpr is the type a value expression builds, when the code says.
func (ix *goIndex) typeOfExpr(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.UnaryExpr:
		return ix.typeOfExpr(t.X)
	case *ast.CompositeLit:
		return typeNameOf(t.Type)
	case *ast.CallExpr:
		name, got := calleeName(t), ""
		for _, d := range ix.funcs[name] {
			res := d.funcType().Results
			if res == nil {
				return ""
			}
			n := typeNameOf(res.List[0].Type)
			if got != "" && n != got {
				return ""
			}
			got = n
		}
		return got
	}
	return ""
}
