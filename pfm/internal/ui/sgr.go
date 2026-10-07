package ui

import (
	"strconv"
	"strings"
)

// A deck frame paints a few thousand coloured runs, and spelling each run's
// SGR sequence as its own strings — three decimal channels per colour, a
// builder per run, the run's rendering, then the line built from those — was
// most of a frame's allocations and so most of its garbage-collection cost.
// The writers here append a run's sequence straight into the caller's line:
// the bytes are exactly tone.render's, without a string per run.

// decimalByte spells every channel value once, so a colour is written without
// formatting a number.
var decimalByte = func() (table [256]string) {
	for value := range table {
		table[value] = strconv.Itoa(value)
	}
	return table
}()

// truecolorOK reports a colour the direct path can spell: empty (no colour) or
// #rrggbb. Anything else — a named colour, say — is lipgloss's to render.
func truecolorOK(hex string) bool {
	if hex == "" {
		return true
	}
	if len(hex) != 7 || hex[0] != '#' {
		return false
	}
	for index := 1; index < len(hex); index++ {
		if _, ok := hexDigit(hex[index]); !ok {
			return false
		}
	}
	return true
}

// writeChannels writes the r;g;b of a #rrggbb colour truecolorOK accepted.
func writeChannels(buf *strings.Builder, hex string) {
	for channel := range 3 {
		high, _ := hexDigit(hex[1+2*channel])
		low, _ := hexDigit(hex[2+2*channel])
		if channel > 0 {
			buf.WriteByte(';')
		}
		buf.WriteString(decimalByte[high<<4|low])
	}
}

// writeSGR writes the opening sequence of a tone whose colours truecolorOK
// accepted, and reports false, writing nothing, for a tone with nothing to set.
// lipgloss writes italic before faint; the same order keeps the two paths
// byte-identical.
func (paint tone) writeSGR(buf *strings.Builder) bool {
	if !paint.bold && !paint.italic && !paint.dim && paint.fg == "" && paint.bg == "" {
		return false
	}
	buf.WriteString("\x1b[")
	first := true
	param := func(code string) {
		if !first {
			buf.WriteByte(';')
		}
		buf.WriteString(code)
		first = false
	}
	if paint.bold {
		param("1")
	}
	if paint.italic {
		param("3")
	}
	if paint.dim {
		param("2")
	}
	if paint.fg != "" {
		param("38;2;")
		writeChannels(buf, paint.fg)
	}
	if paint.bg != "" {
		param("48;2;")
		writeChannels(buf, paint.bg)
	}
	buf.WriteByte('m')
	return true
}

// renderTo appends paint.render(text) to buf.
func (paint tone) renderTo(buf *strings.Builder, text string) {
	if text == "" {
		return
	}
	if !truecolorOK(paint.fg) || !truecolorOK(paint.bg) {
		buf.WriteString(paint.style().Render(text))
		return
	}
	if !paint.writeSGR(buf) {
		buf.WriteString(text)
		return
	}
	buf.WriteString(text)
	buf.WriteString("\x1b[m")
}

// renderBytesTo is renderTo for a run collected as bytes, so its caller need
// not make a string of every run it paints.
func (paint tone) renderBytesTo(buf *strings.Builder, run []byte) {
	if len(run) == 0 {
		return
	}
	if !truecolorOK(paint.fg) || !truecolorOK(paint.bg) {
		buf.WriteString(paint.style().Render(string(run)))
		return
	}
	if !paint.writeSGR(buf) {
		buf.Write(run)
		return
	}
	buf.Write(run)
	buf.WriteString("\x1b[m")
}
