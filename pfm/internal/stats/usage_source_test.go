package stats

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

type builtinTestUsageSource struct{ id pfmengine.ID }

func (source builtinTestUsageSource) Fetch(ctx context.Context, account LimitAccount) (AccountLimits, error) {
	switch source.id {
	case pfmengine.Claude:
		return FetchClaude(ctx, account)
	case pfmengine.Codex:
		return FetchCodex(ctx, account)
	default:
		return AccountLimits{}, nil
	}
}

func init() {
	RegisterUsageSource(pfmengine.Claude, builtinTestUsageSource{id: pfmengine.Claude})
	RegisterUsageSource(pfmengine.Codex, builtinTestUsageSource{id: pfmengine.Codex})
}

func TestUnknownEngineIsANamedError(t *testing.T) {
	_, err := UsageSourceFor(pfmengine.ID("zz"))
	if err == nil || err.Error() != "engine zz: no usage source registered" {
		t.Fatalf("UsageSourceFor(zz) error = %v", err)
	}
}

// The Limits tab's requests go through the usage door carrying the picker's
// version: the sampler's Version reaches the User-Agent.
func TestLimitsSamplerRequestNamesThePfmVersion(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	configDir := filepath.Join(home, "claude")
	writeFixtureCredentials(t, configDir)
	now := time.Unix(1_800_000_000, 0)
	agent := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		agent = request.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, usageJSONBody(51, 61, now))
	}))
	defer server.Close()
	sampler := NewLimitsSampler(
		[]LimitAccount{{ID: 11, Engine: pfmengine.Claude, Label: "account 11", ConfigDir: configDir}},
	)
	sampler.Now = func() time.Time { return now }
	sampler.Endpoint = server.URL
	sampler.Client = server.Client()
	sampler.Version = "7.7.7"
	if limits, _ := sampler.Sample(context.Background()); len(limits) != 1 || len(limits[0].Windows) != 2 {
		t.Fatalf("sample through the usage door: %#v", limits)
	}
	if agent != "pfm/7.7.7" {
		t.Fatalf("User-Agent = %q, want pfm/7.7.7", agent)
	}
}

// An account whose provider answers with no limit window at all (on credits)
// gets a card that says so, with no window, no warning and no error status;
// the answer is the plan's shape, not a failed refresh.
func TestAnAnswerWithNoLimitWindowIsAStatusNotAFailure(t *testing.T) {
	for _, engine := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex} {
		t.Run(string(engine), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(paths.EnvHome, home)
			configDir := filepath.Join(home, "claude")
			writeFixtureCredentials(t, configDir)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if engine == pfmengine.Claude {
					fmt.Fprint(w, `{"five_hour":null,"seven_day":null,"seven_day_opus":null,"limits":[]}`)
					return
				}
				fmt.Fprint(w, `{"plan_type":"pro","rate_limit":null,"credits":{"balance":"12.5"}}`)
			}))
			defer server.Close()
			account := LimitAccount{ID: 12, Engine: engine, Label: "account 12", ConfigDir: configDir}
			if engine == pfmengine.Codex {
				account = LimitAccount{
					ID: 12, Engine: engine, Label: "Codex",
					CodexAuthPath: writeCodexAuth(t, home, "fixture-access", "fixture-account"),
				}
			}
			sampler := NewLimitsSampler([]LimitAccount{account})
			sampler.Now = func() time.Time { return time.Unix(1_800_000_000, 0) }
			sampler.Endpoint, sampler.CodexEndpoint = server.URL, server.URL
			sampler.Client, sampler.CodexClient = server.Client(), server.Client()
			limits, warnings := sampler.Sample(context.Background())
			if len(warnings) != 0 || len(limits) != 1 || len(limits[0].Windows) != 0 ||
				limits[0].Status != "no limit window on this plan" {
				t.Fatalf("limits=%#v warnings=%v, want a no-window card and no warning", limits, warnings)
			}
		})
	}
}

type fixedLimitsSource struct{ limits AccountLimits }

