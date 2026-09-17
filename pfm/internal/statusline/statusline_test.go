package statusline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/transcript"
)

func TestEngineFromEnvironmentRefusesMissingEngine(t *testing.T) {
	_, err := EngineFromEnvironment(func(string) string { return "" })
	if !errors.Is(err, ErrNoEngineInEnvironment) {
		t.Fatalf("EngineFromEnvironment(empty) error = %v, want ErrNoEngineInEnvironment", err)
	}
}

func TestEngineFromEnvironmentUsesDescriptorVariables(t *testing.T) {
	for _, testCase := range []struct {
		name string
		key  string
		want pfmengine.ID
	}{
		{name: "session", key: "CODEX_THREAD_ID", want: pfmengine.Codex},
		{name: "home", key: "CLAUDE_CONFIG_DIR", want: pfmengine.Claude},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := EngineFromEnvironment(func(key string) string {
				if key == testCase.key {
					return "set"
				}
				return ""
			})
			if err != nil || got != testCase.want {
				t.Fatalf("EngineFromEnvironment(%s) = (%q, %v), want (%q, nil)", testCase.key, got, err, testCase.want)
			}
		})
	}
}

func TestDefaultRuntimeUsesTheCodexSeatsOwnHome(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex-2")
	t.Setenv("PFM_HOME", home)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CODEX_THREAD_ID", "thread-2")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude-must-not-win"))
	runtime, err := DefaultRuntime(pfmengine.Codex)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Engine != pfmengine.Codex || runtime.ConfigDir != codexHome {
		t.Fatalf(
			"DefaultRuntime() engine=%q account home=%q, want codex/%q",
			runtime.Engine,
			runtime.ConfigDir,
			codexHome,
		)
	}
}

func TestUnknownConfigDirHasNoAccountBadge(t *testing.T) {
	runtime := Runtime{
		Home:        t.TempDir(),
		ConfigDir:   filepath.Join(t.TempDir(), "unmatched"),
		AccountDirs: map[string]int{filepath.Join(t.TempDir(), "account-one"): 1},
	}
	badge, account := accountBadge(runtime)
	if badge != "" || account != 0 {
		t.Fatalf("unknown account badge=%q account=%d, want empty/0", badge, account)
	}
}

func TestMissingConfiguredAccountHasNoFallbackBadge(t *testing.T) {
	badge, account := accountBadgeForID(Runtime{
		AccountEmojis: map[int]string{1: "🥇"},
	}, 2)
	if badge != "" || account != 0 {
		t.Fatalf("missing configured account badge=%q account=%d, want empty/0", badge, account)
	}
}

