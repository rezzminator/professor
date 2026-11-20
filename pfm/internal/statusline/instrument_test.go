package statusline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// The agent row's clock, row for row (plugins/professor/tests/text.test.ts).
func TestSpanTextIsTheAgentRowsClock(t *testing.T) {
	for _, tc := range []struct {
		span time.Duration
		want string
	}{
		{0, "0s"},
		{59_999 * time.Millisecond, "59s"},
		{60 * time.Second, "1m00s"},
		{188 * time.Second, "3m08s"},
		{3_599_999 * time.Millisecond, "59m59s"},
		{time.Hour, "1h00m"},
		{64 * time.Minute, "1h04m"},
		{-5 * time.Second, "0s"},
	} {
		if got := spanText(tc.span); got != tc.want {
			t.Errorf("spanText(%v) = %q, want %q", tc.span, got, tc.want)
		}
	}
}

func TestShareToneTurnsAtHalfAndFourFifths(t *testing.T) {
	for _, tc := range []struct {
		percent int
		want    string
	}{{0, cGood}, {49, cGood}, {50, cWarn}, {79, cWarn}, {80, cBad}, {104, cBad}} {
		if got := shareTone(tc.percent); got != tc.want {
			t.Errorf("shareTone(%d) = %q, want %q", tc.percent, got, tc.want)
		}
	}
}

// Line 2 gives its parts up in the agent row's order as the terminal narrows:
// the spend's words, the growth figure, the spend's counts, the pulse beside
// the gauge, the tokens over the window. Each rank is drawn at the narrowest width
// that holds it and the next at one column less; the cache window never goes.
func TestInstrumentLineGivesUpPartsAsTheTerminalNarrows(t *testing.T) {
	const (
		// The context block's form comes from contextForms by the terminal's width alone.
		full        = "🟢 ▰▱▱▱▱▱▱▱ ·····•   +3.0K  17%  82.5K/500.0K │ ⏳ 8m12s │ Σ $1.50 · 82.5K in · 0 out · 0 tools API-eq │ 💾∅ 60%"
		pulseWords  = "🟢 ▰▱▱▱▱▱▱▱ ······• 17%  82.5K/500.0K │ ⏳ 8m12s │ Σ $1.50 · 82.5K in · 0 out · 0 tools API-eq │ 💾∅ 60%"
		pulseSlash  = "🟢 ▰▱▱▱▱▱▱▱ ······• 17%  82.5K/500.0K │ ⏳ 8m12s │ Σ $1.50/82.5K/0/0 API-eq │ 💾∅ 60%"
		windowSlash = "🟢 ▰▱▱▱▱▱▱▱  17%  82.5K/500.0K │ ⏳ 8m12s │ Σ $1.50/82.5K/0/0 API-eq │ 💾∅ 60%"
		gaugeWords  = "🟢 ▰▱▱▱▱▱▱▱  17% │ ⏳ 8m12s │ Σ $1.50 · 82.5K in · 0 out · 0 tools API-eq │ 💾∅ 60%"
		gaugeSlash  = "🟢 ▰▱▱▱▱▱▱▱  17% │ ⏳ 8m12s │ Σ $1.50/82.5K/0/0 API-eq │ 💾∅ 60%"
		// Then the blanks around each separator, then the facts.
		tight     = "🟢 ▰▱▱▱▱▱▱▱  17%│⏳ 8m12s│Σ $1.50/82.5K/0/0 API-eq│💾∅ 60%"
		dollars   = "🟢 ▰▱▱▱▱▱▱▱  17%│⏳ 8m12s│Σ $1.50 API-eq│💾∅ 60%"
		noClock   = "🟢 ▰▱▱▱▱▱▱▱  17%│Σ $1.50 API-eq│💾∅ 60%"
		cacheTail = "🟢 ▰▱▱▱▱▱▱▱  17%│💾∅ 60%"
		// At rest the pulse runs over the growth figure's blank cells and the share's pad to one blank
		// before the share, which stays on the cells it holds beside a growth figure.
		restFull  = "🟢 ▰▱▱▱▱▱▱▱ ··············· 17%  82.5K/500.0K │ ⏳ 8m12s │ Σ $1.50 · 82.5K in · 0 out · 0 tools API-eq │ 💾∅ 60%"
		restWords = "🟢 ▰▱▱▱▱▱▱▱ ······· 17%  82.5K/500.0K │ ⏳ 8m12s │ Σ $1.50 · 82.5K in · 0 out · 0 tools API-eq │ 💾∅ 60%"
		restSlash = "🟢 ▰▱▱▱▱▱▱▱ ······· 17%  82.5K/500.0K │ ⏳ 8m12s │ Σ $1.50/82.5K/0/0 API-eq │ 💾∅ 60%"
		growing   = 79_500
		resting   = 82_500
	)
	cases := []struct {
		columns int
		before  int64
		want    string
	}{
		{0, resting, restFull},
		{120, resting, restFull},
		{119, resting, restWords},
		{100, resting, restSlash},
		{99, resting, windowSlash},
		{0, growing, full},
		{120, growing, full},
		{119, growing, pulseWords},
		{108, growing, pulseWords},
		{107, growing, pulseSlash},
		{100, growing, pulseSlash},
		{99, growing, windowSlash},
		{90, growing, windowSlash},
		{89, growing, gaugeWords},
		{87, growing, gaugeWords},
		{86, growing, gaugeSlash},
		{68, growing, gaugeSlash},
		{67, growing, tight},
		{62, growing, tight},
		{61, growing, dollars},
		{52, growing, dollars},
		{51, growing, noClock},
		{43, growing, noClock},
		{42, growing, cacheTail},
		{28, growing, cacheTail},
		{20, growing, cacheTail},
	}
	for _, tc := range cases {
		runtime := parityRuntime(t)
		runtime.Columns = tc.columns
		var data input
		if err := json.Unmarshal([]byte(`{"session_id":"session-1","model":{"id":"gpt-6.1-sol-fast[1m]"},`+
			`"cost":{"total_cost_usd":1.5,"total_duration_ms":492000},"context_window":{"context_window_size":1000000,`+
			`"used_percentage":16.5,"current_usage":{"input_tokens":165000}}}`), &data); err != nil {
			t.Fatal(err)
		}
		data.TranscriptPath = filepath.Join(runtime.Home, "main.jsonl")
		// 82,500 of 500,000 tokens is 16.5%: one cell of the gauge's eight.
		turn := `{"type":"assistant","message":{"model":"gpt-6.1-sol","usage":{"input_tokens":32500,` +
			`"cache_read_input_tokens":50000}}}` + "\n"
		if err := os.WriteFile(data.TranscriptPath, []byte(turn), 0o600); err != nil {
			t.Fatal(err)
		}
		// The step before this render read 79,500 tokens, the newest cell growing by 3,000, or 82,500, at rest.
		before := `[{"at":1799999995000,"tokens":` + strconv.FormatInt(tc.before, 10) + `}]`
		readings := filepath.Join(runtime.CacheDir, "cc-sl-pulse-session-1")
		if err := os.WriteFile(readings, []byte(before), 0o600); err != nil {
			t.Fatal(err)
		}
		_, line := instrumentLine(runtime, data, "", runtime.now())
		if got := stripANSICodes(line); got != tc.want {
			t.Errorf("at %d columns, %d tokens before, line = %q, want %q", tc.columns, tc.before, got, tc.want)
		}
	}
}