func (source fixedLimitsSource) Fetch(context.Context, LimitAccount) (AccountLimits, error) {
	return source.limits, nil
}

// A forward-dated confirmation stamp is clamped to now: the windows beside it
// are still the provider's answer and stay, with a warning.
func TestLimitsSamplerKeepsWindowsUnderAFutureConfirmationStamp(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	const engine = pfmengine.ID("zz-future")
	RegisterUsageSource(engine, fixedLimitsSource{limits: AccountLimits{
		ConfirmedAt: now.Add(time.Hour), Windows: []Window{{Name: "5h", UsedPct: 42}},
	}})
	t.Cleanup(func() { delete(usageSources, engine) })
	sampler := NewLimitsSampler([]LimitAccount{{ID: 9, Engine: engine, Label: "account 9"}})
	sampler.Now = func() time.Time { return now }
	limits, warnings := sampler.Sample(context.Background())
	wantWarning := "account 9 limits: future confirmation timestamp, treated as now"
	if len(warnings) != 1 || warnings[0] != wantWarning {
		t.Fatalf("warnings = %q, want [%q]", warnings, wantWarning)
	}
	if len(limits) != 1 || len(limits[0].Windows) != 1 || limits[0].Windows[0].UsedPct != 42 ||
		!limits[0].ConfirmedAt.Equal(now) || limits[0].Status != "" {
		t.Fatalf("limits = %#v, want the 5h window at 42%% confirmed at now", limits)
	}
}

// A window the provider names with a reset but no utilization keeps its row
// with an unknown number: skipping it would hide the window and its reset.
func TestUsageWindowsKeepsAResetOnlyWindowWithAnUnknownNumber(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	windows := usageWindows(usagehook.Usage{
		SevenDay: usagehook.Window{ResetsAt: now.Add(72 * time.Hour).Format(time.RFC3339)},
	}, now)
	if len(windows) != 1 || windows[0].Name != "7d" || windows[0].UsedPct != UnknownUsedPct ||
		!windows[0].ResetAt.Equal(now.Add(72*time.Hour)) {
		t.Fatalf("windows=%#v, want a 7d row with an unknown number and its reset", windows)
	}
}

// A statusline snapshot that answers stands even when a sibling file it skipped comes with it:
// the account keeps its windows under the fallback, and the skipped file is a warning naming it.
func TestStatuslineFallbackKeepsItsWindowsBesideASkippedSnapshotFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	configDir := filepath.Join(home, ".claude2")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	confirmed := now.Add(-30 * time.Minute) // past the usage door's TTL, inside the stale horizon
	rateDir := filepath.Join(home, "tmp", "cc-rate-limits")
	writeStatuslineQuotaFixture(t, rateDir, configDir, confirmed)
	if err := os.WriteFile(filepath.Join(rateDir, "acct-2.broken.json"), []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sampler := NewLimitsSampler([]LimitAccount{{
		ID: 2, Engine: pfmengine.Claude, Label: "account 2", ConfigDir: configDir,
	}})
	sampler.Now = func() time.Time { return now }
	sampler.Ack = func(context.Context, LimitAccount) error {
		t.Error("credential refresh must not run: the snapshot answered")
		return nil
	}
	limits, warnings := sampler.Sample(context.Background())
	want := "account 2 limits: statusline quota partly unreadable, showing what read: skipped statusline quota " +
		"acct-2.broken.json: decode: invalid character 'o' in literal null (expecting 'u')"
	if len(warnings) != 1 || warnings[0] != want {
		t.Fatalf("warnings = %q, want [%q]", warnings, want)
	}
	if len(limits) != 1 || len(limits[0].Windows) != 2 || limits[0].Windows[0].UsedPct != 31 ||
		limits[0].Windows[1].UsedPct != 47 || !limits[0].ConfirmedAt.Equal(confirmed) ||
		limits[0].Status != "statusline quota partly unreadable" {
		t.Fatalf("limits = %#v, want the 31/47 windows confirmed at %s", limits, confirmed)
	}
}
