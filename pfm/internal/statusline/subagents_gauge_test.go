package statusline

import (
	"strconv"
	"strings"
	"testing"
)

// Claude Code's payload tokenCount is not the agent's context: the gauge reads
// the prompt size of the newest completed call in the agent's own transcript,
// and keeps the payload's count only while none has landed.
func TestRenderSubagentsGaugeReadsTheTranscriptContext(t *testing.T) {
	const task = `{"id":"p","type":"local_agent","status":"running","startTime":1790244000000,` +
		`"model":"claude-opus-5-5","contextWindowSize":1000000,"tokenCount":252}`
	completed := func(id string, read, created int64) string {
		return `{"type":"assistant","timestamp":"2026-09-24T10:00:05Z","message":{"id":"` + id +
			`","model":"claude-opus-5-5","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":` +
			strconv.FormatInt(read, 10) + `,"cache_creation_input_tokens":` + strconv.FormatInt(created, 10) + `}}}`
	}
	streaming := `{"type":"assistant","timestamp":"2026-09-24T10:00:06Z","message":{"id":"m9","model":"claude-opus-5-5",` +
		`"usage":{"input_tokens":900000,"output_tokens":1}}}`
	cases := []struct {
		name  string
		lines []string
		want  string // the gauge, ANSI stripped
	}{
		{
			name:  "the newest completed call's input, cache read and cache creation",
			lines: []string{completed("m1", 100000, 0), completed("m2", 290000, 9990)},
			want:  "▰▰▱▱▱▱▱▱ 30% 300.0K/1.0M",
		},
		{
			name:  "a call still streaming does not displace the completed one",
			lines: []string{completed("m1", 290000, 9990), streaming},
			want:  "▰▰▱▱▱▱▱▱ 30% 300.0K/1.0M",
		},
		{
			name:  "no completed call yet keeps the payload's count",
			lines: []string{streaming},
			want:  "▱▱▱▱▱▱▱▱ 0% 252/1.0M",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := nestedSession(t, map[string][]string{"p": tc.lines}, map[string]string{"p": ""})
			got, _ := renderOneSubagent(t, session, task)
			if !strings.Contains(stripANSICodes(got), tc.want+" ") {
				t.Fatalf("content = %q, want the gauge %q", stripANSICodes(got), tc.want)
			}
		})
	}
}

// An unreadable transcript is not a zero context: the payload's count stands.
func TestRenderSubagentsGaugeKeepsTheCountWithoutATranscript(t *testing.T) {
	const task = `{"id":"p","type":"local_agent","status":"running","startTime":1790244000000,` +
		`"model":"claude-opus-5-5","contextWindowSize":1000000,"tokenCount":252}`
	session := nestedSession(t, map[string][]string{}, map[string]string{"p": ""})
	got, _ := renderOneSubagent(t, session, task)
	if !strings.Contains(stripANSICodes(got), "252/1.0M") {
		t.Fatalf("content = %q, want the payload's 252 in the gauge", stripANSICodes(got))
	}
}