// The context's figures in the colours the agent row wears: a step that shed
// tokens is a drop beside its size, and Codex counts the human prompts.
func TestContextColumnWritesTheStripsFigures(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, tc := range []struct {
		name     string
		engine   pfmengine.ID
		gauge    contextGauge
		readings []pulseReading
		form     ContextForm
		want     string
	}{
		{
			name:   "a context that shrank",
			engine: pfmengine.Claude,
			gauge:  contextGauge{known: true, tokens: 20_000, window: 200_000, percent: 10},
			readings: []pulseReading{
				{At: now.Add(-pulseStep).UnixMilli(), Tokens: 150_000}, {At: now.UnixMilli(), Tokens: 20_000},
			},
			form: contextForms[0],
			want: "🟢 \x1b[38;5;118m▰\x1b[2m▱▱▱▱▱▱▱\x1b[0m \x1b[38;5;245m·····\x1b[0m\x1b[38;5;117m↓\x1b[0m" +
				" \x1b[38;5;117m−130.0K\x1b[0m \x1b[38;5;118m 10%\x1b[0m" +
				" \x1b[1;97m 20.0K\x1b[0m\x1b[38;5;245m/\x1b[0m\x1b[38;5;117m200.0K\x1b[0m",
		},
		{
			name:   "codex counts the human prompts",
			engine: pfmengine.Codex,
			gauge:  contextGauge{known: true, tokens: 14_200, window: 272_000, percent: 5, humanPrompts: 3},
			form:   contextForms[0],
			want: "🟢 \x1b[38;5;118m▰\x1b[2m▱▱▱▱▱▱▱\x1b[0m \x1b[38;5;245m················\x1b[0m" +
				" \x1b[38;5;118m5%\x1b[0m" +
				" \x1b[1;97m 14.2K\x1b[0m\x1b[38;5;245m/\x1b[0m\x1b[38;5;117m272.0K\x1b[0m \x1b[2m✎3\x1b[0m",
		},
		{
			// 92,500 of 500,000 tokens is 18.5%: one cell of eight, where the rounded 19% would turn on two.
			name:   "the narrowest rank draws the gauge alone, by the tokens over the window",
			engine: pfmengine.Claude,
			gauge:  contextGauge{known: true, tokens: 92_500, window: 500_000, percent: 19},
			readings: []pulseReading{
				{At: now.Add(-pulseStep).UnixMilli(), Tokens: 80_000}, {At: now.UnixMilli(), Tokens: 92_500},
			},
			form: contextForms[len(contextForms)-1],
			want: "🟢 \x1b[38;5;118m▰\x1b[2m▱▱▱▱▱▱▱\x1b[0m \x1b[38;5;118m 19%\x1b[0m",
		},
		{
			name:   "a step of 5,000 tokens or more is the pulse's highest",
			engine: pfmengine.Claude,
			gauge:  contextGauge{known: true, tokens: 170_000, window: 200_000, percent: 85},
			readings: []pulseReading{
				{At: now.Add(-pulseStep).UnixMilli(), Tokens: 165_000}, {At: now.UnixMilli(), Tokens: 170_000},
			},
			form: contextForms[0],
			want: "🔥 \x1b[1;38;5;203m▰▰▰▰▰▰▰\x1b[2m▱\x1b[0m \x1b[38;5;245m·····\x1b[0m\x1b[38;5;117m●\x1b[0m" +
				" \x1b[38;5;117m  +5.0K\x1b[0m \x1b[1;38;5;203m 85%\x1b[0m" +
				" \x1b[1;97m170.0K\x1b[0m\x1b[38;5;245m/\x1b[0m\x1b[38;5;117m200.0K\x1b[0m",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := contextColumn(tc.engine, input{}, tc.gauge, tc.readings, now, tc.form)
			if got != tc.want {
				t.Errorf("column = %q, want %q", got, tc.want)
			}
		})
	}
}

