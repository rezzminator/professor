package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type RGB struct{ R, G, B uint8 }

func lerpRGB(a, b RGB, t float64) RGB {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return RGB{
		R: uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G: uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B: uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
	}
}

func scaleRGB(c RGB, f float64) RGB {
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return RGB{uint8(float64(c.R) * f), uint8(float64(c.G) * f), uint8(float64(c.B) * f)}
}

func luma(c RGB) float64 {
	return 0.30*float64(c.R) + 0.59*float64(c.G) + 0.11*float64(c.B)
}

var brailleBits = [2][4]uint8{
	{0x01, 0x02, 0x04, 0x40},
	{0x08, 0x10, 0x20, 0x80},
}

type cell struct {
	ch   rune
	fg   RGB
	bold bool
	used bool
}

// Canvas is one frame. Text cells always win over braille when rendered, so
// labels remain readable on top of line art.
type Canvas struct {
	Cols, Rows int
	// Quant is the colour quantisation step applied at the render boundary —
	// 8 by default; vscode-safe mode widens it to 16 to cut the distinct
	// (glyph, colour) pairs feeding VS Code's shared WebGL glyph atlas
	// (microsoft/vscode#332859). Pressure relief, not a proven cure — see
	// cosmosTickInterval's honesty note.
	Quant    uint16
	cells    []cell
	dots     []uint8
	dotColor []RGB
	dotLuma  []float64
}

func NewCanvas(cols, rows int) *Canvas {
	c := &Canvas{Cols: cols, Rows: rows, Quant: 8}
	n := cols * rows
	c.cells = make([]cell, n)
	c.dots = make([]uint8, n)
	c.dotColor = make([]RGB, n)
	c.dotLuma = make([]float64, n)
	return c
}

func (c *Canvas) Clear() {
	for i := range c.cells {
		c.cells[i] = cell{}
		c.dots[i] = 0
		c.dotColor[i] = RGB{}
		c.dotLuma[i] = 0
	}
}

func (c *Canvas) PW() int { return c.Cols * 2 }
func (c *Canvas) PH() int { return c.Rows * 4 }

func (c *Canvas) SetCell(x, y int, ch rune, fg RGB, bold bool) {
	if x < 0 || y < 0 || x >= c.Cols || y >= c.Rows {
		return
	}
	c.cells[y*c.Cols+x] = cell{ch: ch, fg: fg, bold: bold, used: true}
}

func (c *Canvas) Text(x, y int, s string, fg RGB, bold bool) {
	col := x
	for _, r := range s {
		c.SetCell(col, y, r, fg, bold)
		col++
	}
}

// TextFree reports whether the n cells starting at (x, y) hold no text yet —
// braille dots do not count, only glyph and label cells — so a label can
// look for a row where it will not print over another label. Cells outside
// the canvas count as free: clipping is the caller's business, collision is
// this one's.
func (c *Canvas) TextFree(x, y, n int) bool {
	if y < 0 || y >= c.Rows {
		return true
	}
	for i := 0; i < n; i++ {
		col := x + i
		if col < 0 || col >= c.Cols {
			continue
		}
		if c.cells[y*c.Cols+col].used {
			return false
		}
	}
	return true
}

func (c *Canvas) Dot(px, py int, color RGB) {
	if px < 0 || py < 0 || px >= c.PW() || py >= c.PH() {
		return
	}
	idx := (py/4)*c.Cols + px/2
	c.dots[idx] |= brailleBits[px%2][py%4]
	if l := luma(color); l >= c.dotLuma[idx] {
		c.dotLuma[idx] = l
		c.dotColor[idx] = color
	}
}

func (c *Canvas) Bezier(x0, y0, cx, cy, x1, y1 float64, from, to RGB, fade float64, dashed bool) {
	dist := math.Hypot(x1-x0, y1-y0) + math.Hypot(cx-x0, cy-y0)
	steps := int(dist * 1.2)
	if steps < 8 {
		steps = 8
	}
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		if dashed && (i/5)%2 == 1 {
			continue
		}
		c.Dot(
			int(bezierAt(x0, cx, x1, t)),
			int(bezierAt(y0, cy, y1, t)),
			scaleRGB(lerpRGB(from, to, t), fade),
		)
	}
}

