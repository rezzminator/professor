package tokens

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplayCompactionPreservesDistinctReads(t *testing.T) {
	read := func(id, path, at string, input int) []string {
		call := strings.Replace(
			assistant(id, "claude-sonnet-5", at, input, 10, 0, 0),
			`{"type":"text","text":"ok"}`,
			fmt.Sprintf(`{"type":"tool_use","id":%q,"name":"Read","input":{"file_path":%q}}`, id, path),
			1,
		)
		result := fmt.Sprintf(
			`{"type":"user","timestamp":%q,"message":{"content":[{"type":"tool_result","tool_use_id":%q,"content":"file text"}]}}`,
			at,
			id,
		)
		return []string{call, result}
	}
	for _, test := range []struct {
		name            string
		compact, shrink bool
		want            int
	}{
		{"compact", true, false, catReadFirst},
		{"inferred shrink", false, true, catReadFirst},
		{"no reset", false, false, catReadAgain},
	} {
		t.Run(test.name, func(t *testing.T) {
			lines := read("r1", "/w/demo/A", "2026-09-21T05:00:00Z", 10000)
			lines = append(lines, assistant("m2", "claude-sonnet-5", "2026-09-21T05:01:00Z", 11000, 10, 0, 0))
			if test.compact {
				lines = append(lines, `{"type":"system","subtype":"compact_boundary"}`)
			}
			input := 12000
			if test.shrink || test.compact {
				input = 1000
			}
			lines = append(lines, read("r2", "/w/demo/B", "2026-09-21T05:02:00Z", input)...)
			lines = append(lines, read("r3", "/w/demo/A", "2026-09-21T05:03:00Z", input+1000)...)
			lines = append(lines, assistant("m4", "claude-sonnet-5", "2026-09-21T05:04:00Z", input+2000, 10, 0, 0))
			file := writeTranscript(t, filepath.Join(t.TempDir(), "s1.jsonl"), lines...)
			a, _, _ := testAuditor(t)
			c, err := a.collect(file, nil)
			if err != nil {
				t.Fatal(err)
			}
			r := a.replay(file, c)
			if r == nil {
				t.Fatal("replay returned no run")
			}
			if r.distinctRead != 2 {
				t.Errorf("distinctRead = %v, want 2", r.distinctRead)
			}
			var got []int
			for _, e := range c.seq {
				if e.tool == toolRead {
					got = append(got, e.cat)
				}
			}
			if len(got) != 3 || got[2] != test.want {
				t.Errorf("read categories = %v, want final %d", got, test.want)
			}
			wantAgain := 0.0
			if test.want == catReadAgain {
				wantAgain = 1
			}
			if r.rereadN != wantAgain {
				t.Errorf("rereadN = %v, want %v", r.rereadN, wantAgain)
			}
		})
	}
}
