package hostfixture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestPackageDocNamesEveryExportedFixtureFunction parses every non-test
// source file in this package and asserts doc.go's package comment mentions
// every exported top-level function by name — so a fixture added later
// without updating the doc comment fails this test instead of silently
// drifting out of sync with what the package actually exports.
func TestPackageDocNamesEveryExportedFixtureFunction(t *testing.T) {
	docSource, err := os.ReadFile("doc.go")
	if err != nil {
		t.Fatalf("read doc.go: %v", err)
	}
	docText := string(docSource)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.): %v", err)
	}
	fset := token.NewFileSet()
	var missing []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") || name == "doc.go" {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() {
				continue
			}
			if !strings.Contains(docText, fn.Name.Name) {
				missing = append(missing, fn.Name.Name+" ("+name+")")
			}
		}
	}
	if len(missing) > 0 {
		t.Fatalf("doc.go's package comment does not name: %s", strings.Join(missing, ", "))
	}
}
