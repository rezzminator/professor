package statusline

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

var quotaNow = time.Unix(1_786_838_400, 0)

const quotaCodexCache = `{"primary":{"usedPercent":52,"windowDurationMins":10080,"resetsAt":1787122800}}`

// quotaPayload carries all three Claude windows: 5h 29% (39m left), 7d 62%
// (3d3h left), 7d-fable 0% (3d3h left).
func quotaPayload() string {
	reset := func(d time.Duration) string { return strconv.FormatInt(quotaNow.Add(d).Unix(), 10) }
	week := 3*24*time.Hour + 3*time.Hour + 5*time.Minute
	return `{"session_name":"A long enough session title to widen the identity row past the quota labels",` +
		`"rate_limits":{"five_hour":{"used_percentage":29,"resets_at":` + reset(39*time.Minute+30*time.Second) + `},` +
		`"seven_day":{"used_percentage":62,"resets_at":` + reset(week) + `},` +
		`"limits":[{"kind":"weekly_scoped","percent":0,"resets_at":"` + quotaNow.Add(week).UTC().Format(time.RFC3339) +
		`","scope":{"model":{"display_name":"Fable"}},"is_active":true}]}}`
}

func quotaLines(t *testing.T, columns int, payload string) []string {
	t.Helper()
	runtime := fableRuntime(t.TempDir(), 2, quotaNow)
	runtime.Columns = columns
	weeklyCache(t, runtime, quotaCodexCache, time.Minute)
	return renderQuotaLines(t, runtime, payload)
}

func renderQuotaLines(t *testing.T, runtime Runtime, payload string) []string {
	t.Helper()
	got, err := Render(context.Background(), []byte(payload), runtime)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(got, "\n"), "\n")
}

func TestClaudeQuotaRowsOrderAndSplit(t *testing.T) {
	for _, columns := range []int{120, 80, 0} {
		t.Run(fmt.Sprint(columns), func(t *testing.T) {
			lines := quotaLines(t, columns, quotaPayload())
			if len(lines) != 5 {
				t.Fatalf("want identity, context, 5h|fable, 7d, codex-7d rows, got %d: %q", len(lines), lines)
			}
			top, week, codex := ansi.Strip(lines[2]), ansi.Strip(lines[3]), ansi.Strip(lines[4])
			left, right, found := strings.Cut(top, " │ ")
			if !found || !strings.Contains(left, "5h-used:29% ↻0h39m") || strings.Contains(left, "7d") ||
				!strings.Contains(right, "7d-fable-used:0% ↻3d3h") || strings.Contains(top, "7d-used") {
				t.Fatalf("top row must be 5h | 7d-fable: %q", top)
			}
			if !strings.HasSuffix(week, "7d-used:62% ↻3d3h") || strings.Contains(week, "5h") ||
				strings.Contains(week, "│") {
				t.Fatalf("second row must be the lone 7d: %q", week)
			}
			if !strings.HasSuffix(codex, "codex-7d-used:52% ↻3d7h") {
				t.Fatalf("third row must be codex-7d: %q", codex)
			}
			full := ansi.StringWidth(lines[4])
			if ansi.StringWidth(lines[2]) != full || ansi.StringWidth(lines[3]) != full {
				t.Fatalf("widths differ: 5h|fable=%d 7d=%d codex=%d", ansi.StringWidth(lines[2]),
					ansi.StringWidth(lines[3]), full)
			}
			if columns > 0 && full > columns-4 {
				t.Fatalf("width %d exceeds %d content columns", full, columns-4)
			}
			wantLeft := (full - 3) / 2
			if got := ansi.StringWidth(left); got != wantLeft || ansi.StringWidth(right) != full-3-wantLeft {
				t.Fatalf("halves %d+%d, want %d+%d around a 3-cell separator", got, ansi.StringWidth(right),
					wantLeft, full-3-wantLeft)
			}
			for _, label := range []string{"5h-used", "7d-fable-used"} {
				half := left
				if label != "5h-used" {
					half = right
				}
				if cells := strings.Count(half, "▰") + strings.Count(half, "▱"); cells <= 5 {
					t.Fatalf("%s bar was not stretched: %q", label, half)
				}
			}
			if cells := strings.Count(week, "▰"); cells != 62*(strings.Count(week, "▰")+strings.Count(week, "▱"))/100 {
				t.Fatalf("7d bar not proportional to 62%%: %q", week)
			}
		})
	}
}

