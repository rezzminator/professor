package statusline

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func weeklyRuntime(t *testing.T) Runtime {
	t.Helper()
	root := t.TempDir()
	return Runtime{
		Now:  func() time.Time { return time.Unix(1_786_838_400, 0) },
		Home: root, CacheDir: filepath.Join(root, "cache"),
		RateLimitDir: filepath.Join(root, "rates"), SIDDir: filepath.Join(root, "sid"),
		TmuxDir: filepath.Join(root, "tmux"), ProcRoot: filepath.Join(root, "proc"),
		Columns: 120, UID: 1000, Env: map[string]string{}, Command: quietRunner{},
	}
}

func weeklyCache(t *testing.T, runtime Runtime, body string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(runtime.CacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(runtime.CacheDir, "cc-gpt-usage-1000.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := runtime.now().Add(-age)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func weeklyLines(t *testing.T, runtime Runtime, payload string) []string {
	t.Helper()
	got, err := Render(context.Background(), []byte(payload), runtime)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(got, "\n"), "\n")
}

func assertWeeklySiblingWidth(t *testing.T, lines []string, columns int) {
	t.Helper()
	widest := 0
	for _, line := range lines[:len(lines)-1] {
		widest = max(widest, ansi.StringWidth(line))
	}
	if len(lines) == 3 { // no Claude quota rows above: the weekly row grows past the widest sibling itself
		widest = (widest*quotaRowPercent + 99) / 100
	}
	if columns > 0 {
		widest = min(widest, max(0, columns-4))
	}
	if got := ansi.StringWidth(lines[len(lines)-1]); got != widest {
		t.Fatalf("weekly width %d != bounded sibling width %d: %q", got, widest, lines)
	}
}

func TestClaudeCodexWeeklyMatchesRenderedRows(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
	}{
		{"context longest", `{}`},
		{"quotas longest", `{"rate_limits":{"five_hour":{"used_percentage":4,"resets_at":1786848660},"seven_day":{"used_percentage":84,"resets_at":1787122800},"seven_day_fable":{"used_percentage":0,"resets_at":1787122800}}}`},
		{"identity longest", `{"session_name":"A deliberately long session title with 界面 and emoji 🌳 across the identity row"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := weeklyRuntime(t)
			runtime.Columns = 256
			weeklyCache(t, runtime,
				`{"primary":{"usedPercent":52,"windowDurationMins":10080,"resetsAt":1787122800},"secondary":null}`,
				time.Minute)
			lines := weeklyLines(t, runtime, test.payload)
			widest := 0
			for i, line := range lines[:len(lines)-1] {
				width := ansi.StringWidth(line)
				widest = max(widest, width)
				t.Logf("sibling %d: width=%d %s", i+1, width, ansi.Strip(line))
			}
			if len(lines) == 3 { // no quota rows above: the weekly row grows past the widest sibling itself
				widest = (widest*quotaRowPercent + 99) / 100
			}
			weekly := ansi.Strip(lines[len(lines)-1])
			t.Logf("weekly: width=%d %s", ansi.StringWidth(weekly), weekly)
			if !strings.HasSuffix(weekly, "codex-7d-used:52% ↻3d7h") {
				t.Errorf("weekly row lost complete reset suffix: %q", weekly)
			}
			if got := ansi.StringWidth(weekly); got != widest {
				t.Errorf(
					"weekly content width = %d, want widest sibling %d (terminal %d)",
					got,
					widest,
					runtime.Columns,
				)
			}
		})
	}
}

func TestClaudeCodexWeeklyFitsSiblingWidth(t *testing.T) {
	for _, test := range []struct {
		name    string
		windows string
		columns int
	}{
		{"missing windows", `"five_hour":{"used_percentage":4}`, 120},
		{"all windows", `"five_hour":{"used_percentage":4,"resets_at":1786848660},"seven_day":{"used_percentage":84,"resets_at":1787122800},"seven_day_fable":{"used_percentage":0,"resets_at":1787122800}`, 120},
		{"long resets narrow terminal", `"five_hour":{"used_percentage":100,"resets_at":1786910400},"seven_day":{"used_percentage":100,"resets_at":1789700400},"seven_day_fable":{"used_percentage":100,"resets_at":1789700400}`, 40},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := weeklyRuntime(t)
			runtime.Columns = test.columns
			weeklyCache(
				t,
				runtime,
				`{"primary":{"usedPercent":99,"windowDurationMins":300},"secondary":{"usedPercent":62.6,"windowDurationMins":10080,"resetsAt":1787122800}}`,
				time.Minute,
			)
			lines := weeklyLines(t, runtime, `{"rate_limits":{`+test.windows+`}}`)
			if len(lines) != 5 {
				t.Fatalf(
					"want Codex weekly row directly under the two Claude quota rows (5 lines), got %d: %q",
					len(lines),
					lines,
				)
			}
			claude, codex := styleANSI.ReplaceAllString(lines[2], ""), styleANSI.ReplaceAllString(lines[4], "")
			if !strings.Contains(claude, "5h-used:") || !strings.Contains(codex, "codex-7d-used:63% ↻3d7h") {
				t.Fatalf("quota row order/value/reset mismatch: %q then %q", claude, codex)
			}
			assertWeeklySiblingWidth(t, lines, test.columns)
			cells := strings.Count(codex, "▰") + strings.Count(codex, "▱")
			if cells <= 5 || strings.Count(codex, "▰") != 63*cells/100 {
				t.Fatalf("weekly bar was not sized/proportional: %q", codex)
			}
		})
	}
}

func TestClaudeCodexWeeklyUsesSecondaryColor(t *testing.T) {
	for _, test := range []struct {
		percent float64
		want    int
		color   string
	}{
		{0, 0, green},
		{49, 49, green},
		{50, 50, yellow},
		{79, 79, yellow},
		{80, 80, red},
		{99.9, 99, red},
		{100, 100, red},
	} {
		t.Run(fmt.Sprint(test.percent), func(t *testing.T) {
			runtime := weeklyRuntime(t)
			refreshes := 0
			runtime.Spawn = func(RefreshKind) error { refreshes++; return nil }
			weeklyCache(
				t,
				runtime,
				fmt.Sprintf(
					`{"primary":{"usedPercent":17,"windowDurationMins":300},"secondary":{"usedPercent":%g,"windowDurationMins":10080,"resetsAt":1787122800}}`,
					test.percent,
				),
				time.Minute,
			)
			lines := weeklyLines(t, runtime, `{"rate_limits":{"five_hour":{"used_percentage":4}}}`)
			last := lines[len(lines)-1]
			want := test.color + fmt.Sprintf("codex-7d-used:%d%%", test.want) + reset
			if !strings.Contains(last, want) || !strings.Contains(last, "↻3d7h") ||
				!strings.Contains(last, test.color+"▰") && test.want > 0 {
				t.Fatalf("weekly row lacks secondary percent/reset/color %q: %q", want, last)
			}
			if refreshes != 0 {
				t.Fatalf("fresh weekly cache armed %d refreshes", refreshes)
			}
		})
	}
}

func TestClaudeCodexWeeklyUnavailableAndStale(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      string
		age       time.Duration
		want      string
		refreshes int
		warn      bool
	}{
		{name: "unreadable", want: "codex-7d: error", warn: true},
		{name: "missing", want: "codex-7d: no data", refreshes: 1},
		{name: "no secondary", body: `{"primary":{"usedPercent":17}}`, want: "codex-7d: no data"},
		{name: "malformed", body: `{`, want: "codex-7d: error", warn: true},
		{name: "invalid percent", body: `{"secondary":{"usedPercent":-1,"windowDurationMins":10080,"resetsAt":1787122800}}`, want: "codex-7d: error", warn: true},
		{name: "missing percent", body: `{"secondary":{"windowDurationMins":10080,"resetsAt":1787122800}}`, want: "codex-7d: error", warn: true},
		{name: "null percent", body: `{"secondary":{"usedPercent":null,"windowDurationMins":10080,"resetsAt":1787122800}}`, want: "codex-7d: error", warn: true},
		{name: "short window", body: `{"secondary":{"usedPercent":42,"windowDurationMins":300,"resetsAt":1787122800}}`, want: "codex-7d: no data"},
		{name: "primary invalid percent", body: `{"primary":{"usedPercent":101,"windowDurationMins":10080,"resetsAt":1787122800}}`, want: "codex-7d: error", warn: true},
		{name: "primary missing percent", body: `{"primary":{"windowDurationMins":10080,"resetsAt":1787122800}}`, want: "codex-7d: error", warn: true},
		{name: "primary null percent", body: `{"primary":{"usedPercent":null,"windowDurationMins":10080,"resetsAt":1787122800}}`, want: "codex-7d: error", warn: true},
		{name: "primary invalid reset", body: `{"primary":{"usedPercent":42,"windowDurationMins":10080}}`, want: "codex-7d: error", warn: true},
		{name: "primary stale", body: `{"primary":{"usedPercent":42,"windowDurationMins":10080,"resetsAt":1787122800},"secondary":null}`, age: 2 * time.Hour, want: "codex-7d-used:42% ↻3d7h stale 2h0m", refreshes: 1},
		{name: "stale", body: `{"secondary":{"usedPercent":42,"windowDurationMins":10080,"resetsAt":1787122800}}`, age: 2 * time.Hour, want: "codex-7d-used:42% ↻3d7h stale 2h0m", refreshes: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := weeklyRuntime(t)
			var warnings bytes.Buffer
			runtime.Warn = &warnings
			refreshes := 0
			runtime.Spawn = func(kind RefreshKind) error {
				if kind != RefreshKindCodex {
					t.Fatalf("wrong refresher kind %q", kind)
				}
				refreshes++
				return nil
			}
			if test.name == "unreadable" {
				if err := os.MkdirAll(filepath.Join(runtime.CacheDir, "cc-gpt-usage-1000.json"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if test.body != "" {
				weeklyCache(t, runtime, test.body, test.age)
			}
			lines := weeklyLines(t, runtime, `{"rate_limits":{"five_hour":{"used_percentage":4}}}`)
			last := styleANSI.ReplaceAllString(lines[len(lines)-1], "")
			if !strings.Contains(last, test.want) {
				t.Fatalf("weekly row must expose %q, got %q", test.want, last)
			}
			if len(lines) != 5 {
				t.Fatalf("unavailable/stale weekly row must follow Claude quotas: %q", lines)
			}
			assertWeeklySiblingWidth(t, lines, runtime.Columns)
			if refreshes != test.refreshes {
				t.Fatalf("refreshes=%d, want %d", refreshes, test.refreshes)
			}
			if test.warn && !strings.Contains(warnings.String(), "cc-gpt-usage-1000.json") {
				t.Fatalf("cache failure lacks contextual warning: %q", warnings.String())
			}
		})
	}
}

func TestClaudeCodexWeeklyWithoutClaudeQuotas(t *testing.T) {
	runtime := weeklyRuntime(t)
	lines := weeklyLines(t, runtime, `{}`)
	if len(lines) != 3 || !strings.Contains(lines[2], "codex-7d: no data") {
		t.Fatalf("missing Claude quotas must not hide Codex data status: %q", lines)
	}
	assertWeeklySiblingWidth(t, lines, runtime.Columns)
}

func TestClaudeCodexWeeklySelectsDurationAcrossWindows(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"observed primary only", `{"primary":{"resetsAt":1793089450,"usedPercent":50,"windowDurationMins":10080},"secondary":null}`, "codex-7d-used:50%"},
		{"primary with short secondary", `{"primary":{"resetsAt":1793089450,"usedPercent":50,"windowDurationMins":10080},"secondary":{"resetsAt":1793089450,"usedPercent":99,"windowDurationMins":300}}`, "codex-7d-used:50%"},
		{"secondary with short primary", `{"primary":{"usedPercent":99,"windowDurationMins":300},"secondary":{"resetsAt":1793089450,"usedPercent":50,"windowDurationMins":10080}}`, "codex-7d-used:50%"},
		{"both weekly primary first", `{"primary":{"resetsAt":1793089450,"usedPercent":50,"windowDurationMins":10080},"secondary":{"resetsAt":1793089450,"usedPercent":99,"windowDurationMins":10080}}`, "codex-7d-used:50%"},
		{"only short windows", `{"primary":{"usedPercent":50,"windowDurationMins":300},"secondary":{"usedPercent":99,"windowDurationMins":60}}`, "codex-7d: no data"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := weeklyRuntime(t)
			weeklyCache(t, runtime, test.body, time.Minute)
			lines := weeklyLines(t, runtime, `{}`)
			if len(lines) != 3 || !strings.Contains(lines[2], test.want) {
				t.Fatalf("weekly selection must expose %q without Claude quotas: %q", test.want, lines)
			}
			assertWeeklySiblingWidth(t, lines, runtime.Columns)
		})
	}
}

func TestClaudeCodexWeeklyNarrowWidths(t *testing.T) {
	for _, body := range []string{
		`{"primary":{"usedPercent":50,"windowDurationMins":10080,"resetsAt":1787122800}}`,
		`{`,
		`{}`,
	} {
		for _, columns := range []int{1, 2, 3, 4, 5, 8, 16, 24, 40} {
			t.Run(fmt.Sprintf("%s/%d", body, columns), func(t *testing.T) {
				runtime := weeklyRuntime(t)
				runtime.Columns = columns
				var warnings bytes.Buffer
				runtime.Warn = &warnings
				weeklyCache(t, runtime, body, 2*time.Hour)
				last := weeklyRowAt(runtime, "Claude quota fallback")
				contentColumns := max(0, columns-4)
				if got := ansi.StringWidth(last); got > contentColumns || got == 0 && contentColumns > 0 {
					t.Fatalf("weekly row width %d must fit %d content columns: %q", got, contentColumns, last)
				}
			})
		}
	}
}

func TestClaudeCodexWeeklyNoContentDoesNotAddBlankRow(t *testing.T) {
	for _, columns := range []int{1, 2, 3, 4} {
		for _, payload := range []string{`{}`, `{"rate_limits":{"five_hour":{"used_percentage":4}}}`} {
			t.Run(fmt.Sprintf("%d/%s", columns, payload), func(t *testing.T) {
				runtime := weeklyRuntime(t)
				runtime.Columns = columns
				lines := weeklyLines(t, runtime, payload)
				want := 2
				if payload != `{}` {
					want = 4 // the 5h|fable and 7d rows stay at their compact width; the Codex row has no cell
				}
				if len(lines) != want || ansi.StringWidth(lines[len(lines)-1]) == 0 {
					t.Fatalf("no weekly content must not add a blank row: %q", lines)
				}
			})
		}
	}
}

func TestClaudeCodexWeeklyUnknownWidth(t *testing.T) {
	for _, columns := range []int{0, -1} {
		for _, claudeLine := range []string{"", green + strings.Repeat("quota ", 10) + reset} {
			runtime := weeklyRuntime(t)
			runtime.Columns = columns
			weeklyCache(t, runtime,
				`{"primary":{"usedPercent":52,"windowDurationMins":10080,"resetsAt":1787122800}}`,
				time.Minute)
			line := weeklyRowAt(runtime, claudeLine)
			if !strings.HasSuffix(ansi.Strip(line), "codex-7d-used:52% ↻3d7h") {
				t.Fatalf("unknown width lost reset: %q", line)
			}
			if want := (ansi.StringWidth(claudeLine)*quotaRowPercent + 99) / 100; claudeLine != "" &&
				ansi.StringWidth(line) != want {
				t.Fatalf("unknown width must grow Claude content width without subtracting padding: %q", line)
			}
			if runtime.Columns != columns {
				t.Fatalf("footer adjustment changed raw terminal width: %d", runtime.Columns)
			}
		}
	}
}

func TestClaudeCodexWeeklyLabelFitsShortSiblings(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		age  time.Duration
		want string
	}{
		{"missing", "", 0, "codex-7d: no data"},
		{"fresh", `{"primary":{"usedPercent":52,"windowDurationMins":10080,"resetsAt":1787122800}}`, time.Minute, "codex-7d-used:52% ↻3d7h"},
		{"stale", `{"primary":{"usedPercent":52,"windowDurationMins":10080,"resetsAt":1787122800}}`, time.Hour, "codex-7d-used:52% ↻3d7h stale 1h0m"},
	} {
		for _, columns := range []int{0, -1, ansi.StringWidth(test.want) + 4, 256} {
			t.Run(fmt.Sprintf("%s/%d", test.name, columns), func(t *testing.T) {
				runtime := weeklyRuntime(t)
				runtime.Columns = columns
				if test.body != "" {
					weeklyCache(t, runtime, test.body, test.age)
				}
				line := weeklyRowAt(runtime, green+"界"+reset, "tiny")
				if got := ansi.Strip(line); !strings.HasSuffix(got, test.want) {
					t.Fatalf("short siblings must not clip weekly text when it fits: got %q, want %q", got, test.want)
				}
			})
		}
	}
}

func TestClaudeCodexWeeklyFailedRefreshIsThrottled(t *testing.T) {
	runtime := weeklyRuntime(t)
	now := runtime.now()
	runtime.Now = func() time.Time { return now }
	refreshes := 0
	runtime.Spawn = func(RefreshKind) error {
		refreshes++
		return fmt.Errorf("codex unavailable")
	}
	weeklyLines(t, runtime, `{}`)
	weeklyLines(t, runtime, `{}`)
	if refreshes != 1 {
		t.Fatalf("missing Codex spawned %d refreshes inside TTL", refreshes)
	}
	now = now.Add(6 * time.Minute)
	weeklyLines(t, runtime, `{}`)
	if refreshes != 2 {
		t.Fatalf("failed refresh never retried after TTL: %d", refreshes)
	}
}

// weeklyRowAt draws the Codex weekly row alone at the quota width its siblings
// set, as claudeQuotaRows does; "" when no content column exists.
func weeklyRowAt(runtime Runtime, siblingLines ...string) string {
	row := codexWeeklyRow(runtime, runtime.now())
	columns, ok := rowColumns(runtime, []int{ansi.StringWidth(row.label)}, siblingLines...)
	if !ok {
		return ""
	}
	return barRow(row, columns)
}
