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

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// The prompt hook names only the windows the answer carries: with the 5-hour
// window gone from the plan, a 7-day reading at the warning level warns about
// the 7-day window alone, and no "5h 0%" is made up for the missing one.
func TestEvaluateWarnsWithoutAWindowThePlanLacks(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	root, configDir, cacheDir, env := doorSeat(t, 12, now)
	server, _ := fixedUsageServer(t, `{"seven_day":{"utilization":85,"resets_at":"`+
		now.Add(48*time.Hour).UTC().Format(time.RFC3339)+`"}}`)
	message, err := Evaluate(context.Background(), Options{
		Now: func() time.Time { return now }, Env: env, Home: root, ConfigDir: configDir,
		AccountDirs: map[string]int{configDir: 12}, CacheDir: cacheDir, Warn: 80, Critical: 95,
		TTL: 3 * time.Minute, Client: server.Client(), Endpoint: server.URL, Log: io.Discard,
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !strings.Contains(message, "7d 85%") || strings.Contains(message, "5h") {
		t.Fatalf("warning %q, want the 7d reading and no 5h phrase", message)
	}
}

// The critical guidance never quotes the headroom of a 5-hour window the plan
// does not have.
func TestCriticalGuidanceWithoutAFiveHourWindow(t *testing.T) {
	for _, testcase := range []struct {
		name                     string
		five, seven, opus, fable int
	}{
		{"7-day cap", -1, 96, -1, -1},
		{"Fable cap", -1, 10, -1, 97},
	} {
		if got := criticalGuidance(
			testcase.five,
			testcase.seven,
			testcase.opus,
			testcase.fable,
			95,
		); strings.Contains(
			got,
			"5-hour",
		) {
			t.Errorf("%s: guidance %q names a 5-hour window the plan lacks", testcase.name, got)
		}
	}
}

// A warned account whose next answer carries no main window (the plan dropped
// them, or the account moved onto credits) still gets its recovery line, in
// words, never "back to ." with nothing named.
func TestEvaluateRecoversToAnAnswerWithNoWindow(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	root, configDir, cacheDir, env := doorSeat(t, 12, now)
	if err := paths.EnsurePrivateDir("usage cache directory", cacheDir); err != nil {
		t.Fatalf("cache dir: %v", err)
	}
	flagPath := filepath.Join(cacheDir, fmt.Sprintf("warned-%d", 12))
	if err := os.WriteFile(flagPath, []byte(configDir), 0o600); err != nil {
		t.Fatalf("write warned flag: %v", err)
	}
	server, _ := fixedUsageServer(t, `{"five_hour":null,"seven_day":null,"limits":[]}`)
	message, err := Evaluate(context.Background(), Options{
		Now: func() time.Time { return now }, Env: env, Home: root, ConfigDir: configDir,
		AccountDirs: map[string]int{configDir: 12}, CacheDir: cacheDir, Warn: 80, Critical: 95,
		TTL: 3 * time.Minute, Client: server.Client(), Endpoint: server.URL, Log: io.Discard,
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !strings.Contains(message, "usage recovered — account 12 reports no limit window on its plan.") ||
		strings.Contains(message, "back to .") {
		t.Fatalf("recovery %q, want the no-window wording", message)
	}
}