func BezierPoint(x0, y0, cx, cy, x1, y1, t float64) (float64, float64) {
	return bezierAt(x0, cx, x1, t), bezierAt(y0, cy, y1, t)
}

// bezierAt evaluates ONE axis of the quadratic Bezier at t — the single
// canonical form both Bezier and BezierPoint sample, so a rail and the comet
// riding it can never drift apart.
//
// The three float64() conversions are load-bearing, not decoration. The Go
// spec (Floating-point operators) lets an implementation contract `a*b + c`
// into one fused multiply-add rounded ONCE, and every FMA architecture takes
// it: for this exact expression the arm64 compiler emits FMADDD twice
// (verified from `GOARCH=arm64 go build -gcflags=-S ./internal/ui`), while
// amd64 has no contraction rule at all. An explicit conversion to float64
// rounds the product to float64 precision and forbids the fusion, so the
// value is the same on every CPU.
//
// It matters because the result is truncated to a pixel. The cosmos focus
// golden draws a dead-straight rail down pixel column 78 whose 22 samples are
// all mathematically exactly 78; with the products rounded separately, two of
// them land on 77.999999999999986 and int() drops those to column 77 — that
// pair of dots, with the halo rows a hot rail carries, IS the ⢸ (U+28B8)
// glyph the golden holds. Fused, sample 9 rounds to exactly 78, its dots
// leave that cell, and the glyph becomes ⠘ (U+2818): one byte, 0xb8 → 0x98.
// Unpinned, these goldens pin the CPU that generated them instead of the code.
func bezierAt(p0, control, p1, t float64) float64 {
	mt := 1 - t
	return float64(mt*mt*p0) + float64(2*mt*t*control) + float64(t*t*p1)
}

func quantRGB(c RGB, step uint16) RGB {
	if step == 0 {
		step = 8
	}
	q := func(v uint8) uint8 {
		r := (uint16(v) + step/2) / step * step
		if r > 255 {
			r = 255
		}
		return uint8(r)
	}
	return RGB{q(c.R), q(c.G), q(c.B)}
}

// render returns a complete newline-joined frame. Bubble Tea performs the
// differential paint, so this boundary deliberately emits no cursor moves.
func (c *Canvas) render() string {
	var b strings.Builder
	for y := 0; y < c.Rows; y++ {
		last := RGB{1, 1, 1}
		lastBold := false
		styleValid := false
		for x := 0; x < c.Cols; x++ {
			idx := y*c.Cols + x
			ch := ' '
			fg := RGB{}
			bold := false
			switch {
			case c.cells[idx].used:
				ch, fg, bold = c.cells[idx].ch, quantRGB(c.cells[idx].fg, c.Quant), c.cells[idx].bold
			case c.dots[idx] != 0:
				ch, fg = rune(0x2800+int(c.dots[idx])), quantRGB(c.dotColor[idx], c.Quant)
			}
			if ch != ' ' && (!styleValid || fg != last || bold != lastBold) {
				if bold {
					fmt.Fprintf(&b, "\x1b[1;38;2;%d;%d;%dm", fg.R, fg.G, fg.B)
				} else {
					fmt.Fprintf(&b, "\x1b[0;38;2;%d;%d;%dm", fg.R, fg.G, fg.B)
				}
				last, lastBold, styleValid = fg, bold, true
			}
			b.WriteRune(ch)
		}
		b.WriteString("\x1b[0m")
		if y+1 < c.Rows {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (c *Canvas) DimAll(f float64) {
	for i := range c.cells {
		if c.cells[i].used {
			c.cells[i].fg = scaleRGB(c.cells[i].fg, f)
			c.cells[i].bold = false
		}
		if c.dots[i] != 0 {
			c.dotColor[i] = scaleRGB(c.dotColor[i], f)
		}
	}
}

func rgbFromHex(value string) RGB {
	if len(value) != 7 || value[0] != '#' {
		return RGB{160, 160, 160}
	}
	parsed, err := strconv.ParseUint(value[1:], 16, 24)
	if err != nil {
		return RGB{160, 160, 160}
	}
	return RGB{uint8(parsed >> 16), uint8(parsed >> 8), uint8(parsed)}
}
