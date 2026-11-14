// Package main is testassert: it classifies every Go test function of a tree by the checks it makes,
// read with go/parser and go/ast, never a regex — arch-check C35's enumerator and the exact-tests census.
//
//	go run ./scripts/testassert -root DIR [-census] < list
//
// stdin lists .go paths relative to DIR, one a line (arch-check's all.list: test files hold the tests,
// every file lends the helpers and the functions a test drives). A test function is `func Test…(*testing.T)`
// or `func Fuzz…(*testing.F)` in a _test.go file, TestMain excepted. Its class:
//
//	substring  every value check is a substring or regexp match (strings/bytes Contains, HasPrefix,
//	           HasSuffix, Index…, a regexp Match/Find)
//	mixed      substring checks beside exact ones
//	exact      exact checks only (==/!= on a value, reflect.DeepEqual, cmp.Diff, slices/maps/bytes.Equal, errors.Is/As)
//	weak       only guards: nil, truthiness, an empty string, a bare length, an ordering
//	none       no check found (a failure call the walk does not see as an if's)
//
// Output, byte-sorted: one `{file} {Func}` line per substring test (C35's ratchet input), or with
// -census a header and one TSV row per test: dir file line func class substr exact weak drives.
// BROKEN STATE: an unreadable or unparsable file prints `testassert: {why}` on stderr and exits 2;
// "could not look" never reads as zero.
//
//   - classify.go: the check classes and the walk from a test through its helpers.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func main() {
	os.Exit(censusRun(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func censusRun(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("testassert", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "the tree the listed paths are relative to")
	census := flags.Bool("census", false, "print every test as a TSV row")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	tree, err := parseTree(*root, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "testassert: %v\n", err)
		return 2
	}
	rows := tree.classify()
	if *census {
		fmt.Fprintln(stdout, "dir\tfile\tline\tfunc\tclass\tsubstr\texact\tweak\tdrives")
	}
	for _, r := range rows {
		switch {
		case *census:
			fmt.Fprintf(stdout, "%s\t%s\t%d\t%s\t%s\t%d\t%d\t%d\t%s\n", filepath.Dir(r.file), filepath.Base(r.file),
				r.line, r.name, r.class, r.substr, r.exact, r.weak, strings.Join(r.drives, ","))
		case r.class == classSubstring:
			fmt.Fprintf(stdout, "%s %s\n", r.file, r.name)
		}
	}
	return 0
}

// tree is every parsed file, grouped by directory (a package and its external _test package are one unit).
type tree struct {
	fset    *token.FileSet
	module  string
	files   map[string]*ast.File // path → file
	dirs    map[string][]string  // dir → paths
	funcs   map[string]*ast.FuncDecl
	methods map[string][]*ast.FuncDecl // dir + "." + method name
	paths   map[*ast.FuncDecl]string   // decl → its file
}

var modulePath = regexp.MustCompile(`(?m)^module\s+(\S+)`)

func parseTree(root string, list io.Reader) (*tree, error) {
	t := &tree{
		fset: token.NewFileSet(), files: map[string]*ast.File{}, dirs: map[string][]string{},
		funcs: map[string]*ast.FuncDecl{}, methods: map[string][]*ast.FuncDecl{}, paths: map[*ast.FuncDecl]string{},
	}
	// Without the module path no cross-package helper resolves, and a test checking through one would
	// class as no check at all: could not look, never fewer substring tests.
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("read go.mod: %w", err)
	}
	m := modulePath.FindSubmatch(mod)
	if m == nil {
		return nil, fmt.Errorf("read go.mod: no module line in %s", filepath.Join(root, "go.mod"))
	}
	t.module = string(m[1])
	scan := bufio.NewScanner(list)
	for scan.Scan() {
		path := strings.TrimSpace(scan.Text())
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		file, err := parser.ParseFile(t.fset, filepath.Join(root, path), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		t.add(path, file)
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("read the file list: %w", err)
	}
	return t, nil
}

func (t *tree) add(path string, file *ast.File) {
	dir := filepath.Dir(path)
	t.files[path] = file
	t.dirs[dir] = append(t.dirs[dir], path)
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		t.paths[fn] = path
		if fn.Recv == nil {
			t.funcs[dir+"."+fn.Name.Name] = fn
		} else {
			t.methods[dir+"."+fn.Name.Name] = append(t.methods[dir+"."+fn.Name.Name], fn)
		}
	}
}

// row is one test function's census line.
type row struct {
	file, name, class   string
	line                int
	substr, exact, weak int
	drives              []string
}

func (t *tree) classify() []row {
	var rows []row
	for path, file := range t.files {
		if !strings.HasSuffix(path, "_test.go") {
			continue
		}
		for _, d := range file.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && isTestFunc(fn) {
				rows = append(rows, t.walkTest(path, fn))
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].file != rows[j].file {
			return rows[i].file < rows[j].file
		}
		return rows[i].name < rows[j].name
	})
	return rows
}

func isTestFunc(fn *ast.FuncDecl) bool {
	name := fn.Name.Name
	if fn.Recv != nil || name == "TestMain" || len(fn.Type.Params.List) != 1 {
		return false
	}
	want := map[bool]string{true: "T", false: "F"}[strings.HasPrefix(name, "Test")]
	if !strings.HasPrefix(name, "Test") && !strings.HasPrefix(name, "Fuzz") {
		return false
	}
	star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	return ok && selector(star.X) == "testing."+want
}

// selector renders `pkg.Name` for a selector on an identifier, else "".
func selector(e ast.Expr) string {
	if s, ok := e.(*ast.SelectorExpr); ok {
		if id, ok := s.X.(*ast.Ident); ok {
			return id.Name + "." + s.Sel.Name
		}
	}
	return ""
}

func takesTesting(fn *ast.FuncDecl) bool {
	for _, p := range fn.Type.Params.List {
		t := p.Type
		if star, ok := t.(*ast.StarExpr); ok {
			t = star.X
		}
		if s := selector(t); strings.HasPrefix(s, "testing.") {
			return true
		}
	}
	return false
}

func receiver(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.StarExpr:
		return receiver(e.X)
	case *ast.IndexExpr:
		return receiver(e.X)
	case *ast.IndexListExpr:
		return receiver(e.X)
	case *ast.Ident:
		return e.Name
	}
	return "?"
}

// imports maps each import's name in a file to its path.
func (t *tree) imports(path string) map[string]string {
	out := map[string]string{}
	for _, spec := range t.files[path].Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := filepath.Base(p)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		out[name] = p
	}
	return out
}

// dirOf turns a module import path into its directory under the root; "" outside the module.
func (t *tree) dirOf(importPath string) string {
	if t.module == "" || !strings.HasPrefix(importPath, t.module+"/") {
		return ""
	}
	return strings.TrimPrefix(importPath, t.module+"/")
}
