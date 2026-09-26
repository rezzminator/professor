package statusline

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func cacheLaunch(t *testing.T, home, session string, oneHour bool) {
	t.Helper()
	values := paths.Values{StateDB: paths.DefaultStateDB(home)}
	if err := fleetdb.RecordLaunch(context.Background(), values, fleetdb.Launch{
		SessionID: session, Engine: pfmengine.Claude, Account: 2, Cache1H: oneHour,
	}, 1); err != nil {
		t.Fatal(err)
	}
}

func TestStatuslineFiveMinuteWindowUsesLaunchAndNewestRequest(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 2, 0, 0, time.UTC)
	path := writeTranscript(
		t,
		`{"type":"user","timestamp":"2026-09-02T10:00:00Z","message":{"content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-09-02T10:00:30Z","message":{"usage":{"cache_creation":{"ephemeral_1h_input_tokens":100}}}}`,
	)
	home := t.TempDir()
	cacheLaunch(t, home, "S", false)
	runtime := Runtime{
		Home:     home,
		CacheDir: filepath.Join(home, "cache"),
		Env:      map[string]string{},
	}
	got := stripANSICodes(cacheWindowSegment(runtime, now, path, -1, nil, "S"))
	if !strings.Contains(got, "💾5m✓3m:0s") {
		t.Fatalf("window = %q, want the launch's 5m TTL from newest request", got)
	}
}

// The payload's own TTL labels its expiry; the launch record labels it only
// when the payload names no TTL.
func TestStatuslineHarnessExpiryUsesItsOwnLabelElseRecord(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 2, 0, 0, time.UTC)
	home := t.TempDir()
	cacheLaunch(t, home, "S", false)
	expires := now.Add(2 * time.Minute).Unix()
	got := stripANSICodes(cacheWindowSegment(Runtime{Home: home, Env: map[string]string{}}, now, "", -1,
		&promptCache{TTL: "1h", ExpiresAt: &expires}, "S"))
	if !strings.Contains(got, "💾1h✓2m:0s") {
		t.Fatalf("window = %q, want harness expiry with its own 1h label", got)
	}
	got = stripANSICodes(cacheWindowSegment(Runtime{Home: home, Env: map[string]string{}}, now, "", -1,
		&promptCache{ExpiresAt: &expires}, "S"))
	if !strings.Contains(got, "💾5m✓2m:0s") {
		t.Fatalf("window = %q, want harness expiry with the launch's 5m label", got)
	}
}

func TestStatuslineNoLaunchHidesCacheWindow(t *testing.T) {
	home := t.TempDir()
	got := cacheWindowSegment(Runtime{Home: home, Env: map[string]string{}}, time.Now(), "", -1, nil, "S")
	if got != "" {
		t.Fatalf("unlaunched session window = %q, want no segment", got)
	}
	// A transcript whose writes name no cache length leaves no marker either.
	path := writeTranscript(
		t,
		`{"type":"user","timestamp":"2026-09-02T10:00:00.000Z","message":{"role":"user","content":"go"}}`,
	)
	runtime := Runtime{Home: home, CacheDir: filepath.Join(home, "cache"), Env: map[string]string{}}
	if got := cacheWindowSegment(runtime, time.Now(), path, 94, nil, "S"); got != "" {
		t.Fatalf("unlaunched session without a cache write = %q, want no segment", got)
	}
}

func TestStatuslineLaunchReadErrorShowsWarning(t *testing.T) {
	home := t.TempDir()
	path := paths.DefaultStateDB(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = writer
	t.Cleanup(func() { os.Stderr = previous })
	got := cacheWindowSegment(Runtime{Home: home, Env: map[string]string{}}, time.Now(), "", -1, nil, "S")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stderr = previous
	logged, err := io.ReadAll(reader)
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "💾⚠") || !strings.Contains(got, cBad) {
		t.Fatalf("read error window = %q, want bad-colour warning", got)
	}
	if !strings.Contains(string(logged), "launch record S") || !strings.Contains(string(logged), "not a database") {
		t.Fatalf("stderr = %q, want the launch read cause", logged)
	}
}

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "11111111-1111-4111-8111-111111111111.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The prompt-cache TTL is refreshed by API requests, and a local slash command
// (/rc, /cost, /compact's receipt …) writes user-typed records without making
// one. A chat that finished at 01:50 and had /rc run at 14:20 was shown
// "💾1h✗9m" — the anchor had jumped to the local command — when the cache had
// been cold for twelve hours. The anchor is the newest record that sent a
// request: a user record — prompt or tool result — that is not a local command.
func TestCacheAnchorIgnoresLocalCommandRecords(t *testing.T) {
	path := writeTranscript(
		t,
		`{"type":"user","timestamp":"2026-09-01T23:48:00.000Z","message":{"role":"user","content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-09-01T23:50:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`,
		`{"type":"assistant","isSidechain":true,"timestamp":"2026-09-02T03:00:00.000Z","message":{"role":"assistant","content":[]}}`,
		`{"type":"user","timestamp":"2026-09-02T12:20:00.000Z","message":{"role":"user","content":"<local-command-caveat>Caveat: The messages below were generated by the user while running local commands.</local-command-caveat>"}}`,
		`{"type":"user","timestamp":"2026-09-02T12:20:00.500Z","message":{"role":"user","content":"<command-name>/rc</command-name>"}}`,
		`{"type":"user","timestamp":"2026-09-02T12:20:01.000Z","message":{"role":"user","content":"<local-command-stdout>connecting…</local-command-stdout>"}}`,
	)
	want := time.Date(2026, 9, 1, 23, 48, 0, 0, time.UTC)
	if got := cacheAnchor(path).anchor; !got.Equal(want) {
		t.Fatalf(
			"cacheAnchor = %s, want the last request %s (local-command user records must not anchor the TTL)",
			got,
			want,
		)
	}
}

