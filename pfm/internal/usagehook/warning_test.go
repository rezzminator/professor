package usagehook

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A plan without a 5-hour window warns on the 7-day reading alone; a warned seat whose plan reports no window
// recovers with the no-window wording.
func TestEvaluateAnswersAPlanWithoutAWindow(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	for _, row := range []struct {
		name, body string
		warned     bool
		want       string
	}{
		{"7-day only", `{"seven_day":{"utilization":85,"resets_at":"2026-11-15T09:30:00Z"}}`, false, "⚠ usage limit approaching — account 12: 7d 85% (resets Sun 09:30). Plan around it: wrap up soon or /reload to another account before the cap hits.\n | <nil>"},
		{"no window at all", `{"five_hour":null,"seven_day":null,"limits":[]}`, true, "✅ usage recovered — account 12 reports no limit window on its plan. Any earlier limit warning in this conversation is STALE — ignore it and work normally.\n | <nil>"},
	} {
		t.Run(row.name, func(t *testing.T) {
			root, configDir, cacheDir, env := doorSeat(t, 12, now)
			if row.warned {
				if err := os.WriteFile(filepath.Join(cacheDir, "warned-12"), []byte(configDir), 0o600); err != nil {
					t.Fatalf("write warned flag: %v", err)
				}
			}
			server, _ := fixedUsageServer(t, row.body)
			message, err := Evaluate(context.Background(), Options{
				Now: func() time.Time { return now }, Env: env, Home: root, ConfigDir: configDir,
				AccountDirs: map[string]int{configDir: 12}, CacheDir: cacheDir, Warn: 80, Critical: 95,
				TTL: 3 * time.Minute, Client: server.Client(), Endpoint: server.URL, Log: io.Discard,
			})
			if got := strings.ReplaceAll(fmt.Sprint(message, " | ", err), root, "{root}"); got != row.want {
				t.Errorf("message | err = %q, want %q", got, row.want)
			}
		})
	}
}

func TestCriticalGuidanceWithoutAFiveHourWindow(t *testing.T) {
	for _, row := range []struct {
		name                     string
		five, seven, opus, fable int
		want                     string
	}{
		{"7-day cap", -1, 96, -1, -1, "The 7-day account cap is nearly reached: /reload to another account."},
		{"Fable cap", -1, 10, -1, 97, "Only the 7-day fable cap is nearly reached — every other model on this account is unaffected. Keep working; route fable-tier spawns elsewhere or /reload for those."},
	} {
		if got := criticalGuidance(row.five, row.seven, row.opus, row.fable, 95); got != row.want {
			t.Errorf("%s: guidance = %q, want %q", row.name, got, row.want)
		}
	}
}
