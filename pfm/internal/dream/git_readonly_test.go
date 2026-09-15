package dream

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProductionDreamSourcesContainNoGitWriteInvocation(t *testing.T) {
	root := moduleRoot(t)
	scanRoots := []string{
		filepath.Join(root, "internal", "dream"),
		filepath.Join(root, "cmd", "pfm", "dream_command.go"),
	}
	writeVerbs := map[string]struct{}{
		"add": {}, "am": {}, "apply": {}, "bisect": {}, "branch": {},
		"checkout": {}, "cherry-pick": {}, "clean": {}, "clone": {}, "commit": {},
		"fetch": {}, "gc": {}, "init": {}, "merge": {}, "mv": {}, "notes": {},
		"pull": {}, "push": {}, "rebase": {}, "reset": {}, "restore": {}, "revert": {},
		"rm": {}, "stash": {}, "submodule": {}, "switch": {}, "tag": {}, "update-ref": {},
	}
	files := 0
	for _, scanRoot := range scanRoots {
		info, err := os.Stat(scanRoot)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			files++
			scanGitInvocations(t, scanRoot, writeVerbs)
			continue
		}
		err = filepath.WalkDir(scanRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			files++
			scanGitInvocations(t, path, writeVerbs)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if files == 0 {
		t.Fatal("production Git invocation scan visited no files")
	}
}

func scanGitInvocations(t *testing.T, path string, writeVerbs map[string]struct{}) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	aliases := map[string]struct{}{}
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if importPath != "os/exec" {
			continue
		}
		alias := "exec"
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		aliases[alias] = struct{}{}
	}
	parents := make(map[ast.Node]ast.Node)
	var stack []ast.Node
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		if len(stack) > 0 {
			parents[node] = stack[len(stack)-1]
		}
		stack = append(stack, node)
		return true
	})
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (selector.Sel.Name != "Command" && selector.Sel.Name != "CommandContext") {
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		if _, ok := aliases[ident.Name]; !ok || len(call.Args) == 0 {
			return true
		}
		binary, ok := stringLiteral(call.Args[0])
		if !ok || binary != "git" {
			return true
		}
		function := enclosingFunction(call, parents)
		if function == nil {
			t.Errorf("%s:%d invokes Git outside a function", path, fset.Position(call.Pos()).Line)
			return true
		}
		readVerb := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}
			switch value {
			case "rev-parse", "cat-file", "show", "grep", "status":
				readVerb = true
			}
			if _, forbidden := writeVerbs[value]; forbidden {
				position := fset.Position(literal.Pos())
				t.Errorf(
					"%s:%d contains forbidden Git write verb %q in a Git invocation function",
					path,
					position.Line,
					value,
				)
			}
			return true
		})
		if !readVerb {
			position := fset.Position(call.Pos())
			t.Errorf("%s:%d Git invocation has no statically pinned read verb", path, position.Line)
		}
		return true
	})
}

func enclosingFunction(node ast.Node, parents map[ast.Node]ast.Node) *ast.FuncDecl {
	for current := node; current != nil; current = parents[current] {
		if function, ok := current.(*ast.FuncDecl); ok {
			return function
		}
	}
	return nil
}

func stringLiteral(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}