func TestCacheAnchorFollowsTheNewestRequestInAnOpenTurn(t *testing.T) {
	path := writeTranscript(
		t,
		`{"type":"assistant","timestamp":"2026-09-02T10:00:00.000Z","message":{"role":"assistant","content":[]}}`,
		`{"type":"user","timestamp":"2026-09-02T10:07:00.000Z","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`,
	)
	want := time.Date(2026, 9, 2, 10, 7, 0, 0, time.UTC)
	if got := cacheAnchor(path).anchor; !got.Equal(want) {
		t.Fatalf("cacheAnchor = %s, want the tool-result request %s", got, want)
	}
	fresh := writeTranscript(t,
		`{"type":"user","timestamp":"2026-09-02T10:00:00.000Z","message":{"role":"user","content":"first prompt"}}`,
	)
	if got := cacheAnchor(fresh).anchor; !got.Equal(time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("a chat with only its first prompt in flight anchors on that prompt, got %s", got)
	}
	if got := cacheAnchor(writeTranscript(t, `{"type":"summary","summary":"nothing"}`)).anchor; !got.IsZero() {
		t.Fatalf("a transcript with no request must yield the zero anchor, got %s", got)
	}
}

// The cache is refreshed when a request is SENT; the assistant record that
// answers it is stamped after the reply streamed. A 3-minute reply anchored on
// its assistant record showed 3 minutes more cache than there was.
func TestCacheAnchorIsTheRequestNotTheReply(t *testing.T) {
	path := writeTranscript(
		t,
		`{"type":"user","timestamp":"2026-09-02T10:00:00.000Z","message":{"role":"user","content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-09-02T10:03:00.000Z","message":{"role":"assistant","content":[]}}`,
	)
	if got := cacheAnchor(path).anchor; !got.Equal(time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("cacheAnchor = %s, want the request at 10:00, not the reply at 10:03", got)
	}
	replies := writeTranscript(t,
		`{"type":"assistant","timestamp":"2026-09-02T11:00:00.000Z","message":{"role":"assistant","content":[]}}`,
	)
	if got := cacheAnchor(replies).anchor; !got.Equal(time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("a transcript with no request record anchors on its newest reply, got %s", got)
	}
}

// The window's length is what the newest cache write used, read from its
// usage — a chat assumed 1h ran 5m for six hours before this.
func TestCacheWindowLengthComesFromTheNewestWrite(t *testing.T) {
	usage := func(fiveMinutes, oneHour int) string {
		created := fmt.Sprintf(`{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}`, fiveMinutes, oneHour)
		return `{"type":"assistant","timestamp":"2026-09-02T10:00:01.000Z","message":{"role":"assistant","content":[],` +
			`"usage":{"cache_creation":` + created + `}}}`
	}
	request := `{"type":"user","timestamp":"2026-09-02T10:00:00.000Z","message":{"role":"user","content":"go"}}`
	for _, tc := range []struct {
		name  string
		lines []string
		want  time.Duration
	}{
		{"a 1h write", []string{request, usage(0, 4193)}, time.Hour},
		{"a 5m write after a 1h one", []string{request, usage(0, 4193), usage(12709, 0)}, 5 * time.Minute},
		{"a pure read keeps the last write's length", []string{request, usage(12709, 0), usage(0, 0)}, 5 * time.Minute},
		{"no breakdown recorded", []string{request}, 0},
	} {
		if got := cacheAnchor(writeTranscript(t, tc.lines...)).ttl; got != tc.want {
			t.Fatalf("%s: ttl = %s, want %s", tc.name, got, tc.want)
		}
	}
}