// A context no source measures has no reading to add: the line shows the
// harness's share alone, and the session's readings stay as they were.
func TestInstrumentLineOfAnUnmeasuredContextAddsNoReading(t *testing.T) {
	runtime := parityRuntime(t)
	var data input
	if err := json.Unmarshal([]byte(`{"session_id":"session-1","model":{"id":"gpt-6.1-sol-fast[1m]"},`+
		`"context_window":{"used_percentage":12}}`), &data); err != nil {
		t.Fatal(err)
	}
	readings := filepath.Join(runtime.CacheDir, "cc-sl-pulse-session-1")
	const before = `[{"at":1799999990000,"tokens":4000}]`
	if err := os.WriteFile(readings, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	_, line := instrumentLine(runtime, data, "", runtime.now())
	const want = "🟢 ▰▱▱▱▱▱▱▱ ··············· 12%      ?/500.0K │ ⏳ 0s │ Σ $? · ? in · ? out · ? tools │ 💾–"
	if got := stripANSICodes(line); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
	body, err := os.ReadFile(readings)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != before {
		t.Errorf("%s holds %s, want it left as %s", readings, body, before)
	}
}

// The agent row's token counts, row for row (plugins/professor/tests/text.test.ts): one decimal,
// rounded half up, a count that rounds to 1000.0K written in the next unit.
func TestContextTokensAreTheAgentRowsTokens(t *testing.T) {
	for _, tc := range []struct {
		tokens int64
		want   string
	}{
		{0, "0"},
		{870, "870"},
		{999, "999"},
		{1_000, "1.0K"},
		{1_949, "1.9K"},
		{1_950, "2.0K"},
		{74_250, "74.3K"},
		{92_907, "92.9K"},
		{999_949, "999.9K"},
		{999_950, "1.0M"},
		{999_999, "1.0M"},
		{1_000_000, "1.0M"},
		{1_250_000, "1.3M"},
		{1_849_999, "1.8M"},
		{1_850_000, "1.9M"},
		{1_874_214, "1.9M"},
	} {
		if got := formatContextTokens(tc.tokens); got != tc.want {
			t.Errorf("formatContextTokens(%d) = %q, want %q", tc.tokens, got, tc.want)
		}
	}
}
