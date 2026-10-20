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
	"unicode/utf8"
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

func TestClaudeCodexWeeklyMatchesQuotaWidths(t *testing.T) {
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
			if len(lines) != 4 {
				t.Fatalf("want Codex weekly row directly under Claude quotas (4 lines), got %d: %q", len(lines), lines)
			}
			claude, codex := styleANSI.ReplaceAllString(lines[2], ""), styleANSI.ReplaceAllString(lines[3], "")
			if !strings.Contains(claude, "5h-used:") || !strings.Contains(codex, "codex-7d-used:63% ↻3d7h") {
				t.Fatalf("quota row order/value/reset mismatch: %q then %q", claude, codex)
			}
			if got, want := utf8.RuneCountInString(codex), utf8.RuneCountInString(claude); got != want {
				t.Fatalf("Codex width %d != Claude width %d: %q then %q", got, want, claude, codex)
			}
			cells := strings.Count(codex, "▰") + strings.Count(codex, "▱")
			if cells <= 5 || strings.Count(codex, "▰") != 63*cells/100 {
				t.Fatalf("weekly bar was not stretched/proportional: %q", codex)
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
		{name: "wrong window", body: `{"secondary":{"usedPercent":42,"windowDurationMins":300,"resetsAt":1787122800}}`, want: "codex-7d: error", warn: true},
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
			if len(lines) != 4 ||
				utf8.RuneCountInString(last) != utf8.RuneCountInString(styleANSI.ReplaceAllString(lines[2], "")) {
				t.Fatalf("unavailable/stale weekly row must match Claude width: %q", lines)
			}
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
	lines := weeklyLines(t, weeklyRuntime(t), `{}`)
	if len(lines) != 3 || !strings.Contains(lines[2], "codex-7d: no data") {
		t.Fatalf("missing Claude quotas must not hide Codex data status: %q", lines)
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
