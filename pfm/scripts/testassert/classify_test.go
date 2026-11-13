package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestClassOfCheck(t *testing.T) {
	for _, tc := range []struct{ cond, want string }{
		{`!strings.Contains(out, "x")`, classSubstring},
		{`err == nil || !strings.Contains(err.Error(), "x")`, classSubstring},
		{`strings.Count(out, "x") != 2`, classSubstring},
		{`re.FindString(out) != "x"`, classSubstring},
		{`!bytes.HasSuffix(b, nl)`, classSubstring},
		{`got != want`, classExact},
		{`code != 2`, classExact},
		{`!reflect.DeepEqual(got, want)`, classExact},
		{`!errors.Is(err, fs.ErrNotExist)`, classExact},
		{`!at.Equal(then)`, classExact},
		{`err != nil`, classWeak},
		{`(nil) != err`, classWeak},
		{`got == ""`, classWeak},
		{`len(rows) != 3`, classWeak},
		{`n > 2`, classWeak},
		{`!ok`, classWeak},
		{`slices.Contains(list, "x")`, classWeak},
	} {
		expr, err := parser.ParseExpr(tc.cond)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.cond, err)
		}
		if got := classOf(nil, expr); got != tc.want {
			t.Errorf("classOf(%s) = %s, want %s", tc.cond, got, tc.want)
		}
	}
}

// TestFailsCountsOnlyAFailingCallStatement: a branch fails the test when it calls a failing method as
// a statement; the same names yielding a value, or a call inside a nested if or closure, fail nothing.
func TestFailsCountsOnlyAFailingCallStatement(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`t.Fatalf("x")`, true},
		{`t.Error("x")`, true},
		{`msg = err.Error()`, false},
		{`return fmt.Errorf("x")`, false},
		{`if ok { t.Fatal("x") }`, false},
		{`func() { t.Fail() }()`, false},
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "x.go", "package x\nfunc f() {\n"+tc.body+"\n}\n", 0)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.body, err)
		}
		if got := fails(file.Decls[0].(*ast.FuncDecl).Body); got != tc.want {
			t.Errorf("fails({%s}) = %t, want %t", tc.body, got, tc.want)
		}
	}
}
