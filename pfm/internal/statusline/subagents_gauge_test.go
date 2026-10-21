package statusline

import (
	"strconv"
	"strings"
	"testing"
)

// The gauge's tokens come from Claude Code's payload tokenCount only when it is
// above zero and shows a percent above 0% (any count above zero when the window
// is unknown); otherwise from the prompt size of the newest completed call in the
// agent's own transcript; otherwise a count the payload stated, a zero included;
// otherwise `?`.
func TestRenderSubagentsGaugeFallsBackToTheTranscript(t *testing.T) {
	task := func(model, counters string) string {
		return `{"id":"p","type":"local_agent","status":"running","startTime":1790244000000,` +
			`"model":"` + model + `"` + counters + `}`
	}
	completed := func(id string, read, created int64) string {
		return `{"type":"assistant","timestamp":"2026-09-24T10:00:05Z","message":{"id":"` + id +
			`","model":"claude-opus-5-5","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":` +
			strconv.FormatInt(read, 10) + `,"cache_creation_input_tokens":` + strconv.FormatInt(created, 10) + `}}}`
	}
	streaming := `{"type":"assistant","timestamp":"2026-09-24T10:00:06Z","message":{"id":"m9","model":"claude-opus-5-5",` +
		`"usage":{"input_tokens":900000,"output_tokens":1}}}`
	const window = `,"contextWindowSize":1000000`
	cases := []struct {
		name  string
		task  string
		lines []string
		want  string // the gauge, ANSI stripped
	}{
		{
			name:  "a payload count showing 0% falls back to the transcript's call",
			task:  task("claude-opus-5-5", window+`,"tokenCount":252`),
			lines: []string{completed("m1", 290000, 9990)},
			want:  "▰▰▱▱▱▱▱▱ 30% 300.0K/1.0M",
		},
		{
			name:  "a payload count showing a percent beats the transcript's call",
			task:  task("claude-opus-5-5", window+`,"tokenCount":300000`),
			lines: []string{completed("m1", 490000, 9990)},
			want:  "▰▰▱▱▱▱▱▱ 30% 300.0K/1.0M",
		},
		{
			name:  "a stated zero falls back to the transcript's call",
			task:  task("claude-opus-5-5", window+`,"tokenCount":0`),
			lines: []string{completed("m1", 290000, 9990)},
			want:  "▰▰▱▱▱▱▱▱ 30% 300.0K/1.0M",
		},
		{
			name:  "a stated zero with no completed call stays a zero",
			task:  task("claude-opus-5-5", window+`,"tokenCount":0`),
			lines: []string{streaming},
			want:  "▱▱▱▱▱▱▱▱ 0% 0/1.0M",
		},
		{
			name:  "no payload count: the newest completed call's input, cache read and cache creation",
			task:  task("claude-opus-5-5", window),
			lines: []string{completed("m1", 100000, 0), completed("m2", 290000, 9990)},
			want:  "▰▰▱▱▱▱▱▱ 30% 300.0K/1.0M",
		},
		{
			name:  "no payload count: a call still streaming does not displace the completed one",
			task:  task("claude-opus-5-5", window),
			lines: []string{completed("m1", 290000, 9990), streaming},
			want:  "▰▰▱▱▱▱▱▱ 30% 300.0K/1.0M",
		},
		{
			name:  "an unknown window shows no percent: a payload count above zero stands",
			task:  task("gpt-6-sol", `,"tokenCount":252`),
			lines: []string{completed("m1", 290000, 9990)},
			want:  "252/? ctx missing",
		},
		{
			name:  "no payload count and an unknown window: the transcript's call, `/?` and the capacity state",
			task:  task("gpt-6-sol", ""),
			lines: []string{completed("m1", 290000, 9990)},
			want:  "300.0K/? ctx missing",
		},
		{
			name:  "no payload count and no completed call: unknown tokens",
			task:  task("claude-opus-5-5", window),
			lines: []string{streaming},
			want:  "?/1.0M",
		},
		{
			name:  "no payload count, no completed call, no window: `?`, `/?` and the capacity state",
			task:  task("gpt-6-sol", ""),
			lines: []string{streaming},
			want:  "?/? ctx missing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := nestedSession(t, map[string][]string{"p": tc.lines}, map[string]string{"p": ""})
			got, _ := renderOneSubagent(t, session, tc.task)
			if !strings.Contains(stripANSICodes(got), tc.want+" ") {
				t.Fatalf("content = %q, want the gauge %q", stripANSICodes(got), tc.want)
			}
		})
	}
}

// An unreadable transcript leaves the payload's stated count, even one showing 0%.
func TestRenderSubagentsGaugeKeepsTheCountWithoutATranscript(t *testing.T) {
	const task = `{"id":"p","type":"local_agent","status":"running","startTime":1790244000000,` +
		`"model":"claude-opus-5-5","contextWindowSize":1000000,"tokenCount":252}`
	session := nestedSession(t, map[string][]string{}, map[string]string{"p": ""})
	got, _ := renderOneSubagent(t, session, task)
	if !strings.Contains(stripANSICodes(got), "252/1.0M") {
		t.Fatalf("content = %q, want the payload's 252 in the gauge", stripANSICodes(got))
	}
}