func TestClaudeQuotaRowsNarrowTruncate(t *testing.T) {
	for _, columns := range []int{5, 6, 8, 16, 24, 40} {
		t.Run(fmt.Sprint(columns), func(t *testing.T) {
			lines := quotaLines(t, columns, quotaPayload())
			if len(lines) != 5 {
				t.Fatalf("want 5 rows, got %q", lines)
			}
			content := columns - 4
			for _, line := range lines[2:] {
				if got := ansi.StringWidth(line); got != content {
					t.Fatalf("row width %d, want exactly %d content columns: %q", got, content, ansi.Strip(line))
				}
			}
			if columns == 16 {
				if week := ansi.Strip(lines[3]); week != "7d-used:62% " {
					t.Fatalf("7d label must truncate like the codex row, got %q", week)
				}
			}
		})
	}
}

func TestClaudeQuotaRowsTinyKeepQuotasDropCodex(t *testing.T) {
	lines := quotaLines(t, 4, quotaPayload())
	if len(lines) != 4 || !strings.Contains(ansi.Strip(lines[2]), "5h-used:29%") ||
		!strings.Contains(ansi.Strip(lines[3]), "7d-used:62%") {
		t.Fatalf("no content columns: want identity, context and both quota rows only: %q", lines)
	}
}

func TestClaudeQuotaRowsStates(t *testing.T) {
	fiveOnly := `{"rate_limits":{"five_hour":{"used_percentage":29,"resets_at":` +
		strconv.FormatInt(quotaNow.Add(time.Hour).Unix(), 10) + `}}}`
	check := func(t *testing.T, lines []string, wantWeek string) {
		t.Helper()
		if len(lines) != 5 {
			t.Fatalf("want 5 rows: %q", lines)
		}
		weekRow := ansi.Strip(lines[3])
		if !strings.HasSuffix(weekRow, wantWeek) {
			t.Fatalf("7d row %q lacks %q", weekRow, wantWeek)
		}
		if strings.Contains(lines[3], "▰") {
			t.Fatalf("a no-data 7d bar must be empty: %q", lines[3])
		}
		if ansi.StringWidth(lines[3]) != ansi.StringWidth(lines[4]) ||
			ansi.StringWidth(lines[2]) != ansi.StringWidth(lines[4]) {
			t.Fatalf("widths differ in state row set: %q", lines)
		}
	}
	t.Run("missing", func(t *testing.T) {
		lines := quotaLines(t, 120, fiveOnly)
		check(t, lines, "7d-used:—")
		if !strings.Contains(ansi.Strip(lines[2]), "7d-fable-used:—") {
			t.Fatalf("fable half must keep its missing marker: %q", lines[2])
		}
		if !strings.Contains(lines[3], dim+"7d-used:—"+reset) {
			t.Fatalf("missing label must stay dim: %q", lines[3])
		}
	})
	t.Run("error", func(t *testing.T) {
		runtime := fableRuntime(t.TempDir(), 2, quotaNow)
		weeklyCache(t, runtime, quotaCodexCache, time.Minute)
		writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
			Backoff:   &usagehook.CacheBackoff{Message: "rate limited", RetryAfter: quotaNow.Add(time.Hour)},
			ConfigDir: runtime.ConfigDir,
			FetchedAt: &quotaNow,
		})
		lines := renderQuotaLines(t, runtime, fiveOnly)
		check(t, lines, "7d-used:ERR")
		if !strings.Contains(lines[3], red+"7d-used:ERR"+reset) {
			t.Fatalf("ERR label must stay red: %q", lines[3])
		}
	})
	t.Run("stale cache stays missing", func(t *testing.T) {
		runtime := fableRuntime(t.TempDir(), 2, quotaNow)
		weeklyCache(t, runtime, quotaCodexCache, time.Minute)
		stale := quotaNow.Add(-2 * time.Hour)
		writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
			Usage: usagehook.Usage{SevenDay: usagehook.Window{
				Utilization: floatPtr(80), ResetsAt: quotaNow.Add(24 * time.Hour).UTC().Format(time.RFC3339),
			}},
			ConfigDir: runtime.ConfigDir,
			FetchedAt: &stale,
		})
		lines := renderQuotaLines(t, runtime, fiveOnly)
		check(t, lines, "7d-used:—")
		if strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "80%") {
			t.Fatalf("a stale cache must not surface its value: %q", lines)
		}
	})
}

