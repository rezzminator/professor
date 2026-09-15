package deps

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProductionExecLiteralsAreRegisteredAndResolved(t *testing.T) {
	root := filepath.Join("..", "..")
	filesScanned := 0
	execCallsScanned := 0
	for _, directory := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
				strings.Contains(path, string(filepath.Separator)+"deps"+string(filepath.Separator)) {
				return nil
			}
			filesScanned++
			set := token.NewFileSet()
			file, parseErr := parser.ParseFile(set, path, nil, 0)
			if parseErr != nil {
				return parseErr
			}
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok ||
					(selector.Sel.Name != "Command" && selector.Sel.Name != "CommandContext" && selector.Sel.Name != "LookPath") {
					return true
				}
				pkg, ok := selector.X.(*ast.Ident)
				if !ok || pkg.Name != "exec" {
					return true
				}
				execCallsScanned++
				argument := call.Args[0]
				if selector.Sel.Name == "CommandContext" {
					if len(call.Args) < 2 {
						return true
					}
					argument = call.Args[1]
				}
				literal, ok := argument.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				name, quoteErr := strconv.Unquote(literal.Value)
				if quoteErr != nil {
					t.Errorf("%s: decode command literal: %v", set.Position(literal.Pos()), quoteErr)
					return true
				}
				if !Registered(name) {
					t.Errorf("%s: external command %q is absent from deps registry", set.Position(literal.Pos()), name)
					return true
				}
				t.Errorf("%s: registered command %q bypasses deps.Resolve", set.Position(literal.Pos()), name)
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", directory, err)
		}
	}
	if filesScanned == 0 {
		t.Fatal("dependency source guard scanned zero production Go files")
	}
	if execCallsScanned == 0 {
		t.Fatal("dependency source guard found zero production exec calls")
	}
}
