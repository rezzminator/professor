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

// compactWidth is a drawn segment's label + space + 5-cell bar.
func compactWidth(segment string) int {
	return ansi.StringWidth(segment) - strings.Count(segment, "▰") - strings.Count(segment, "▱") + 5
}

// quotaParts splits the one Claude row into its 5h, 7d-fable and 7d segments.
func quotaParts(t *testing.T, line string) []string {
	t.Helper()
	parts := strings.Split(ansi.Strip(line), " │ ")
	if len(parts) != 3 {
		t.Fatalf("Claude row must be 5h | 7d-fable | 7d: %q", ansi.Strip(line))
	}
	return parts
}

func TestClaudeQuotaRowsOrderAndSplit(t *testing.T) {
	for _, columns := range []int{120, 80, 0} {
		t.Run(fmt.Sprint(columns), func(t *testing.T) {
			lines := quotaLines(t, columns, quotaPayload())
			if len(lines) != 4 {
				t.Fatalf("want identity, context, one Claude row, codex-7d rows, got %d: %q", len(lines), lines)
			}
			parts, codex := quotaParts(t, lines[2]), ansi.Strip(lines[3])
			if !strings.Contains(parts[0], "5h-used:29% ↻0h39m") ||
				!strings.Contains(parts[1], "7d-fable-used:0% ↻3d3h") ||
				!strings.HasSuffix(strings.TrimSpace(parts[2]), "7d-used:62% ↻3d3h") ||
				strings.Contains(parts[2], "fable") {
				t.Fatalf("Claude row must be 5h | 7d-fable | 7d: %q", parts)
			}
			if !strings.HasSuffix(codex, "codex-7d-used:52% ↻3d7h") {
				t.Fatalf("last row must be codex-7d: %q", codex)
			}
			full := ansi.StringWidth(lines[3])
			if ansi.StringWidth(lines[2]) != full {
				t.Fatalf("widths differ: claude=%d codex=%d", ansi.StringWidth(lines[2]), full)
			}
			if columns > 0 && full > columns-4 {
				t.Fatalf("width %d exceeds %d content columns", full, columns-4)
			}
			cells := full - 6
			for i, part := range parts[:2] {
				floor := compactWidth(part)
				want := max(cells/4, floor)
				if got := ansi.StringWidth(part); got != want {
					t.Fatalf(
						"segment %d is %d cells, want max(25%% of %d, floor %d) = %d: %q",
						i,
						got,
						cells,
						floor,
						want,
						part,
					)
				}
			}
			if got, want := ansi.StringWidth(
				parts[2],
			), cells-ansi.StringWidth(
				parts[0],
			)-ansi.StringWidth(
				parts[1],
			); got != want {
				t.Fatalf("7d takes the remainder: %d, want %d", got, want)
			}
			if bar := strings.Count(parts[2], "▰"); bar != 62*(bar+strings.Count(parts[2], "▱"))/100 {
				t.Fatalf("7d bar not proportional to 62%%: %q", parts[2])
			}
		})
	}
}

func TestClaudeQuotaRowsNarrowTruncate(t *testing.T) {
	for _, columns := range []int{5, 6, 8, 16, 24, 40} {
		t.Run(fmt.Sprint(columns), func(t *testing.T) {
			lines := quotaLines(t, columns, quotaPayload())
			if len(lines) != 4 {
				t.Fatalf("want 4 rows, got %q", lines)
			}
			content := columns - 4
			for _, line := range lines[2:] {
				if got := ansi.StringWidth(line); got != content {
					t.Fatalf("row width %d, want exactly %d content columns: %q", got, content, ansi.Strip(line))
				}
			}
		})
	}
}

