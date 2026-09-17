package rearm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// moduleRoot walks upward from the test's working directory to the nearest
// go.mod. It stays package-local rather than exporting test-only machinery;
// duplicating this handful of setup lines per package is the repo's precedent.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		} else if !os.IsNotExist(statErr) {
			t.Fatalf("stat go.mod in %s: %v", dir, statErr)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod above test working directory")
		}
		dir = parent
	}
}

// pointerCalls returns every rearm.Pointer(...) call expression found in
// path's source.
func pointerCalls(t *testing.T, path string) []*ast.CallExpr {
	t.Helper()
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var calls []*ast.CallExpr
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Pointer" {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || pkg.Name != "rearm" {
			return true
		}
		calls = append(calls, call)
		return true
	})
	return calls
}

// callsPointerWithSelectorArg reports whether any rearm.Pointer(...) call in
// path passes, as its second argument, the exact selector expression
// pkgName.selName — e.g. rearm.DefaultThresholdBytes, referenced by
// identifier, never re-typed as a literal.
func callsPointerWithSelectorArg(t *testing.T, path, pkgName, selName string) bool {
	t.Helper()
	for _, call := range pointerCalls(t, path) {
		if len(call.Args) != 2 {
			continue
		}
		selector, ok := call.Args[1].(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != selName {
			continue
		}
		ident, ok := selector.X.(*ast.Ident)
		if ok && ident.Name == pkgName {
			return true
		}
	}
	return false
}

// callsPointerWithDerivedCall reports whether any rearm.Pointer(...) call in
// path passes, as its second argument, a call to a method/function named
// selName — e.g. engine.rearmThresholdBytes(target) — rather than a bare
// constant.
func callsPointerWithDerivedCall(t *testing.T, path, selName string) bool {
	t.Helper()
	for _, call := range pointerCalls(t, path) {
		if len(call.Args) != 2 {
			continue
		}
		inner, ok := call.Args[1].(*ast.CallExpr)
		if !ok {
			continue
		}
		selector, ok := inner.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == selName {
			return true
		}
	}
	return false
}

// TestReloadAndSelfCompactBothComposeThroughPointer pins behaviour 1 (ONE
// WRITER for the re-arm pointer text) together with the reload half of
// behaviour 3 (the deliberate reload/self-compact threshold asymmetry):
// both reset call sites — cmd/pfm/reload_command.go and
// internal/inject/engine.go — must call rearm.Pointer rather than composing
// their own steer string, and each must pass the specific threshold
// Pointer's own doc comment promises: reload passes
// rearm.DefaultThresholdBytes UNCHANGED; self-compact passes its own
// derived rearmThresholdBytes(...) budget. A hand-rolled pointer string in
// either file — or reload silently switched onto the derived budget — would
// compile clean and pass every other test in this package; only reading the
// actual call sites catches it.
func TestReloadAndSelfCompactBothComposeThroughPointer(t *testing.T) {
	root := moduleRoot(t)

	t.Run("reload_command.go calls rearm.Pointer with the unmodified default threshold", func(t *testing.T) {
		path := filepath.Join(root, "cmd", "pfm", "reload_command.go")
		if !callsPointerWithSelectorArg(t, path, "rearm", "DefaultThresholdBytes") {
			t.Fatalf("%s: no rearm.Pointer(..., rearm.DefaultThresholdBytes) call found", path)
		}
	})

	t.Run("inject/engine.go calls rearm.Pointer with its own derived channel budget", func(t *testing.T) {
		path := filepath.Join(root, "internal", "inject", "engine.go")
		if !callsPointerWithDerivedCall(t, path, "rearmThresholdBytes") {
			t.Fatalf("%s: no rearm.Pointer(..., ....rearmThresholdBytes(...)) call found", path)
		}
	})
}
