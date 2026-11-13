package main

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	classSubstring = "substring"
	classMixed     = "mixed"
	classExact     = "exact"
	classWeak      = "weak"
	classNone      = "none"
)

// substringCalls and exactCalls name a call by `pkg.Func` on a package identifier, or `.Method` on
// any other receiver (a compiled regexp, a time.Time).
var substringCalls = names(
	"strings.Contains strings.ContainsAny strings.ContainsRune strings.ContainsFunc strings.HasPrefix",
	"strings.HasSuffix strings.Index strings.IndexAny strings.IndexByte strings.IndexRune strings.IndexFunc",
	"strings.LastIndex strings.LastIndexAny strings.LastIndexByte strings.Count strings.Cut strings.CutPrefix",
	"strings.CutSuffix bytes.Contains bytes.ContainsAny bytes.ContainsRune bytes.HasPrefix bytes.HasSuffix",
	"bytes.Index bytes.IndexByte bytes.LastIndex bytes.Count regexp.MatchString regexp.Match .MatchString",
	".Match .FindString .FindStringSubmatch .FindAllString .FindAllStringSubmatch .FindStringIndex",
	".FindSubmatch .FindAllSubmatch",
)

var exactCalls = names(
	"reflect.DeepEqual cmp.Diff cmp.Equal slices.Equal slices.EqualFunc maps.Equal maps.EqualFunc",
	"bytes.Equal strings.EqualFold errors.Is errors.As .Equal",
)

// failCalls are the methods that fail a test: an if whose branch calls one is a check.
var failCalls = names("Error Errorf Fatal Fatalf Fail FailNow")

func names(lines ...string) map[string]bool {
	set := map[string]bool{}
	for _, l := range lines {
		for _, n := range strings.Fields(l) {
			set[n] = true
		}
	}
	return set
}

// walker follows one test through the helpers it calls, collecting each check once by position.
type walker struct {
	t      *tree
	seen   map[*ast.FuncDecl]bool
	checks map[token.Pos]string
	drives map[string]bool
}

func (t *tree) walkTest(path string, fn *ast.FuncDecl) row {
	w := &walker{t: t, seen: map[*ast.FuncDecl]bool{}, checks: map[token.Pos]string{}, drives: map[string]bool{}}
	w.walk(path, fn)
	r := row{file: path, name: fn.Name.Name, line: t.fset.Position(fn.Pos()).Line}
	for _, c := range w.checks {
		switch c {
		case classSubstring:
			r.substr++
		case classExact:
			r.exact++
		default:
			r.weak++
		}
	}
	switch {
	case r.substr > 0 && r.exact > 0:
		r.class = classMixed
	case r.substr > 0:
		r.class = classSubstring
	case r.exact > 0:
		r.class = classExact
	case r.weak > 0:
		r.class = classWeak
	default:
		r.class = classNone
	}
	for d := range w.drives {
		r.drives = append(r.drives, d)
	}
	sort.Strings(r.drives)
	return r
}

func (w *walker) walk(path string, fn *ast.FuncDecl) {
	if fn.Body == nil || w.seen[fn] {
		return
	}
	w.seen[fn] = true
	imports := w.t.imports(path)
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.IfStmt:
			if fails(n.Body) || fails(n.Else) {
				w.checks[n.Pos()] = classOf(n.Init, n.Cond)
			}
		case *ast.CallExpr:
			w.call(filepath.Dir(path), imports, n.Fun)
		}
		return true
	})
}

// call resolves a callee inside the module: a test helper (a _test.go function, or one taking a
// testing type) is walked for its checks, anything else is a function the test drives.
func (w *walker) call(dir string, imports map[string]string, fun ast.Expr) {
	var found []*ast.FuncDecl
	switch f := fun.(type) {
	case *ast.Ident:
		if fn := w.t.funcs[dir+"."+f.Name]; fn != nil {
			found = append(found, fn)
		}
	case *ast.SelectorExpr:
		if id, ok := f.X.(*ast.Ident); ok && imports[id.Name] != "" {
			if fn := w.t.funcs[w.t.dirOf(imports[id.Name])+"."+f.Sel.Name]; fn != nil {
				found = append(found, fn)
			}
		} else {
			found = w.t.methods[dir+"."+f.Sel.Name]
		}
	}
	for _, fn := range found {
		path := w.t.paths[fn]
		if strings.HasSuffix(path, "_test.go") || takesTesting(fn) {
			w.walk(path, fn)
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil {
			name = receiver(fn.Recv.List[0].Type) + "." + name
		} else if d := filepath.Dir(path); d != dir {
			name = filepath.Base(d) + "." + name
		}
		w.drives[name] = true
	}
}

// fails reports whether a branch calls a failing method, as a statement of its own, outside a nested
// if or closure: a value it yields (msg = err.Error(), return fmt.Errorf(…)) fails nothing.
func fails(branch ast.Stmt) bool {
	block, ok := branch.(*ast.BlockStmt)
	if !ok {
		return false
	}
	found := false
	ast.Inspect(block, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit, *ast.IfStmt:
			return false
		case *ast.ExprStmt:
			call, ok := n.X.(*ast.CallExpr)
			if !ok {
				return true
			}
			if s, ok := call.Fun.(*ast.SelectorExpr); ok && failCalls[s.Sel.Name] {
				found = true
			}
		}
		return !found
	})
	return found
}

// classOf classes one check by its init and condition: substring when any substring call takes part,
// else exact on an exact call or an ==/!= between two values, else weak (nil, truthiness, "", len, order).
func classOf(nodes ...ast.Node) string {
	substr, exact := false, false
	for _, node := range nodes {
		if node == nil {
			continue
		}
		ast.Inspect(node, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.CallExpr:
				name := callName(n.Fun)
				substr = substr || substringCalls[name]
				exact = exact || exactCalls[name]
			case *ast.BinaryExpr:
				if (n.Op == token.EQL || n.Op == token.NEQ) && !guard(n.X) && !guard(n.Y) {
					exact = true
				}
			}
			return true
		})
	}
	switch {
	case substr:
		return classSubstring
	case exact:
		return classExact
	}
	return classWeak
}

func callName(fun ast.Expr) string {
	s, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if id, ok := s.X.(*ast.Ident); ok &&
		(substringCalls[id.Name+"."+s.Sel.Name] || exactCalls[id.Name+"."+s.Sel.Name]) {
		return id.Name + "." + s.Sel.Name
	}
	return "." + s.Sel.Name
}

// guard reports an operand that makes ==/!= an existence check: nil, a bool literal, "", len or cap.
func guard(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name == "nil" || e.Name == "true" || e.Name == "false"
	case *ast.BasicLit:
		s, err := strconv.Unquote(e.Value)
		return e.Kind == token.STRING && err == nil && s == ""
	case *ast.CallExpr:
		id, ok := e.Fun.(*ast.Ident)
		return ok && (id.Name == "len" || id.Name == "cap")
	case *ast.ParenExpr:
		return guard(e.X)
	}
	return false
}
