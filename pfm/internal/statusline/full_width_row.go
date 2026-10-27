package statusline

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// quotaRow is one quota reading ready to draw as a labelled bar: percent drives
// the fill, hasData=false draws an empty dim bar (nothing measured), and label
// is the styled text beside it.
type quotaRow struct {
	percent int
	hasData bool
	label   string
}

// quotaRowPercent stretches every quota row to 115% of its widest sibling, so
// the bars have room beyond the longest line above them.
const quotaRowPercent = 115

// rowColumns is the width a full-width quota row spans: 115% (rounded up) of
// the widest of the labels and sibling lines, bounded by hook COLUMNS minus the
// 2×2 footer padding. ok=false when the terminal leaves no content column at all.
func rowColumns(runtime Runtime, labelWidths []int, siblingLines ...string) (columns int, ok bool) {
	for _, width := range labelWidths {
		columns = max(columns, width)
	}
	for _, line := range siblingLines {
		columns = max(columns, ansi.StringWidth(line))
	}
	columns = (columns*quotaRowPercent + 99) / 100
	if runtime.Columns > 0 {
		contentColumns := runtime.Columns - 2*2 // Claude's outer footer padding, left and right.
		if contentColumns <= 0 {
			return 0, false
		}
		columns = min(columns, contentColumns)
	}
	return columns, true
}

// barRow draws row in exactly columns cells: the bar takes whatever the label
// leaves; a label that leaves no cell for a bar is truncated (or padded by the
// one cell the bar would have needed), so every row still ends on its column.
func barRow(row quotaRow, columns int) string {
	columns = max(columns, 0)
	width := columns - ansi.StringWidth(row.label) - 1
	if width <= 0 {
		label := ansi.Truncate(row.label, columns, "")
		return label + strings.Repeat(" ", columns-ansi.StringWidth(label))
	}
	bar := makeBar(row.percent, width)
	if !row.hasData {
		bar = dim + strings.Repeat("▱", width) + reset
	}
	return bar + " " + row.label
}

// fullWidthRow draws row at the shared full-row width; "" when no content column exists.
func fullWidthRow(runtime Runtime, row quotaRow, siblingLines ...string) string {
	columns, ok := rowColumns(runtime, []int{ansi.StringWidth(row.label)}, siblingLines...)
	if !ok {
		return ""
	}
	return barRow(row, columns)
}

// compactRow is the label with a fixed 5-cell bar.
func compactRow(row quotaRow) string {
	return barRow(row, ansi.StringWidth(row.label)+1+5)
}

// halfRows draws left | right across exactly columns cells, each half half the
// row, the separator between them; a row narrower than the separator is cut to fit.
func halfRows(left, right quotaRow, columns int) string {
	halves := max(columns-ansi.StringWidth(sep), 0)
	return ansi.Truncate(barRow(left, halves/2)+sep+barRow(right, halves-halves/2), columns, "")
}