func TestClaudeAccountFourDoesNotRenderCodexUsage(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, ".cc", "4")
	got, err := Render(context.Background(), []byte(`{}`), Runtime{
		Now:           time.Now,
		Home:          root,
		ConfigDir:     configDir,
		CacheDir:      filepath.Join(root, "cache"),
		RateLimitDir:  filepath.Join(root, "rates"),
		SIDDir:        filepath.Join(root, "sid"),
		TmuxDir:       filepath.Join(root, "tmux"),
		ProcRoot:      filepath.Join(root, "proc"),
		Columns:       120,
		UID:           1000,
		AccountDirs:   map[string]int{configDir: 4},
		AccountEmojis: map[int]string{4: "🍀"},
		Env:           map[string]string{},
		Command:       quietRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	plain := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(got, "")
	if strings.Contains(plain, "ChatGPT") || strings.Contains(plain, "proxy") {
		t.Fatalf("Claude account 4 rendered Codex usage: %q", plain)
	}
}

func TestStatuslineRendersFableRateAndSymbol(t *testing.T) {
	got, err := Render(context.Background(), []byte(`{
  "model":{"display_name":"Fable"},
  "rate_limits":{"limits":[{"kind":"weekly_scoped","percent":23,"resets_at":"2030-01-07T08:00:00Z","scope":{"model":{"display_name":" fAbLe "}},"is_active":true}]}
}`), Runtime{
		Home: t.TempDir(), Columns: 120, UID: 1000, Command: quietRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	plain := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(got, "")
	if !strings.Contains(plain, "✦ Fable") || !strings.Contains(plain, "7d-fable-used:23%") {
		t.Fatalf("fable statusline=%q", plain)
	}
}

func TestStatuslineQuotaSnapshotCarriesAccountConfigIdentity(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, ".cc", "2")
	rateDir := filepath.Join(root, "rates")
	now := time.Now().Truncate(time.Second)
	runtime := Runtime{
		Now: func() time.Time { return now }, Home: root, ConfigDir: configDir,
		CacheDir: filepath.Join(root, "cache"), RateLimitDir: rateDir,
		SIDDir: filepath.Join(root, "sid"), TmuxDir: filepath.Join(root, "tmux"),
		ProcRoot: filepath.Join(root, "proc"), Columns: 120, UID: 1000,
		AccountDirs: map[string]int{configDir: 2}, Env: map[string]string{}, Command: quietRunner{},
	}
	if _, account := accountBadge(runtime); account != 2 {
		t.Fatalf("accountBadge(config 2)=%d, want 2", account)
	}
	input := []byte(fmt.Sprintf(`{
  "model":{"display_name":"Opus 4"},
  "session_id":"account-two-session",
  "rate_limits":{
    "five_hour":{"used_percentage":31,"resets_at":%d},
    "seven_day":{"used_percentage":47,"resets_at":%d}
  }
}`, now.Add(4*time.Hour).Unix(), now.Add(6*24*time.Hour).Unix()))
	_, err := Render(context.Background(), input, runtime)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(rateDir, "acct-2.account-two-session.json"))
	if err != nil {
		entries, _ := os.ReadDir(rateDir)
		t.Fatalf("read account 2 quota snapshot: %v; entries=%v", err, entries)
	}
	var snapshot struct {
		Account   int    `json:"acct"`
		ConfigDir string `json:"config_dir"`
		TS        int64  `json:"ts"`
	}
	if err := json.Unmarshal(body, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Account != 2 || snapshot.ConfigDir != configDir || snapshot.TS != now.Unix() {
		t.Fatalf("quota snapshot identity=%#v, want account 2 config %q at %d", snapshot, configDir, now.Unix())
	}
}

func TestStatuslineDropsUnknownTopLevelWindows(t *testing.T) {
	got, err := Render(context.Background(), []byte(`{
  "rate_limits":{
    "seven_day_nimbus_quill":{"used_percentage":17,"resets_at":1800086400},
    "plan_type":"fixture"
  }
}`), Runtime{
		Home: t.TempDir(), Columns: 120, UID: 1000, Command: quietRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	plain := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(got, "")
	if strings.Contains(plain, "nimbus_quill") || strings.Contains(plain, "unknown[") {
		t.Fatalf("unknown top-level key rendered as a window: %q", plain)
	}
}

type quietRunner struct{}

func (quietRunner) Output(
	context.Context,
	string,
	...string,
) ([]byte, error) {
	return nil, nil
}

func TestStatuslineCapturedInputGoldens(t *testing.T) {
	for _, sample := range []struct {
		name    string
		fixture string
		account int
		engine  string
		env     map[string]string
	}{
		{name: "primary", fixture: "primary", account: 1, engine: "claude", env: map[string]string{"PFM_TEST_PROBE_SOCKETS": "1"}},
		{name: "codex", fixture: "gpt", account: 4, engine: "codex", env: map[string]string{
			"PFM_TEST_PROBE_SOCKETS": "1",
			"ANTHROPIC_MODEL":        "gpt-5.6-sol[1m]",
		}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			root := t.TempDir()
			now := time.Unix(1_786_838_400, 0)
			cacheDir := filepath.Join(root, "cache")
			tmuxDir := filepath.Join(root, "tmux")
			procRoot := filepath.Join(root, "proc")
			for _, directory := range []string{cacheDir, tmuxDir, filepath.Join(procRoot, "net")} {
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			for _, socket := range []string{"probe-cc-one", "probe-cc-two", "probe-cx-one"} {
				if err := os.WriteFile(filepath.Join(tmuxDir, socket), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if sample.account == 4 {
				writeGoldenCache(
					t,
					filepath.Join(cacheDir, "cc-gpt-usage-1000.json"),
					`{"primary":{"usedPercent":32,"windowDurationMins":300,"resetsAt":1786845600},"secondary":{"usedPercent":71,"windowDurationMins":10080,"resetsAt":1787443200},"planType":"plus"}`,
					now,
				)
				writeGoldenCache(t, filepath.Join(cacheDir, "cc-sl-gptreq"), "7 0\n", now)
				if err := os.WriteFile(
					filepath.Join(procRoot, "net", "tcp"),
					[]byte(" 00000000:494D "),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			raw, err := os.ReadFile(filepath.Join("testdata", "render-"+sample.fixture+".json"))
			if err != nil {
				t.Fatal(err)
			}
			// A captured input cannot ship the transcript path it was captured
			// with — that is a machine home path, which never enters a tracked
			// file. The jail supplies one instead, so the golden exercises the
			// cache window on the path that renders a WINDOW; the broken states
			// are pinned separately by the regression test below.
			raw = []byte(strings.ReplaceAll(
				string(raw),
				"__TRANSCRIPT__",
				writeGoldenTranscript(t, root, now.Add(-12*time.Minute), sample.engine),
			))
			engineID, parseErr := pfmengine.Parse(sample.engine)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			got, err := Render(context.Background(), raw, Runtime{
				Now:          func() time.Time { return now },
				Home:         root,
				ConfigDir:    filepath.Join(root, ".cc", strconv.Itoa(sample.account)),
				CacheDir:     cacheDir,
				RateLimitDir: filepath.Join(root, "rates"),
				SIDDir:       filepath.Join(root, "sid"),
				TmuxDir:      tmuxDir,
				ProcRoot:     procRoot,
				Columns:      120,
				UID:          1000,
				Engine:       engineID,
				Env:          sample.env,
				Command:      quietRunner{},
			})
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join("testdata", "render-"+sample.fixture+".golden"))
			if err != nil {
				t.Fatalf("golden is missing; captured output is:\n%s", strconv.Quote(got))
			}
			if quoted := strconv.Quote(got) + "\n"; quoted != string(want) {
				t.Fatalf("statusline visual drift:\n got %s\nwant %s", quoted, want)
			}
		})
	}
}

// writeGoldenTranscript lays down a one-turn transcript inside the jail and
// returns its path, so a golden can anchor the cache window at a fixed offset.
func writeGoldenTranscript(t *testing.T, root string, turn time.Time, engine string) string {
	t.Helper()
	path := filepath.Join(root, "transcript.jsonl")
	body := `{"type":"assistant","timestamp":"` + turn.UTC().Format(time.RFC3339Nano) + `"}` + "\n"
	if engine == "codex" {
		body += `{"payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":136000},"model_context_window":272000}}}` + "\n"
	} else {
		body = `{"type":"assistant","timestamp":"` + turn.UTC().
			Format(time.RFC3339Nano) +
			`","message":{"model":"claude-opus-4-1","usage":{"input_tokens":1000,"cache_read_input_tokens":419000}}}` + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeGoldenCache(t *testing.T, path, body string, now time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := now.Add(-5 * time.Minute)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func TestRenderCarriesNativeIdentityMetricsAndSky(t *testing.T) {
	root := t.TempDir()
	tmuxDir := filepath.Join(root, "tmux")
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"probe-cc-one", "probe-cc-two", "probe-cx-one"} {
		if err := os.WriteFile(filepath.Join(tmuxDir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	input := []byte(`{
  "model":{"display_name":"Opus 4"},
  "workspace":{"current_dir":"/work/sample"},
  "context_window":{
    "used_percentage":42.9,
    "total_input_tokens":12345,
    "total_output_tokens":678,
    "current_usage":{"cache_read_input_tokens":8000,"cache_creation_input_tokens":2000,"input_tokens":345}
  },
  "cost":{"total_cost_usd":3.42,"total_duration_ms":332000},
  "effort":{"level":"high"},
  "thinking":{"enabled":true},
  "session_name":"BUILDER:1",
  "session_id":"11111111-1111-4111-8111-111111111111"
}`)
	runtime := Runtime{
		Now:          func() time.Time { return time.Unix(1_786_838_400, 0) },
		Home:         root,
		ConfigDir:    filepath.Join(root, ".cc", "1"),
		CacheDir:     filepath.Join(root, "cache"),
		RateLimitDir: filepath.Join(root, "rates"),
		SIDDir:       filepath.Join(root, "sid"),
		TmuxDir:      tmuxDir,
		ProcRoot:     filepath.Join(root, "proc"),
		Columns:      120,
		UID:          1000,
		Env:          map[string]string{"PFM_TEST_PROBE_SOCKETS": "1"},
		Command:      quietRunner{},
	}
	got, err := Render(context.Background(), input, runtime)
	if err != nil {
		t.Fatal(err)
	}
	plain := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(got, "")
	for _, want := range []string{
		"🥇 ", "◆ Opus 4", "🔖 BUILDER:1", "💠 high", "sample",
		"42%", "🧮10.3K", "💰$3.42", "⏳ 5m32s", "·2 ·1",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("render lacks %q:\n%q", want, got)
		}
	}
}

func TestRenderUsesPostCompactFloorEstimateInsteadOfStaleSelfReport(t *testing.T) {
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "session.jsonl")
	transcript := strings.Join([]string{
		`{"type":"assistant","isSidechain":false,"message":{"model":"claude-opus-4-1","usage":{"input_tokens":1000,"cache_read_input_tokens":699000,"cache_creation_input_tokens":0,"output_tokens":1000}}}`,
		`{"type":"system","subtype":"compact_boundary","compactMetadata":{"postTokens":150000}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}

	xdgCache := filepath.Join(root, "xdg-cache")
	floorDir := filepath.Join(xdgCache, "pfm-statusline")
	if err := os.MkdirAll(floorDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(floorDir, "claude-work-sample.txt"), []byte("50000\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	encodedTranscriptPath, err := json.Marshal(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	input := []byte(`{
  "model":{"id":"claude-opus-4-1","display_name":"Opus 4"},
  "cwd":"/work/sample",
  "context_window":{"used_percentage":77},
  "transcript_path":` + string(encodedTranscriptPath) + `
}`)
	got, err := Render(context.Background(), input, Runtime{
		Home:     root,
		CacheDir: filepath.Join(root, "cache"),
		TmuxDir:  filepath.Join(root, "tmux"),
		ProcRoot: filepath.Join(root, "proc"),
		Columns:  120,
		UID:      1000,
		Env:      map[string]string{"XDG_CACHE_HOME": xdgCache},
		Command:  quietRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	plain := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(got, "")
	if !strings.Contains(plain, "▰▰▱▱▱▱▱▱▱▱ ~20%") || strings.Contains(plain, "77%") {
		t.Fatalf(
			"post-compact gauge did not use floor + postTokens estimate, or retained stale self-report:\n%q",
			plain,
		)
	}
}

func TestRenderMarksCompactBoundaryEstimateWithoutPostTokens(t *testing.T) {
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "session.jsonl")
	transcript := strings.Join([]string{
		`{"type":"assistant","message":{"model":"claude-opus-4-1","usage":{"input_tokens":1000,"cache_read_input_tokens":699000}}}`,
		`{"type":"system","subtype":"compact_boundary","compactMetadata":{}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	encodedPath, err := json.Marshal(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Render(context.Background(), []byte(`{
  "model":{"id":"claude-opus-4-1","display_name":"Opus 4"},
  "cwd":"/work/sample",
  "context_window":{"used_percentage":77},
  "transcript_path":`+string(encodedPath)+`
}`), Runtime{
		Home: root, CacheDir: filepath.Join(root, "cache"), TmuxDir: filepath.Join(root, "tmux"),
		ProcRoot: filepath.Join(root, "proc"), Columns: 120, UID: 1000, Env: map[string]string{},
		Command: quietRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	plain := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(got, "")
	if !strings.Contains(plain, "~70%") || strings.Contains(plain, "77%") {
		t.Fatalf("compact boundary without postTokens did not mark the prior occupancy as estimated:\n%q", plain)
	}
}

func TestContextFloorKeyMatchesRetiredOverlay(t *testing.T) {
	if got := sanitizeProject(""); got != "root" {
		t.Fatalf("empty project key = %q, want root", got)
	}
	if got := sanitizeProject("/work/acme.api_v2"); got != "work-acme-api-v2" {
		t.Fatalf("sanitized project key = %q", got)
	}
	if got := sanitizeProject("/" + strings.Repeat("a", 100)); len(got) != 80 {
		t.Fatalf("long project key length = %d, want 80", len(got))
	}
	window := contextWindow(
		Runtime{Env: map[string]string{}, Engine: pfmengine.Claude},
		input{Model: struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		}{ID: "claude-haiku-4", DisplayName: "Claude"}},
		transcript.Meta{},
	)
	if window != 200_000 {
		t.Fatalf("Haiku model-id window = %d, want 200000", window)
	}
}

func TestRenderUsesMeasuredTranscriptAndCachesFloorWithPromptCount(t *testing.T) {
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "session.jsonl")
	transcript := strings.Join([]string{
		`{"type":"user","message":{"content":"first"}}`,
		`{"type":"assistant","isSidechain":false,"message":{"model":"claude-opus-4-1","usage":{"input_tokens":1000,"cache_read_input_tokens":249000}}}`,
		`{"type":"user","message":{"content":"second"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	encodedPath, err := json.Marshal(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Render(context.Background(), []byte(`{
  "model":{"display_name":"Opus 4"},
  "cwd":"/work/sample",
  "context_window":{"used_percentage":77,"current_usage":{"input_tokens":1000}},
  "transcript_path":`+string(encodedPath)+`
}`), Runtime{
		Home: root, CacheDir: filepath.Join(root, "cache"), TmuxDir: filepath.Join(root, "tmux"),
		ProcRoot: filepath.Join(root, "proc"), Columns: 120, UID: 1000, Env: map[string]string{},
		Command: quietRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	plain := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(got, "")
	if !strings.Contains(plain, "25%") || strings.Contains(plain, "77%") || !strings.Contains(plain, "🧮1.0K ✎2") {
		t.Fatalf("measured transcript gauge or prompt count missing:\n%q", plain)
	}
	floor, err := os.ReadFile(filepath.Join(root, ".cache", "pfm-statusline", "claude-work-sample.txt"))
	if err != nil || string(floor) != "250000\n" {
		t.Fatalf("cached floor = %q, %v; want 250000", floor, err)
	}
}

func TestDefaultUnknownCacheWindowRendersInfinity(t *testing.T) {
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "session.jsonl")
	if err := os.WriteFile(
		transcriptPath,
		[]byte(`{"type":"assistant","message":{"usage":{"input_tokens":1000}}}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	segment := cacheWindowSegment(
		Runtime{Home: root, CacheDir: filepath.Join(root, "cache")},
		time.Now(),
		transcriptPath,
	)
	plain := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(segment, "")
	if !strings.Contains(plain, "💾1h∞") || strings.Contains(plain, "1h?") {
		t.Fatalf("unknown default cache window = %q, want 1h∞", plain)
	}
}

func TestCodexSegmentDoesNotOverwriteTranscriptGauge(t *testing.T) {
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "rollout.jsonl")
	transcript := `{"payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":68000},"model_context_window":272000}}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	encodedPath, err := json.Marshal(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Render(context.Background(), []byte(`{
  "context_window":{"used_percentage":77,"current_usage":{"input_tokens":136000}},
  "transcript_path":`+string(encodedPath)+`
}`), Runtime{
		Home: root, CacheDir: filepath.Join(root, "cache"), TmuxDir: filepath.Join(root, "tmux"),
		ProcRoot: filepath.Join(root, "proc"), Columns: 120, UID: 1000, Engine: pfmengine.Codex,
		Env: map[string]string{"ANTHROPIC_MODEL": "gpt-5.6-sol"}, Command: quietRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	plain := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(got, "")
	if !strings.Contains(plain, "25% of 272.0K") || strings.Contains(plain, "50%") || strings.Contains(plain, "77%") {
		t.Fatalf("Codex overwrote transcript gauge:\n%q", plain)
	}
}

func TestCodexEngineOwnsCodexUsageIndependentOfAccountID(t *testing.T) {
	root := t.TempDir()
	input := []byte(`{
  "model":{"display_name":"Claude"},
  "workspace":{"current_dir":"/work/sample"},
  "context_window":{"used_percentage":10,"current_usage":{"input_tokens":136000}},
  "cost":{"total_cost_usd":99,"total_duration_ms":1000}
}`)
	got, err := Render(context.Background(), input, Runtime{
		Now:       func() time.Time { return time.Unix(1_786_838_400, 0) },
		Home:      root,
		ConfigDir: filepath.Join(root, ".cc", "2"),
		CacheDir:  filepath.Join(root, "cache"),
		TmuxDir:   filepath.Join(root, "tmux"),
		ProcRoot:  filepath.Join(root, "proc"),
		Columns:   120,
		UID:       1000,
		Engine:    pfmengine.Codex,
		Env:       map[string]string{"ANTHROPIC_MODEL": "gpt-5.6-sol[1m]"},
		Command:   quietRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "🥈 ") ||
		!strings.Contains(got, "🍀 gpt-5.6-sol") ||
		!strings.Contains(got, "50%") ||
		strings.Contains(got, "💰$99.00") {
		t.Fatalf("Codex-engine rendering drifted:\n%q", got)
	}
}

func TestFleetSnapshotCountsOnlySocketsPresentInProcOnLinux(t *testing.T) {
	root := t.TempDir()
	tmuxDir := filepath.Join(root, "tmux")
	procNet := filepath.Join(root, "proc", "net")
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(procNet, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"probe-cc-live", "probe-cc-grave", "probe-cx-live", "probe-ox-live"} {
		if err := os.WriteFile(filepath.Join(tmuxDir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	proc := "Num RefCount Protocol Flags Type St Inode Path\n" +
		"0: 2 0 00010000 0001 01 1 " + filepath.Join(tmuxDir, "probe-cc-live") + "\n" +
		"1: 2 0 00010000 0001 01 2 " + filepath.Join(tmuxDir, "probe-cx-live") + "\n" +
		"2: 2 0 00010000 0001 01 3 " + filepath.Join(tmuxDir, "probe-ox-live") + "\n"
	if err := os.WriteFile(filepath.Join(procNet, "unix"), []byte(proc), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Render(context.Background(), []byte(`{"model":{"display_name":"Claude"}}`), Runtime{
		Home: root, ConfigDir: filepath.Join(root, ".cc", "1"), CacheDir: filepath.Join(root, "cache"),
		TmuxDir: tmuxDir, ProcRoot: filepath.Join(root, "proc"), UID: 1000,
		Env: map[string]string{"PFM_TEST_PROBE_SOCKETS": "1"}, Command: quietRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	plain := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(got, "")
	if !strings.Contains(plain, "·1 ·1 ·1") || strings.Contains(plain, "·2 ·1 ·1") {
		t.Fatalf("graveyard socket affected live fleet count: %q", plain)
	}
}

// TestCacheWindowSaysSoWhenTheTranscriptCannotBeRead pins the one thing this
// segment must never do: report "I could not measure the cache window" with the
// same pixels as "this statusline has no cache window". Both halves matter, so
// the readable case is asserted alongside the broken ones — a marker that is
// always on says nothing either.
func TestCacheWindowSaysSoWhenTheTranscriptCannotBeRead(t *testing.T) {
	root := t.TempDir()
	now := time.Unix(1_786_838_400, 0)
	live := filepath.Join(root, "live.jsonl")
	turn := `{"type":"user","timestamp":"` +
		now.Add(-2*time.Minute).UTC().Format(time.RFC3339Nano) + `"}` + "\n"
	if err := os.WriteFile(live, []byte(turn), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, testcase := range []struct {
		name    string
		path    string
		columns int
		want    string
	}{
		{name: "a readable transcript anchors the window", path: live, want: "💾5m✓3m:0s"},
		{name: "a path with no file behind it", path: filepath.Join(root, "gone.jsonl"), want: "💾5m!"},
		{name: "no path at all", path: "", want: "💾5m!"},
		{name: "a path that is a directory", path: root, want: "💾5m!"},
		{name: "a narrow pane keeps the timer", path: live, columns: 97, want: "💾5m✓3m:0s"},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			columns := testcase.columns
			if columns == 0 {
				columns = 120
			}
			cacheDir := filepath.Join(t.TempDir(), "cache")
			if err := os.MkdirAll(cacheDir, 0o700); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(testcase.path)
			if err != nil {
				t.Fatal(err)
			}
			input := []byte(`{"model":{"display_name":"Opus 4"},` +
				`"workspace":{"current_dir":"/work/sample"},` +
				`"context_window":{"used_percentage":10},` +
				`"transcript_path":` + string(encoded) + `}`)
			got, err := Render(context.Background(), input, Runtime{
				Now:      func() time.Time { return now },
				Home:     root,
				CacheDir: cacheDir,
				TmuxDir:  filepath.Join(root, "tmux"),
				ProcRoot: filepath.Join(root, "proc"),
				Columns:  columns,
				UID:      1000,
				Env:      map[string]string{"FORCE_PROMPT_CACHING_5M": "1"},
				Command:  quietRunner{},
			})
			if err != nil {
				t.Fatal(err)
			}
			plain := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(got, "")
			if !strings.Contains(plain, testcase.want) {
				t.Fatalf("cache window lacks %q:\n%q", testcase.want, plain)
			}
		})
	}
}

func TestCodexAuthRejectStreakIsTheLastThreeCompletedRequests(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, ".local", "state", "claude-code-proxy")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	log := strings.Join([]string{
		`{"msg":"request_completed","t":"2026-08-16T01:00:00Z","status":401}`,
		`{"msg":"upstream_diagnostic","t":"2026-08-16T02:00:00Z","status":403}`,
		`{"msg":"request_completed","t":"2026-08-16T03:00:00Z","status":401}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(logDir, "proxy.log"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	count, reject := codexRequestCount(Runtime{
		Now:  func() time.Time { return time.Date(2026, 8, 16, 5, 0, 0, 0, time.UTC) },
		Home: root, CacheDir: filepath.Join(root, "cache"),
	}, time.Date(2026, 8, 16, 5, 0, 0, 0, time.UTC))
	if count != 2 || reject {
		t.Fatalf("count=%d reject=%v; non-request rows must not manufacture a three-request streak", count, reject)
	}
}

// The scoped Fable window is resolved into data.RateLimits.Windows before
// harvestRateLimits runs, but the snapshot writer only ever copied the two
// flat windows out of that map. A host whose Limits tab reads nothing BUT
// this snapshot (credentials in the OS keychain, so no usage-API path) could
// therefore never show Fable, however faithfully it was rendered inline.
func TestStatuslineQuotaSnapshotCarriesTheScopedFableWindow(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, ".cc", "2")
	rateDir := filepath.Join(root, "rates")
	now := time.Now().Truncate(time.Second)
	fableResets := now.Add(5 * 24 * time.Hour).UTC()
	runtime := Runtime{
		Now: func() time.Time { return now }, Home: root, ConfigDir: configDir,
		CacheDir: filepath.Join(root, "cache"), RateLimitDir: rateDir,
		SIDDir: filepath.Join(root, "sid"), TmuxDir: filepath.Join(root, "tmux"),
		ProcRoot: filepath.Join(root, "proc"), Columns: 120, UID: 1000,
		AccountDirs: map[string]int{configDir: 2}, Env: map[string]string{}, Command: quietRunner{},
	}
	input := []byte(fmt.Sprintf(`{
  "model":{"display_name":"Fable 5.1"},
  "session_id":"fable-session",
  "rate_limits":{
    "five_hour":{"used_percentage":31,"resets_at":%d},
    "seven_day":{"used_percentage":47,"resets_at":%d},
    "limits":[{"kind":"weekly_scoped","scope":{"model":{"display_name":"Fable"}},"percent":62,"resets_at":%q,"is_active":true}]
  }
}`, now.Add(4*time.Hour).Unix(), now.Add(6*24*time.Hour).Unix(), fableResets.Format(time.RFC3339)))
	if _, err := Render(context.Background(), input, runtime); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(rateDir, "acct-2.fable-session.json"))
	if err != nil {
		t.Fatalf("read account 2 quota snapshot: %v", err)
	}
	var snapshot struct {
		FiveHourUsed  int64 `json:"five_hour_used"`
		FableUsed     int64 `json:"fable_used"`
		FableResetsAt int64 `json:"fable_resets_at"`
	}
	if err := json.Unmarshal(body, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.FiveHourUsed != 31 || snapshot.FableUsed != 62 || snapshot.FableResetsAt != fableResets.Unix() {
		t.Fatalf("snapshot=%#v, want five_hour 31 and Fable 62 resetting at %d", snapshot, fableResets.Unix())
	}
}