func TestClaudeQuotaRowsTinyKeepQuotasDropCodex(t *testing.T) {
	lines := quotaLines(t, 4, quotaPayload())
	if len(lines) != 3 {
		t.Fatalf("no content columns: want identity, context and one compact Claude line only: %q", lines)
	}
	row := ansi.Strip(lines[2])
	five, fable, week := strings.Index(
		row,
		"5h-used:29%",
	), strings.Index(
		row,
		"7d-fable-used:0%",
	), strings.Index(
		row,
		"7d-used:62%",
	)
	if five < 0 || fable < five || week < fable {
		t.Fatalf("compact line must read 5h, 7d-fable, 7d in order: %q", row)
	}
}

func TestClaudeQuotaRowsAbsentReadingsTakeNoPlace(t *testing.T) {
	payload := `{"rate_limits":{"five_hour":{"used_percentage":29,"resets_at":` +
		strconv.FormatInt(
			quotaNow.Add(39*time.Minute).Unix(),
			10,
		) + `},"seven_day":{"used_percentage":62,"resets_at":` +
		strconv.FormatInt(quotaNow.Add(24*time.Hour).Unix(), 10) + `},"limits":[]}}`
	runtime := fableRuntime(t.TempDir(), 2, quotaNow)
	runtime.Columns = 120
	weeklyCache(t, runtime, quotaCodexCache, time.Minute)
	lines := renderQuotaLines(t, runtime, payload)
	if len(lines) != 4 {
		t.Fatalf("want one Claude row and codex: %q", lines)
	}
	row := ansi.Strip(lines[2])
	left, right, found := strings.Cut(row, " │ ")
	if !found || strings.Contains(row, "fable") || !strings.Contains(left, "5h-used:29%") ||
		!strings.HasSuffix(right, "7d-used:62% ↻1d0h") {
		t.Fatalf("5h and 7d only: %q", row)
	}
	if cells := ansi.StringWidth(row) - 3; ansi.StringWidth(left) != max(cells/4, compactWidth(left)) {
		t.Fatalf("5h takes 25%% of %d cells: %q", cells, left)
	}
}

func TestClaudeQuotaRowsStates(t *testing.T) {
	// The harness reports Fable alone, so the usage cache decides the plain
	// windows' state.
	week := quotaNow.Add(24 * time.Hour).UTC().Format(time.RFC3339)
	unreported := `{"rate_limits":{"seven_day_fable":{"used_percentage":0,"resets_at":` +
		strconv.FormatInt(quotaNow.Add(24*time.Hour).Unix(), 10) + `}}}`
	carriedAll := usagehook.Usage{
		FiveHour: usagehook.Window{Utilization: floatPtr(80), ResetsAt: week},
		SevenDay: usagehook.Window{Utilization: floatPtr(80), ResetsAt: week},
		Limits:   []usagehook.ScopedLimit{fableScopedLimit(80, week, true)},
	}
	check := func(t *testing.T, lines []string, wantWeek string) {
		t.Helper()
		if len(lines) != 4 {
			t.Fatalf("want 4 rows: %q", lines)
		}
		weekRow := quotaParts(t, lines[2])[2]
		if !strings.HasSuffix(weekRow, wantWeek) {
			t.Fatalf("7d segment %q lacks %q", weekRow, wantWeek)
		}
		if strings.Contains(weekRow, "▰") {
			t.Fatalf("a no-data 7d bar must be empty: %q", lines[2])
		}
		if ansi.StringWidth(lines[2]) != ansi.StringWidth(lines[3]) {
			t.Fatalf("widths differ in state row set: %q", lines)
		}
	}
	t.Run("missing", func(t *testing.T) {
		lines := quotaLines(t, 120, unreported)
		check(t, lines, "7d-used:—")
		if !strings.Contains(ansi.Strip(lines[2]), "5h-used:—") {
			t.Fatalf("an unanswered 5h segment must keep its missing marker: %q", lines[2])
		}
		if !strings.Contains(lines[2], dim+"7d-used:—"+reset) {
			t.Fatalf("missing label must stay dim: %q", lines[2])
		}
	})
	t.Run("error", func(t *testing.T) {
		runtime := fableRuntime(t.TempDir(), 2, quotaNow)
		weeklyCache(t, runtime, quotaCodexCache, time.Minute)
		writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
			Usage:     carriedAll,
			Backoff:   &usagehook.CacheBackoff{Message: "rate limited", RetryAfter: quotaNow.Add(time.Hour)},
			ConfigDir: runtime.ConfigDir,
			FetchedAt: &quotaNow,
		})
		lines := renderQuotaLines(t, runtime, unreported)
		check(t, lines, "7d-used:ERR")
		if !strings.Contains(lines[2], red+"7d-used:ERR"+reset) {
			t.Fatalf("ERR label must stay red: %q", lines[2])
		}
	})
	t.Run("stale cache stays missing", func(t *testing.T) {
		runtime := fableRuntime(t.TempDir(), 2, quotaNow)
		weeklyCache(t, runtime, quotaCodexCache, time.Minute)
		stale := quotaNow.Add(-2 * time.Hour)
		writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
			Usage:     carriedAll,
			ConfigDir: runtime.ConfigDir,
			FetchedAt: &stale,
		})
		lines := renderQuotaLines(t, runtime, unreported)
		check(t, lines, "7d-used:—")
		if strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "80%") {
			t.Fatalf("a stale cache must not surface its value: %q", lines)
		}
	})
}

