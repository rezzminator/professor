package statusline

import (
	"strconv"
	"testing"
)

// The gauge's tokens are the prompt size of the newest completed call in the
// agent's own transcript, rounded to the nearest percent of its window; with
// no such call, `?`.
func TestRenderAgentRowsGaugeReadsTheTranscript(t *testing.T) {
	task := func(model, window string) string {
		return `{"id":"p","startTime":1790244000000,"model":"` + model + `"` + window + `}`
	}
	// a call on model; the transcript's model is the one a row shows
	completedOn := func(model, id string, read, created int64) string {
		return `{"type":"assistant","timestamp":"2026-09-24T10:00:05Z","message":{"id":"` + id +
			`","model":"` + model + `","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":` +
			strconv.FormatInt(read, 10) + `,"cache_creation_input_tokens":` + strconv.FormatInt(created, 10) + `}}}`
	}
	completed := func(id string, read, created int64) string { return completedOn("claude-opus-5-5", id, read, created) }
	streamingOn := func(model string) string {
		return `{"type":"assistant","timestamp":"2026-09-24T10:00:06Z","message":{"id":"m9","model":"` + model + `",` +
			`"usage":{"input_tokens":900000,"output_tokens":1}}}`
	}
	streaming := streamingOn("claude-opus-5-5")
	// the counts only in usage.iterations, zeros and no cache counters at the top level
	iterated := `{"type":"assistant","timestamp":"2026-09-24T10:00:05Z","message":{"id":"m3","model":"claude-opus-5-5",` +
		`"usage":{"input_tokens":0,"output_tokens":0,"iterations":[` +
		`{"input_tokens":5,"cache_read_input_tokens":90000,"output_tokens":40},` +
		`{"input_tokens":10,"cache_read_input_tokens":290000,"cache_creation_input_tokens":9990,"output_tokens":9}]}}}`
	compacted := `{"type":"system","subtype":"compact_boundary","timestamp":"2026-09-24T10:00:07Z"}`
	const window = `,"contextWindowSize":1000000`
	cases := []struct {
		name  string
		task  string
		lines []string
		want  string // the row, ANSI stripped
	}{
		{
			name:  "a 200K window: the transcript's call is 60%",
			task:  task("claude-opus-5-5", `,"contextWindowSize":200000`),
			lines: []string{completed("m1", 110000, 9990)},
			want:  "▰▰▰▰▱▱▱▱ 60% 120.0K/200.0K Σ$0.07/120.0K/5/0│general-purpose│opus│running 2m:0s│idle 1m55s│💾?✓3m:5s 91%",
		},
		{
			name:  "6.91% rounds to the nearest percent, 7%",
			task:  task("claude-opus-5-5", window),
			lines: []string{completed("m1", 69090, 0)},
			want:  "▱▱▱▱▱▱▱▱ 7% 69.1K/1.0M Σ$0.01/69.1K/5/0│general-purpose│opus│running 2m:0s│idle 1m55s│💾?✓3m:5s 99%",
		},
		{
			name:  "99.96% rounds to 99%, never a full gauge short of the window",
			task:  task("claude-opus-5-5", window),
			lines: []string{completed("m1", 999590, 0)},
			want:  "▰▰▰▰▰▰▰▱ 99% 999.6K/1.0M Σ$0.20/999.6K/5/0│general-purpose│opus│running 2m:0s│idle 1m55s│💾?✓3m:5s 99%",
		},
		{
			name:  "the whole window is 100%",
			task:  task("claude-opus-5-5", window),
			lines: []string{completed("m1", 999990, 0)},
			want:  "▰▰▰▰▰▰▰▰ 100% 1.0M/1.0M Σ$0.20/1.0M/5/0│general-purpose│opus│running 2m:0s│idle 1m55s│💾?✓3m:5s 99%",
		},
		{
			name:  "the newest completed call's input, cache read and cache creation",
			task:  task("claude-opus-5-5", window),
			lines: []string{completed("m1", 100000, 0), completed("m2", 290000, 9990)},
			want:  "▰▰▱▱▱▱▱▱ 30% 300.0K/1.0M Σ$0.13/400.0K/10/0│general-purpose│opus│running 2m:0s│idle 1m55s│💾?✓3m:5s 96%",
		},
		{
			name:  "a response's counts only in its iterations read its last request",
			task:  task("claude-opus-5-5", window),
			lines: []string{completed("m1", 100000, 0), iterated},
			want:  "▰▰▱▱▱▱▱▱ 30% 300.0K/1.0M Σ$0.15/490.0K/54/0│general-purpose│opus│running 2m:0s│idle 1m55s│💾?✓3m:5s 99%",
		},
		{
			name:  "a call from before a compaction no longer stands",
			task:  task("claude-opus-5-5", window),
			lines: []string{completed("m1", 290000, 9990), compacted},
			want:  "?/1.0M Σ$0.11/300.0K/5/0│general-purpose│opus│running 2m:0s│idle 1m53s│💾?✓3m:5s 96%│⟲1",
		},
		{
			name:  "a call still streaming does not displace the completed one",
			task:  task("claude-opus-5-5", window),
			lines: []string{completed("m1", 290000, 9990), streaming},
			want:  "▰▰▱▱▱▱▱▱ 30% 300.0K/1.0M Σ$3.71/1.2M/6/0│general-purpose│opus│running 2m:0s│idle 1m54s│💾?✓3m:6s 96%",
		},
		{
			name:  "an unknown window: the transcript's call, `/?` and the capacity state",
			task:  task("gpt-6-sol", ""),
			lines: []string{completedOn("gpt-6-sol", "m1", 290000, 9990)},
			want:  "300.0K/? ctx missing Σ$0.16/300.0K/5/0 API-eq│general-purpose│sol-6│running 2m:0s│idle 1m55s│💾∅ 96%",
		},
		{
			name:  "no completed call: unknown tokens",
			task:  task("claude-opus-5-5", window),
			lines: []string{streaming},
			want:  "?/1.0M Σ$3.60/900.0K/1/0│general-purpose│opus│running 2m:0s│idle 1m54s│💾–",
		},
		{
			name:  "no completed call, no window: `?`, `/?` and the capacity state",
			task:  task("gpt-6-sol", ""),
			lines: []string{streamingOn("gpt-6-sol")},
			want:  "?/? ctx missing Σ$?/0/0/0 API-eq│general-purpose│sol-6│running 2m:0s│idle 1m54s│💾–",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := nestedSession(t, map[string][]string{"p": tc.lines}, map[string]string{"p": ""})
			got, _ := renderOneSubagent(t, session, tc.task)
			if got := stripANSICodes(got); got != tc.want {
				t.Errorf("content = %q, want %q", got, tc.want)
			}
		})
	}
}