// The launch decides the window even when the transcript's last write used
// the other TTL, and the segment still carries the last call's hit share.
func TestCacheWindowSegmentUsesLaunchLengthAndWrite(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 2, 0, 0, time.UTC)
	path := writeTranscript(
		t,
		`{"type":"user","timestamp":"2026-09-02T10:00:00.000Z","message":{"role":"user","content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-09-02T10:00:30.000Z","message":{"role":"assistant","content":[],`+
			`"usage":{"cache_creation":{"ephemeral_5m_input_tokens":4200,"ephemeral_1h_input_tokens":0}}}}`,
	)
	root := t.TempDir()
	cacheLaunch(t, root, "S", false)
	runtime := Runtime{Home: root, CacheDir: filepath.Join(root, "cache"), Env: map[string]string{}}
	got := stripANSICodes(cacheWindowSegment(runtime, now, path, 94, nil, "S"))
	if !strings.Contains(got, "💾5m✓3m:0s 94%") {
		t.Fatalf("segment = %q, want the measured 5m window and 94%%", got)
	}
	cold := cacheWindowSegment(runtime, now, path, cacheHitPercent(27_615, 207_108, 2), nil, "S")
	if !strings.Contains(cold, cBad+"11%") {
		t.Fatalf("a full re-write must read red: %q", cold)
	}
}

// Claude Code measures the cache from its own requests and sends the expiry in
// the payload's prompt_cache; when present it wins over the transcript, which
// here claims a different TTL and an older request.
func TestCacheWindowPrefersTheHarnessExpiry(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 2, 0, 0, time.UTC)
	path := writeTranscript(
		t,
		`{"type":"user","timestamp":"2026-09-02T09:00:00.000Z","message":{"role":"user","content":"go"}}`,
	)
	root := t.TempDir()
	cacheLaunch(t, root, "S", true)
	runtime := Runtime{Home: root, CacheDir: filepath.Join(root, "cache"), Env: map[string]string{}}
	var payload input
	raw := `{"prompt_cache":{"warm":true,"ttl":"1h","expires_at":` + jsonText(now.Add(59*time.Minute).Unix()) + `}}`
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	got := stripANSICodes(cacheWindowSegment(runtime, now, path, 99, payload.PromptCache, "S"))
	if !strings.Contains(got, "💾1h✓59m:0s 99%") {
		t.Fatalf("segment = %q, want the harness's 1h window with 59m left", got)
	}
	lapsed := jsonText(now.Add(-2 * time.Minute).Unix())
	expired := `{"prompt_cache":{"warm":false,"ttl":"5m","expires_at":` + lapsed + `}}`
	payload = input{}
	if err := json.Unmarshal([]byte(expired), &payload); err != nil {
		t.Fatal(err)
	}
	got = stripANSICodes(cacheWindowSegment(runtime, now, path, -1, payload.PromptCache, "S"))
	if !strings.Contains(got, "💾5m✗2m:0s") {
		t.Fatalf("segment = %q, want the harness expiry and its own 5m label", got)
	}
	noExpiry := &promptCache{TTL: "5m"}
	got = stripANSICodes(cacheWindowSegment(runtime, now, path, -1, noExpiry, "S"))
	if strings.Contains(got, "5m✓") {
		t.Fatalf("a prompt_cache without expiry must fall back to the transcript, got %q", got)
	}
}

// A lapsed window's hit rate is history: the last call read it while the
// cache was warm, and the next call will not. It renders as "was N%" in the
// muted colour, on the main line and on a sub-agent row alike.
func TestLapsedCacheWindowMarksTheHitAsPast(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 10, 0, 0, time.UTC)
	path := writeTranscript(
		t,
		`{"type":"user","timestamp":"2026-09-02T10:00:00.000Z","message":{"role":"user","content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-09-02T10:00:30.000Z","message":{"role":"assistant","content":[],`+
			`"usage":{"cache_creation":{"ephemeral_5m_input_tokens":4200,"ephemeral_1h_input_tokens":0}}}}`,
	)
	root := t.TempDir()
	runtime := Runtime{Home: root, CacheDir: filepath.Join(root, "cache"), Env: map[string]string{}}
	expires := now.Add(-4 * time.Minute).Unix()
	harness := &promptCache{TTL: "5m", ExpiresAt: &expires}

	main := cacheWindowSegment(runtime, now, path, 99, harness, "S")
	if got := stripANSICodes(main); !strings.Contains(got, "💾5m✗4m:0s was 99%") {
		t.Fatalf("harness segment = %q, want the lapsed hit as was 99%%", got)
	}
	if !strings.Contains(main, cMuted+"was 99%") {
		t.Fatalf("a lapsed hit must render muted, not as live health: %q", main)
	}
	transcript := stripANSICodes(cacheWindowSegment(runtime, now, path, 99, nil, "S"))
	if got := transcript; !strings.Contains(got, "💾5m✗5m:0s was 99%") {
		t.Fatalf("transcript segment = %q, want the lapsed hit as was 99%%", got)
	}
	if got := stripANSICodes(agentCacheText(path, 94, now)); !strings.Contains(got, "💾5m✗5m:0s was 94%") {
		t.Fatalf("agent row = %q, want the lapsed hit as was 94%%", got)
	}
	live := stripANSICodes(cacheWindowSegment(runtime, now.Add(-9*time.Minute), path, 99, nil, "S"))
	if !strings.Contains(live, "✓") || !strings.HasSuffix(live, " 99%") || strings.Contains(live, "was") {
		t.Fatalf("a live window keeps the bare hit: %q", live)
	}
}