// TestClaudeQuotaRowsShareOneRightEdge pins the owner's acceptance: whatever the
// label lengths, percentages, countdowns and stale suffix, the Claude
// row and the codex-7d row end at the same column; the bars absorb the
// difference, the 7d segment taking whatever the 25% shares leave.
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
					if len(lines) != 4 {
						t.Fatalf("want 4 rows: %q", lines)
					}
					widths := []int{ansi.StringWidth(lines[2]), ansi.StringWidth(lines[3])}
					if widths[0] != widths[1] {
						t.Fatalf("right edges differ %v:\n%s\n%s", widths, ansi.Strip(lines[2]), ansi.Strip(lines[3]))
					}
					if age > time.Hour && !strings.Contains(ansi.Strip(lines[3]), "stale 2h0m") {
						t.Fatalf("stale suffix lost: %q", ansi.Strip(lines[3]))
					}
				})
			}
		}
	}
}

// TestClaudeQuotaRowsShrinkBelowWidestSibling pins the 86% width: with room the
// shared width is floor(0.86 x the widest sibling) but never below the compact
// Claude line, without room the terminal cap.
func TestClaudeQuotaRowsShrinkBelowWidestSibling(t *testing.T) {
	for _, name := range []string{strings.Repeat("x", 120), "x"} {
		plain := func(columns int) []string {
			runtime := fableRuntime(t.TempDir(), 2, quotaNow)
			runtime.Columns = columns
			weeklyCache(t, runtime, quotaCodexCache, time.Minute)
			payload := strings.Replace(
				quotaPayload(),
				"A long enough session title to widen the identity row past the quota labels",
				name,
				1,
			)
			return renderQuotaLines(t, runtime, payload)
		}
		lines := plain(0)
		compact := ansi.StringWidth(plain(4)[2])
		widest := max(ansi.StringWidth(lines[0]), ansi.StringWidth(lines[1]))
		want := max(widest*86/100, compact)
		if (len(name) > 1) != (widest*86/100 > compact) {
			t.Fatalf(
				"test needs the %d-char name to make %d (86%%) vs %d (compact) the dominant width",
				len(name),
				widest*86/100,
				compact,
			)
		}
		for _, line := range lines[2:] {
			if got := ansi.StringWidth(line); got != want {
				t.Fatalf("row width %d, want max(floor(0.86 x %d), compact %d) = %d", got, widest, compact, want)
			}
		}
		capped := plain(want) // content columns = want-4 < want
		for _, line := range capped[2:] {
			if got := ansi.StringWidth(line); got != want-4 {
				t.Fatalf("row width %d, want the terminal cap %d", got, want-4)
			}
		}
	}
}