// TestClaudeQuotaRowsShareOneRightEdge pins the owner's acceptance: whatever the
// label lengths, percentages, countdowns and stale suffix, the 5h|fable row, the
// 7d row and the codex-7d row end at the same column; the bars absorb the
// difference, an odd width giving its extra cell to the right half.
func TestClaudeQuotaRowsShareOneRightEdge(t *testing.T) {
	reset := func(d time.Duration) string { return strconv.FormatInt(quotaNow.Add(d).Unix(), 10) }
	for _, test := range []struct{ name, five, seven, fable, codex string }{
		{"short vs long labels", "0", "100", "100", `{"primary":{"usedPercent":3,"windowDurationMins":10080,"resetsAt":1787122800}}`},
		{"full long", "100", "5", "0", `{"primary":{"usedPercent":100,"windowDurationMins":10080,"resetsAt":1787122800}}`},
	} {
		for _, age := range []time.Duration{time.Minute, 2 * time.Hour} { // fresh, then a stale suffix on the codex row
			for _, columns := range []int{120, 80, 0} {
				t.Run(fmt.Sprintf("%s/%v/%d", test.name, age, columns), func(t *testing.T) {
					runtime := fableRuntime(t.TempDir(), 2, quotaNow)
					runtime.Columns = columns
					weeklyCache(t, runtime, test.codex, age)
					payload := `{"session_name":"A session title long and odd enough to widen the identity row ok",` +
						`"rate_limits":{"five_hour":{"used_percentage":` + test.five + `,"resets_at":` + reset(39*time.Minute) + `},` +
						`"seven_day":{"used_percentage":` + test.seven + `,"resets_at":` + reset(6*24*time.Hour+23*time.Hour) + `},` +
						`"limits":[{"kind":"weekly_scoped","percent":` + test.fable + `,"resets_at":"` +
						quotaNow.Add(3*24*time.Hour).UTC().Format(time.RFC3339) +
						`","scope":{"model":{"display_name":"Fable"}},"is_active":true}]}}`
					lines := renderQuotaLines(t, runtime, payload)
					if len(lines) != 5 {
						t.Fatalf("want 5 rows: %q", lines)
					}
					widths := []int{ansi.StringWidth(lines[2]), ansi.StringWidth(lines[3]), ansi.StringWidth(lines[4])}
					if widths[0] != widths[1] || widths[1] != widths[2] {
						t.Fatalf("right edges differ %v:\n%s\n%s\n%s", widths, ansi.Strip(lines[2]),
							ansi.Strip(lines[3]), ansi.Strip(lines[4]))
					}
					if age > time.Hour && !strings.Contains(ansi.Strip(lines[4]), "stale 2h0m") {
						t.Fatalf("stale suffix lost: %q", ansi.Strip(lines[4]))
					}
				})
			}
		}
	}
}

// TestClaudeQuotaRowsGrowPastWidestSibling pins the 115% stretch: with room the
// shared width is ceil(1.15 x the widest sibling), without it the terminal cap.
func TestClaudeQuotaRowsGrowPastWidestSibling(t *testing.T) {
	plain := func(columns int) []string {
		runtime := fableRuntime(t.TempDir(), 2, quotaNow)
		runtime.Columns = columns
		weeklyCache(t, runtime, quotaCodexCache, time.Minute)
		return renderQuotaLines(t, runtime, strings.Replace(quotaPayload(), "widen", "wide", 1))
	}
	lines := plain(0)
	widest := max(ansi.StringWidth(lines[0]), ansi.StringWidth(lines[1]))
	want := (widest*115 + 99) / 100
	if widest*115%100 == 0 || want <= widest {
		t.Fatalf("test needs a width that rounds up: widest %d", widest)
	}
	for _, line := range lines[2:] {
		if got := ansi.StringWidth(line); got != want {
			t.Fatalf("row width %d, want ceil(1.15 x %d) = %d", got, widest, want)
		}
	}
	capped := plain(want) // content columns = want-4 < want
	for _, line := range capped[2:] {
		if got := ansi.StringWidth(line); got != want-4 {
			t.Fatalf("row width %d, want the terminal cap %d", got, want-4)
		}
	}
}
