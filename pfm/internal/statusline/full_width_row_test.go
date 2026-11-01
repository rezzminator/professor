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

func TestClaudeRowSharesAndFit(t *testing.T) {
	five := quotaRow{percent: 10, hasData: true, label: "5h-used:10%"}        // compact 17
	fable := quotaRow{percent: 20, hasData: true, label: "7d-fable-used:20%"} // compact 23
	week := quotaRow{percent: 30, hasData: true, label: "7d-used:30%"}
	none := quotaRow{}
	widths := func(columns int, rows ...quotaRow) []int {
		var out []int
		for _, part := range strings.Split(ansi.Strip(splitQuotaRow(rows[0], rows[1], rows[2], columns)), " │ ") {
			out = append(out, ansi.StringWidth(part))
		}
		return out
	}
	for _, test := range []struct {
		name    string
		columns int
		rows    []quotaRow
		want    []int
	}{
		{"all three 25/25/50", 114 + 6, []quotaRow{five, fable, week}, []int{28, 28, 58}},
		{"fable+7d 25/75", 80 + 3, []quotaRow{none, fable, week}, []int{23, 57}},
		{"5h+7d 25/75", 80 + 3, []quotaRow{five, none, week}, []int{20, 60}},
		{"7d alone", 70, []quotaRow{none, none, week}, []int{70}},
		{"no 7d splits equally", 60 + 3, []quotaRow{five, fable, none}, []int{30, 30}},
		{"no 7d floor comes out of the other", 40 + 3, []quotaRow{five, fable, none}, []int{17, 23}},
		{"lone 5h spans", 50, []quotaRow{five, none, none}, []int{50}},
		{"floor comes out of 7d", 60 + 6, []quotaRow{five, fable, week}, []int{17, 23, 20}},
	} {
		if got := widths(test.columns, test.rows...); fmt.Sprint(got) != fmt.Sprint(test.want) {
			t.Errorf("%s: widths %v, want %v", test.name, got, test.want)
		}
	}
	for columns := 0; columns <= 120; columns++ {
		for _, rows := range [][]quotaRow{{five, fable, week}, {five, fable, none}, {none, fable, week}} {
			if got := ansi.StringWidth(splitQuotaRow(rows[0], rows[1], rows[2], columns)); got != columns {
				t.Fatalf("splitQuotaRow width %d, want exactly %d", got, columns)
			}
		}
	}
	if got := splitQuotaRow(none, none, none, 40); got != "" {
		t.Fatalf("no readings must draw nothing: %q", got)
	}
}

func TestRowColumnsBoundsByTerminal(t *testing.T) {
	sibling := strings.Repeat("x", 90)
	for _, test := range []struct {
		columns, want int
		ok            bool
	}{{0, 77, true}, {-1, 77, true}, {120, 77, true}, {80, 76, true}, {5, 1, true}, {4, 0, false}, {1, 0, false}} {
		got, ok := rowColumns(Runtime{Columns: test.columns}, 0, []int{10}, "x", sibling)
		if ok != test.ok || ok && got != test.want {
			t.Errorf("columns %d: got (%d, %v), want (%d, %v)", test.columns, got, ok, test.want, test.ok)
		}
	}
}

func TestRowColumnsNeverNarrowerThanCompactLine(t *testing.T) {
	sibling := strings.Repeat("x", 40) // 86% = 34
	for _, test := range []struct{ columns, minimum, want int }{{0, 60, 60}, {0, 30, 34}, {50, 60, 46}} {
		if got, _ := rowColumns(Runtime{Columns: test.columns}, test.minimum, nil, sibling); got != test.want {
			t.Errorf("columns %d min %d: got %d, want %d", test.columns, test.minimum, got, test.want)
		}
	}
}
