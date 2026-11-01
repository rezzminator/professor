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

// quotaRowPercent sizes every quota row at 86% of its widest sibling (115% x
// 0.75, rounded down).
const quotaRowPercent = 86

// rowColumns is the width a full-width quota row spans: 86% (rounded down) of
// the widest sibling line, never below minColumns (the compact Claude line) or
// the widest label, so every label stays whole; bounded by hook COLUMNS minus the
// 2×2 footer padding. ok=false when the terminal leaves no content column at all.
func rowColumns(runtime Runtime, minColumns int, labelWidths []int, siblingLines ...string) (columns int, ok bool) {
	widest := 0
	for _, line := range siblingLines {
		widest = max(widest, ansi.StringWidth(line))
	}
	columns = max(widest*quotaRowPercent/100, minColumns)
	for _, width := range labelWidths {
		columns = max(columns, width)
	}
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

// compactRow is the label with a fixed 5-cell bar.
func compactRow(row quotaRow) string {
	return barRow(row, ansi.StringWidth(row.label)+1+5)
}

// splitQuotaRow draws the present Claude readings (5h, 7d-fable, 7d; a zero label is
// absent) side by side across exactly columns cells, the separator between them.
// With a 7d reading, 5h and fable take 25% of the cells the separators leave and 7d
// the remainder; without one the readings split equally. A 5h or fable segment never
// draws below its compact width: the cells it lacks come out of 7d, else out of the
// other segment's cells above its own floor. A row the floors overflow is cut to fit.
func splitQuotaRow(five, fable, week quotaRow, columns int) string {
	var rows []quotaRow
	for _, row := range []quotaRow{five, fable, week} {
		if row.label != "" {
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return ""
	}
	total := max(columns-(len(rows)-1)*ansi.StringWidth(sep), 0)
	last := len(rows) - 1
	widths, floors := make([]int, len(rows)), make([]int, len(rows))
	cells := total
	for i, row := range rows {
		if i < last || week.label == "" {
			floors[i] = ansi.StringWidth(compactRow(row))
		}
		if i == last {
			widths[i] = cells
			break
		}
		widths[i] = total / len(rows)
		if week.label != "" {
			widths[i] = total / 4
		}
		cells -= widths[i]
	}
	for i := range rows {
		need := floors[i] - widths[i]
		for j := last; j >= 0 && need > 0; j-- { // 7d, when present, is last and gives first
			if j == i {
				continue
			}
			give := min(need, max(widths[j]-floors[j], 0))
			widths[j], widths[i], need = widths[j]-give, widths[i]+give, need-give
		}
		widths[i] += max(need, 0)
	}
	drawn := make([]string, len(rows))
	for i, row := range rows {
		drawn[i] = barRow(row, widths[i])
	}
	return ansi.Truncate(strings.Join(drawn, sep), columns, "")
}
