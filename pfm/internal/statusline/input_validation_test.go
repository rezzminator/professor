package statusline

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// A bad value costs only itself: the line still draws every other segment, the
// bad window reads ERR (never hidden as off-plan) and the cause goes to warn.
func TestRenderKeepsTheLineOverABadPayloadValue(t *testing.T) {
	const fiveOK = `"five_hour":{"used_percentage":29,"resets_at":1786840000}`
	const sevenOK = `"seven_day":{"used_percentage":62,"resets_at":1787100000}`
	for _, tc := range []struct {
		name, payload string
		wantRow       string // the stripped Claude quota row, or "" to skip
		wantRowLacks  string // a substring the quota row must not hold
		wantContext   string // a substring of the stripped context line
		wantWarn      string
	}{
		{
			name:     "five-hour percentage out of range",
			payload:  `{"rate_limits":{"five_hour":{"used_percentage":101,"resets_at":1786840000},` + sevenOK + `}}`,
			wantRow:  "5h-used:ERR",
			wantWarn: "statusline: input: invalid five_hour quota\n",
		},
		{
			name:    "five-hour window of the wrong type",
			payload: `{"rate_limits":{"five_hour":{"used_percentage":"many"},` + sevenOK + `}}`,
			wantRow: "5h-used:ERR",
			wantWarn: "statusline: input: decode five_hour rate limit: json: cannot unmarshal string into Go struct " +
				"field rateWindow.used_percentage of type float64\n",
		},
		{
			name:    "scoped limits of the wrong type",
			payload: `{"rate_limits":{` + fiveOK + `,` + sevenOK + `,"limits":"none"}}`,
			wantRow: "7d-fable-used:ERR",
			wantWarn: "statusline: input: decode scoped rate limits: json: cannot unmarshal string into Go value " +
				"of type []usagehook.ScopedLimit\n",
		},
		{
			name: "scoped percentage out of range",
			payload: `{"rate_limits":{` + fiveOK + `,` + sevenOK + `,"limits":[{"kind":"weekly_scoped","percent":-1,` +
				`"is_active":true,"resets_at":"2030-01-01T00:00:00Z","scope":{"model":{"display_name":"Fable"}}}]}}`,
			wantRow:  "7d-fable-used:ERR",
			wantWarn: "statusline: input: invalid scoped weekly_scoped quota -1\n",
		},
		{
			name: "another model's scoped percentage out of range draws no Fable row",
			payload: `{"rate_limits":{` + fiveOK + `,` + sevenOK + `,"limits":[{"kind":"weekly_scoped","percent":-1,` +
				`"is_active":true,"resets_at":"2030-01-01T00:00:00Z","scope":{"model":{"display_name":"Opus"}}}]}}`,
			wantRow:      "5h-used:29%",
			wantRowLacks: "fable",
			wantWarn:     "statusline: input: invalid scoped weekly_scoped quota -1\n",
		},
		{
			name: "scoped reset unreadable keeps the reading",
			payload: `{"rate_limits":{` + fiveOK + `,` + sevenOK + `,"limits":[{"kind":"weekly_scoped","percent":12,` +
				`"is_active":true,"resets_at":"tomorrow","scope":{"model":{"display_name":"Fable"}}}]}}`,
			wantRow: "7d-fable-used:12%",
			wantWarn: `statusline: payload quota: decode seven_day_fable reset "tomorrow": parsing time "tomorrow" ` +
				`as "2006-01-02T15:04:05Z07:00": cannot parse "tomorrow" as "2006" (reading kept, no countdown)` + "\n",
		},
		{
			name: "scoped reset empty keeps the reading",
			payload: `{"rate_limits":{` + fiveOK + `,` + sevenOK + `,"limits":[{"kind":"weekly_scoped","percent":12,` +
				`"is_active":true,"resets_at":"","scope":{"model":{"display_name":"Fable"}}}]}}`,
			wantRow: "7d-fable-used:12% │",
		},
		{
			name:        "negative context counters read as unknown",
			payload:     `{"context_window":{"current_usage":{"input_tokens":-1}}}`,
			wantContext: "?% ?",
			wantWarn:    "statusline: input: negative current_usage counters: context tokens unknown\n",
		},
		{
			name:        "negative duration reads as zero",
			payload:     `{"cost":{"total_duration_ms":-5}}`,
			wantContext: "⏳ 0s",
			wantWarn:    "statusline: input: negative duration -5: unknown\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := fableRuntime(t.TempDir(), 2, quotaNow)
			weeklyCache(t, runtime, quotaCodexCache, time.Minute)
			var warn bytes.Buffer
			runtime.Warn = &warn
			lines := renderQuotaLines(t, runtime, tc.payload)
			if !strings.Contains(warn.String(), tc.wantWarn) {
				t.Fatalf("warn = %q, want it to hold %q", warn.String(), tc.wantWarn)
			}
			if tc.wantRow != "" && !strings.Contains(stripANSICodes(lines[2]), tc.wantRow) {
				t.Fatalf("quota row = %q, want it to hold %q", stripANSICodes(lines[2]), tc.wantRow)
			}
			if tc.wantRowLacks != "" && strings.Contains(stripANSICodes(lines[2]), tc.wantRowLacks) {
				t.Fatalf("quota row = %q, want no %q", stripANSICodes(lines[2]), tc.wantRowLacks)
			}
			if tc.wantContext != "" && !strings.Contains(stripANSICodes(lines[1]), tc.wantContext) {
				t.Fatalf("context line = %q, want it to hold %q", stripANSICodes(lines[1]), tc.wantContext)
			}
		})
	}
}