// configRuntime loads the machine runtime the way pfm's entry does — through
// --config when flagPath is set, else PFM_CONFIG=envPath — and hands it to a
// closed statusline runtime, as the statusline command does.
func configRuntime(t *testing.T, home, flagPath, envPath, stateEnv string) Runtime {
	t.Helper()
	t.Setenv(paths.EnvHome, home)
	t.Setenv(paths.EnvConfig, envPath)
	t.Setenv(paths.EnvStateDB, stateEnv)
	machine, err := pfmconfig.LoadDiagnosticRuntime(flagPath)
	if err != nil {
		t.Fatal(err)
	}
	runtime := Runtime{Home: home, Env: map[string]string{}}
	runtime.UseConfig(machine)
	return runtime
}

func launchAt(t *testing.T, stateDB, session string, oneHour bool) {
	t.Helper()
	if err := fleetdb.RecordLaunch(context.Background(), paths.Values{StateDB: stateDB}, fleetdb.Launch{
		SessionID: session, Engine: pfmengine.Claude, Account: 2, Cache1H: oneHour,
	}, 1); err != nil {
		t.Fatal(err)
	}
}

func writeStateConfig(t *testing.T, stateDB string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pfm.config.json")
	body := fmt.Sprintf(`{"version":2,"state":{"db":%q}}`, stateDB)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStatuslineCacheWindowReadsConfigStateDB(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 2, 0, 0, time.UTC)
	expires := now.Add(2 * time.Minute).Unix()
	for _, door := range []string{"--config", "PFM_CONFIG"} {
		t.Run(door, func(t *testing.T) {
			home := t.TempDir()
			stateDB := filepath.Join(t.TempDir(), "x", "pfm.db")
			launchAt(t, stateDB, "S", true)
			config := writeStateConfig(t, stateDB)
			flagPath, envPath := config, ""
			if door == "PFM_CONFIG" {
				flagPath, envPath = "", config
			}
			runtime := configRuntime(t, home, flagPath, envPath, "")
			got := stripANSICodes(cacheWindowSegment(runtime, now, "", -1, &promptCache{ExpiresAt: &expires}, "S"))
			if !strings.Contains(got, "💾1h✓2m:0s") {
				t.Fatalf("window = %q, want the 1h launch recorded in config state.db %s", got, stateDB)
			}
		})
	}
}

func TestStatuslineCacheWindowStateDBEnvOverridesConfig(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 2, 0, 0, time.UTC)
	expires := now.Add(2 * time.Minute).Unix()
	home := t.TempDir()
	configDB := filepath.Join(t.TempDir(), "config", "pfm.db")
	envDB := filepath.Join(t.TempDir(), "env", "pfm.db")
	launchAt(t, configDB, "S", false)
	launchAt(t, envDB, "S", true)
	runtime := configRuntime(t, home, "", writeStateConfig(t, configDB), envDB)
	got := stripANSICodes(cacheWindowSegment(runtime, now, "", -1, &promptCache{ExpiresAt: &expires}, "S"))
	if !strings.Contains(got, "💾1h✓2m:0s") {
		t.Fatalf("window = %q, want the 1h launch from PFM_STATE_DB %s over config %s", got, envDB, configDB)
	}
}

func TestStatuslineCacheWindowShowsConfigError(t *testing.T) {
	for name, write := range map[string]func(string) error{
		"malformed":  func(path string) error { return os.WriteFile(path, []byte("this is [not a config\n"), 0o600) },
		"unreadable": func(path string) error { return os.Mkdir(path, 0o700) },
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(t.TempDir(), "pfm.config.json")
			if err := write(config); err != nil {
				t.Fatal(err)
			}
			runtime := configRuntime(t, home, "", config, "")
			if runtime.ConfigError == nil {
				t.Fatalf("config %s loaded without error; the fixture is not %s", config, name)
			}
			got := stripANSICodes(cacheWindowSegment(runtime, time.Now(), "", -1, nil, "S"))
			if !strings.Contains(got, "💾⚠config") {
				t.Fatalf("window = %q, want a visible config error, never an absent segment", got)
			}
		})
	}
}
