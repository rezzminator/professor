package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// VS Code's integrated terminal (xterm.js, @xterm/addon-webgl ≥ 0.20) draws
// these Unicode ranges itself as "custom glyphs" from a texture atlas instead
// of rasterizing them from the user's font. Under a live TUI repaint the
// atlas path left stale, blank, and ghosted cells on screen (Limits tab,
// 2026-09-11; xterm.js#3303 still open), so pfm renders nothing from them.
// Box Drawing (U+2500–U+257F) is the deliberate exception: it is the borders
// of every panel, it rendered correctly in the same frames that lost the
// block glyphs, and every terminal draws it well — it stays allowed until it
// is seen to misbehave. Font-path substitutes: Geometric Shapes U+25A0–25FF
// (▰▱ bars, ◖◗ caps), Misc Technical U+23BA–23BD scan lines (sparklines).
var webglCustomGlyphRanges = []struct {
	lo, hi rune
	name   string
}{
	{0x2580, 0x259F, "Block Elements"},
	{0x2800, 0x28FF, "Braille"},
	{0xE0A0, 0xE0D4, "Powerline"},
	{0xEE00, 0xEE0B, "Progress"},
	{0xF5D0, 0xF60D, "Git Branch"},
	{0x1FB00, 0x1FBFF, "Legacy Computing"},
}

// Files that BUILD custom-glyph runes arithmetically rather than spelling them
// as literals, so the literal scan cannot see them. Each entry is a known,
// accepted renderer; adding one here is a decision, not a convenience.
var webglComputedGlyphExemptions = map[string]string{
	"internal/ui/cosmoscanvas.go": "the cosmos star field is a braille dot canvas by construction; replacing it is a canvas rewrite, not a glyph swap",
}

func webglCustomGlyphRange(r rune) string {
	for _, rg := range webglCustomGlyphRanges {
		if r >= rg.lo && r <= rg.hi {
			return rg.name
		}
	}
	return ""
}

// TestNoWebGLCustomGlyphsInRenderedOutput walks every non-test Go source in
// the module and reports each string or rune literal carrying a banned code
// point, plus every non-Go host asset (shell, Python) the installer stages,
// and every arithmetic braille base (0x2800) outside the exemption list.
func TestNoWebGLCustomGlyphsInRenderedOutput(t *testing.T) {
	root := moduleRootForGlyphGuard(t)
	var findings []string
	var goFiles, assetFiles int

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".") && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go"):
			goFiles++
			findings = append(findings, scanGoLiterals(t, root, path)...)
			if _, exempt := webglComputedGlyphExemptions[rel]; !exempt {
				findings = append(findings, scanRawText(root, path, "0x2800", "arithmetic braille base")...)
			}
		case strings.HasPrefix(rel, filepath.Join("internal", "installer", "assets")) && !strings.HasSuffix(path, ".md"):
			assetFiles++
			findings = append(findings, scanRawGlyphs(root, path)...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if goFiles == 0 || assetFiles == 0 {
		t.Fatalf("scanner did not run: %d go files, %d asset files under %s", goFiles, assetFiles, root)
	}
	if len(findings) > 0 {
		t.Fatalf("%d WebGL custom-glyph code point(s) in rendered sources (scanned %d Go files, %d assets):\n  %s",
			len(findings), goFiles, assetFiles, strings.Join(findings, "\n  "))
	}
}

func moduleRootForGlyphGuard(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

func scanGoLiterals(t *testing.T, root, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var findings []string
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || (lit.Kind != token.STRING && lit.Kind != token.CHAR) {
			return true
		}
		// strconv.Unquote handles both double-quoted/raw strings and
		// single-quoted rune literals (a rune literal unquotes to its UTF-8).
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatalf("unquote %s at %s: %v", lit.Value, fset.Position(lit.Pos()), err)
		}
		for _, r := range value {
			if name := webglCustomGlyphRange(r); name != "" {
				rel, _ := filepath.Rel(root, path)
				findings = append(
					findings,
					fmt.Sprintf("%s:%d %q %U (%s)", rel, fset.Position(lit.Pos()).Line, r, r, name),
				)
			}
		}
		return true
	})
	return findings
}

func scanRawGlyphs(root, path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("%s: unreadable: %v", path, err)}
	}
	if !utf8.Valid(raw) {
		return nil // binary asset
	}
	var findings []string
	for i, line := range strings.Split(string(raw), "\n") {
		for _, r := range line {
			if name := webglCustomGlyphRange(r); name != "" {
				rel, _ := filepath.Rel(root, path)
				findings = append(findings, fmt.Sprintf("%s:%d %q %U (%s)", rel, i+1, r, r, name))
			}
		}
	}
	return findings
}

func scanRawText(root, path, needle, label string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("%s: unreadable: %v", path, err)}
	}
	var findings []string
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, needle) && !strings.HasPrefix(strings.TrimSpace(line), "//") {
			rel, _ := filepath.Rel(root, path)
			findings = append(findings, fmt.Sprintf("%s:%d %s", rel, i+1, label))
		}
	}
	return findings
}
