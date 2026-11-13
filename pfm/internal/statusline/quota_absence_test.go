package statusline

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

// renderPlain draws payload at now and returns it without styling.
func renderPlain(t *testing.T, runtime Runtime, now time.Time, payload string) string {
	t.Helper()
	runtime.Now = func() time.Time { return now }
	got, err := Render(context.Background(), []byte(payload), runtime)
	if err != nil {
		t.Fatal(err)
	}
	return stripANSICodes(got)
}

// A plan that dropped its 5-hour window: the usage response leaves five_hour
// out and the harness reports only seven_day. Through the real door and on
// past the backoff minute and the stale horizon, the 5h row never shows ERR and
// never comes back from the record that predates the change: it stays hidden,
// draw after draw. The response's Fable entry shows while the answer is fresh.
func TestAWindowThePlanDroppedStaysHiddenThroughTheDoorsCycle(t *testing.T) {
	start := time.Unix(1_790_000_000, 0).UTC()
	week := start.Add(5 * 24 * time.Hour)
	for _, payload := range []struct{ name, body, want string }{
		{"harness reports seven_day only", `{"model":{"id":"claude-opus-4"},"rate_limits":{"seven_day":` +
			`{"used_percentage":11,"resets_at":` + strconv.FormatInt(week.Unix(), 10) + `}}}`, "0s: 🥈 ◆ opus·effort unknown │ ~ │ · ·\n🟢 ▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ ?% ? ctx unknown │ 💰$?/?/?/? │ ⏳ 0s\n▱▱▱▱▱ 7d-fable-used:3% ↻5d0h │ ▱▱▱▱▱ 7d-used:11% ↻5d0h\n▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ codex-7d: no data\n\n30s: 🥈 ◆ opus·effort unknown │ ~ │ · ·\n🟢 ▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ ?% ? ctx unknown │ 💰$?/?/?/? │ ⏳ 0s\n▱▱▱▱▱ 7d-fable-used:3% ↻4d23h │ ▱▱▱▱▱ 7d-used:11% ↻4d23h\n▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ codex-7d: no data\n\n2m0s: 🥈 ◆ opus·effort unknown │ ~ │ · ·\n🟢 ▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ ?% ? ctx unknown │ 💰$?/?/?/? │ ⏳ 0s\n▱▱▱▱▱ 7d-fable-used:3% ↻4d23h │ ▱▱▱▱▱ 7d-used:11% ↻4d23h\n▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ codex-7d: no data\n\n2h0m0s: 🥈 ◆ opus·effort unknown │ ~ │ · ·\n🟢 ▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ ?% ? ctx unknown │ 💰$?/?/?/? │ ⏳ 0s\n▱▱▱▱▱ 7d-fable-used:— │ ▱▱▱▱▱ 7d-used:11% ↻4d22h\n▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ codex-7d: no data\n\n"},
		{"harness reports nothing yet", `{"model":{"id":"claude-opus-4"}}`, "0s: 🥈 ◆ opus·effort unknown │ ~ │ · ·\n🟢 ▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ ?% ? ctx unknown │ 💰$?/?/?/? │ ⏳ 0s\n▱▱▱▱▱ 7d-fable-used:3% ↻5d0h │ ▱▱▱▱▱ 7d-used:11% ↻5d0h\n▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ codex-7d: no data\n\n30s: 🥈 ◆ opus·effort unknown │ ~ │ · ·\n🟢 ▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ ?% ? ctx unknown │ 💰$?/?/?/? │ ⏳ 0s\n▱▱▱▱▱ 7d-fable-used:3% ↻4d23h │ ▱▱▱▱▱ 7d-used:11% ↻4d23h\n▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ codex-7d: no data\n\n2m0s: 🥈 ◆ opus·effort unknown │ ~ │ · ·\n🟢 ▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ ?% ? ctx unknown │ 💰$?/?/?/? │ ⏳ 0s\n▱▱▱▱▱ 7d-fable-used:3% ↻4d23h │ ▱▱▱▱▱ 7d-used:11% ↻4d23h\n▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ codex-7d: no data\n\n2h0m0s: 🥈 ◆ opus·effort unknown │ ~ │ · ·\n🟢 ▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ ?% ? ctx unknown │ 💰$?/?/?/? │ ⏳ 0s\n▱▱▱▱▱ 7d-fable-used:— │ ▱▱▱▱▱▱▱▱▱▱▱▱▱▱ 7d-used:—\n▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ codex-7d: no data\n\n"},
	} {
		t.Run(payload.name, func(t *testing.T) {
			root := t.TempDir()
			runtime := fableRuntime(root, 2, start)
			if err := os.MkdirAll(runtime.ConfigDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(runtime.ConfigDir, ".credentials.json"),
				[]byte(`{"claudeAiOauth":{"accessToken":"fixture-token"}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			// The record a plan with a 5-hour window left behind.
			before := start.Add(-30 * time.Minute)
			writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
				Usage: usagehook.Usage{
					FiveHour: usagehook.Window{
						Utilization: floatPtr(5), ResetsAt: start.Add(time.Hour).Format(time.RFC3339),
					},
					SevenDay: usagehook.Window{Utilization: floatPtr(10), ResetsAt: week.Format(time.RFC3339)},
				},
				ConfigDir: runtime.ConfigDir, FetchedAt: &before,
			})
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(writer, `{"seven_day":{"utilization":11,"resets_at":"`+week.Format(time.RFC3339)+
					`"},"seven_day_opus":null,"limits":[{"kind":"weekly_scoped","scope":{"model":{"display_name":"Fable"}},`+
					`"percent":3,"resets_at":"`+week.Format(time.RFC3339)+`","is_active":false}]}`)
			}))
			defer server.Close()
			_, _, _ = usagehook.Fetch(context.Background(), usagehook.Options{
				Now:          func() time.Time { return start },
				Env:          &paths.MapEnv{Values: map[string]string{paths.EnvHome: root}},
				Home:         root,
				ConfigDir:    runtime.ConfigDir,
				CacheDir:     usagehook.UsageCacheDir(runtime.CacheDir, runtime.UID),
				TTL:          3 * time.Minute,
				Client:       server.Client(),
				Endpoint:     server.URL,
				ClaudeBinary: filepath.Join(root, "no-claude"),
			}, 2)
			// a window the plan dropped stays hidden at every age of the answer
			got := ""
			for _, after := range []time.Duration{0, 30 * time.Second, 2 * time.Minute, 2 * time.Hour} {
				got += after.String() + ": " + renderPlain(t, runtime, start.Add(after), payload.body) + "\n"
			}
			if got != payload.want {
				t.Errorf("renders = %q, want %q", got, payload.want)
			}
		})
	}
}

