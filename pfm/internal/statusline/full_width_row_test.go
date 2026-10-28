package statusline

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestBarRowFillsExactlyColumns(t *testing.T) {
	row := quotaRow{percent: 50, hasData: true, label: green + "7d-used:50%" + reset} // 11 cells
	nodata := quotaRow{label: dim + "7d-used:—" + reset}
	for _, test := range []struct {
		row     quotaRow
		columns int
		want    int
	}{
		{row, 60, 60},
		{row, 13, 13},
		{row, 12, 12},
		{row, 6, 6},
		{row, 0, 0},
		{row, -3, 0},
		{nodata, 40, 40},
		{nodata, 4, 4},
	} {
		if got := ansi.StringWidth(barRow(test.row, test.columns)); got != test.want {
			t.Errorf("barRow(%q, %d) width %d, want %d", ansi.Strip(test.row.label), test.columns, got, test.want)
		}
	}
	if got := ansi.Strip(barRow(nodata, 20)); got != "▱▱▱▱▱▱▱▱▱▱ 7d-used:—" {
		t.Fatalf("no-data bar must be empty: %q", got)
	}
}

func TestHalfRowsSplitEvenlyAndFit(t *testing.T) {
	left := quotaRow{percent: 10, hasData: true, label: "5h-used:10%"}
	right := quotaRow{percent: 20, hasData: true, label: "7d-fable-used:20%"}
	for columns := 0; columns <= 60; columns++ {
		t.Run(fmt.Sprint(columns), func(t *testing.T) {
			got := ansi.StringWidth(halfRows(left, right, columns))
			if got != columns && columns >= 5 || got > columns {
				t.Fatalf("halfRows width %d, want %d", got, columns)
			}
		})
	}
}

func TestRowColumnsBoundsByTerminal(t *testing.T) {
	sibling := strings.Repeat("x", 90)
	for _, test := range []struct {
		columns, want int
		ok            bool
	}{{0, 104, true}, {-1, 104, true}, {120, 104, true}, {80, 76, true}, {5, 1, true}, {4, 0, false}, {1, 0, false}} {
		got, ok := rowColumns(Runtime{Columns: test.columns}, []int{10}, "x", sibling)
		if ok != test.ok || ok && got != test.want {
			t.Errorf("columns %d: got (%d, %v), want (%d, %v)", test.columns, got, ok, test.want, test.ok)
		}
	}
}
