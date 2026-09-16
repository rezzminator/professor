package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"testing"
)

// The rail the "Cosmos system focus" golden draws: with `s` pressed, the
// focused star takes the galactic centre, its ✳ chat sits at pixel (78, 6)
// and its ▲ chat at (78, 18), so the spawn edge between them is a
// dead-straight vertical line down pixel column 78 — control point (78, 12),
// walked in int(dist*1.2) = 21 steps. Every sample is mathematically exactly
// 78, which is what makes it the sharpest possible probe of how the Bezier
// is rounded.
const (
	railColumn = 78.0
	railSteps  = 21
	// railFusedSample is the one sample of the 22 whose truncated column
	// depends on whether the compiler contracted the expression.
	railFusedSample = 9
)

// railColumns is what int(bezierAt(...)) must return for the 22 samples of
// that rail, on every CPU. Columns 77 at samples 5 and 9 are not typos: with
// each product rounded separately, those two land on 77.999999999999986 and
// int() truncates them one cell to the left. That pair of dots — with the
// halo copies cosmosEdgeLight adds one braille row above and below a hot
// rail — IS the ⢸ (U+28B8) glyph ui_cosmos_focus_80.ansi holds at canvas
// row 2, column 38.
var railColumns = []int{
	78, 78, 78, 78, 78, 77, 78, 78, 78, 77, 78,
	78, 78, 78, 78, 78, 78, 78, 78, 78, 78, 78,
}

// TestBezierAtPinsTheStraightRailPixelColumns pins the exact pixel column of
// every sample of the golden's straight rail, so the file that records the
// frame and the code that draws it can never disagree about a dot again.
func TestBezierAtPinsTheStraightRailPixelColumns(t *testing.T) {
	if len(railColumns) != railSteps+1 {
		t.Fatalf("railColumns has %d entries, want %d — the fixture no longer covers the rail", len(railColumns), railSteps+1)
	}
	for sample, want := range railColumns {
		at := float64(sample) / float64(railSteps)
		value := bezierAt(railColumn, railColumn, railColumn, at)
		if got := int(value); got != want {
			t.Errorf("bezierAt sample %d/%d = %.17g → column %d, want column %d", sample, railSteps, value, got, want)
		}
	}
}

// TestBezierAtRefusesTheFusedMultiplyAdd is the cross-architecture canary.
// The Go spec (Floating-point operators) lets an implementation contract
// `a*b + c` into one fused multiply-add rounded once; the arm64 compiler does
// exactly that for the Bezier's three products, amd64 never does. bezierAt
// forbids it with explicit float64() conversions, so this test compares the
// shipped value against the contraction arm64 would otherwise emit — the
// value that turns the golden's ⢸ (U+28B8) into ⠘ (U+2818), byte 1348,
// 0xb8 → 0x98: sample 9 and its two halo dots leave the cell together. On an FMA host with the conversions removed the two agree and
// this fails; here it proves the trap is real and unentered.
func TestBezierAtRefusesTheFusedMultiplyAdd(t *testing.T) {
	at := float64(railFusedSample) / float64(railSteps)
	mt := 1 - at
	// arm64's own contraction of `mt*mt*p0 + 2*mt*t*control + t*t*p1`, read
	// off its assembly: FMADDD folds the first product into the second, then
	// the third into that sum.
	fused := math.FMA(at*at, railColumn, math.FMA(mt*mt, railColumn, 2*mt*at*railColumn))
	rounded := bezierAt(railColumn, railColumn, railColumn, at)
	if int(fused) != railColumns[railFusedSample]+1 {
		t.Fatalf(
			"sample %d no longer distinguishes the two roundings (fused %.17g → column %d, expected column %d);\n"+
				"this test now proves nothing — repick the sample from the rail before trusting it",
			railFusedSample, fused, int(fused), railColumns[railFusedSample]+1,
		)
	}
	if int(rounded) == int(fused) {
		t.Fatalf(
			"bezierAt sample %d gave column %d — the fused-multiply-add value, not the separately rounded %.17g;\n"+
				"the float64() rounding barrier in bezierAt is gone and this machine's goldens will not match an amd64 host's",
			railFusedSample, int(rounded), rounded,
		)
	}
}

// TestBezierAtRoundsEveryProductExplicitly reads bezierAt's own source: the
// canary above can only fail on a machine that actually fuses, so the barrier
// itself is asserted here, where every architecture sees it. Each term of the
// returned sum must be an explicit float64() conversion — that conversion is
// the only thing in the language that forbids the contraction.
func TestBezierAtRoundsEveryProductExplicitly(t *testing.T) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "cosmoscanvas.go", nil, 0)
	if err != nil {
		t.Fatalf("parse cosmoscanvas.go: %v", err)
	}
	var body *ast.ReturnStmt
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "bezierAt" || function.Body == nil {
			continue
		}
		for _, statement := range function.Body.List {
			if returned, ok := statement.(*ast.ReturnStmt); ok && len(returned.Results) == 1 {
				body = returned
			}
		}
	}
	if body == nil {
		t.Fatalf("cosmoscanvas.go has no bezierAt returning one expression — the quadratic Bezier must stay in ONE canonical form")
	}
	var terms []ast.Expr
	var flatten func(ast.Expr)
	flatten = func(expression ast.Expr) {
		binary, ok := expression.(*ast.BinaryExpr)
		if ok && (binary.Op == token.ADD || binary.Op == token.SUB) {
			flatten(binary.X)
			flatten(binary.Y)
			return
		}
		terms = append(terms, expression)
	}
	flatten(body.Results[0])
	if len(terms) != 3 {
		t.Fatalf("bezierAt sums %d terms, want the 3 Bernstein terms", len(terms))
	}
	for index, term := range terms {
		call, ok := term.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			t.Errorf("bezierAt term %d is not a conversion", index)
			continue
		}
		name, ok := call.Fun.(*ast.Ident)
		if !ok || name.Name != "float64" {
			t.Errorf("bezierAt term %d is not wrapped in float64(); without it an FMA architecture may fuse the product into the sum and move a dot one cell", index)
		}
	}
}