// A real failure stays visible and steady: after a 429, a window the last
// success carried reads ERR while the backoff runs, after it lapses and past
// the stale horizon, until a request succeeds; the windows that success left
// out stay hidden all the while instead of turning into ERR.
func TestAFailureShowsERROnlyForWindowsTheLastAnswerCarried(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	runtime := fableRuntime(t.TempDir(), 2, start)
	fetched := start.Add(-5 * time.Minute)
	writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
		Usage: usagehook.Usage{SevenDay: usagehook.Window{
			Utilization: floatPtr(40), ResetsAt: start.Add(3 * 24 * time.Hour).Format(time.RFC3339),
		}},
		ConfigDir: runtime.ConfigDir, FetchedAt: &fetched,
		Backoff: &usagehook.CacheBackoff{
			Message:    "rate-limited — retry (429 Too Many Requests)",
			RetryAfter: start.Add(10 * time.Minute),
		},
	})
	for _, after := range []time.Duration{0, 20 * time.Minute, 2 * time.Hour} {
		plain := renderPlain(t, runtime, start.Add(after), `{"model":{"id":"claude-opus-4"}}`)
		if plain != `🥈 ◆ opus·effort unknown │ ~ │ · ·
🟢 ▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ ?% ? ctx unknown │ 💰$?/?/?/? │ ⏳ 0s
▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ 7d-used:ERR
▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ codex-7d: no data
` {
			t.Fatalf("+%s: the failed window must read ERR, got %q", after, plain)
		}
		if plain != `🥈 ◆ opus·effort unknown │ ~ │ · ·
🟢 ▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ ?% ? ctx unknown │ 💰$?/?/?/? │ ⏳ 0s
▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ 7d-used:ERR
▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ codex-7d: no data
` {
			t.Fatalf("+%s: windows the last answer left out must stay hidden, got %q", after, plain)
		}
	}
}

// An account on credits: the latest answers report no window at all, so no
// limit row shows, for Claude and for Codex alike, instead of a "no data" row.
// A plan that reports one weekly window keeps that row.
func TestAnAccountWithNoWindowsShowsNoLimitRow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	runtime := fableRuntime(t.TempDir(), 2, now)
	fetched := now.Add(-time.Minute)
	writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
		ConfigDir: runtime.ConfigDir, FetchedAt: &fetched,
	})
	weeklyCache(t, runtime, `{"credits":"120.5","planType":"pro","primary":null,"secondary":null}`, time.Minute)
	plain := renderPlain(t, runtime, now, `{"model":{"id":"claude-opus-4"}}`)
	if plain != `🥈 ◆ opus·effort unknown │ ~ │ · ·
🟢 ▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ ?% ? ctx unknown │ 💰$?/?/?/? │ ⏳ 0s
` {
		t.Fatalf("no window answered, so no limit row may show: %q", plain)
	}
	weeklyCache(t, runtime, `{"credits":"1","planType":"pro","primary":{"usedPercent":82,"windowDurationMins":10080,`+
		`"resetsAt":`+strconv.FormatInt(now.Add(48*time.Hour).Unix(), 10)+`},"secondary":null}`, time.Minute)
	if plain := renderPlain(
		t,
		runtime,
		now,
		`{"model":{"id":"claude-opus-4"}}`,
	); !strings.Contains(
		plain,
		"codex-7d-used:82%",
	) {
		t.Fatalf("the reported weekly window must stay: %q", plain)
	}
}

// The Codex chat's own segment: an answer with no window names the plan only,
// never "quota unknown"; no cache yet still says it is missing.
func TestCodexSegmentWithNoWindowNamesThePlanOnly(t *testing.T) {
	r := parityRuntime(t)
	path := filepath.Join(r.CacheDir, "cc-gpt-usage-0.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"credits":"9","planType":"pro","primary":null,"secondary":null}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if got := stripANSICodes(codexQuotaSegment(r, r.now())); got != "ChatGPT pro" {
		t.Fatalf("segment %q, want the plan alone", got)
	}
}
