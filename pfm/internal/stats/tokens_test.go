package stats

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyTokenRecordCountsEachResponseOnce(t *testing.T) {
	for _, tc := range []struct {
		name         string
		lines        []string
		claude, cdx  int64
		wantKnownNow bool
	}{
		{"a response's content-block lines count once", []string{
			`{"type":"assistant","message":{"id":"m1","usage":{"input_tokens":10,"output_tokens":5}}}`,
			`{"type":"assistant","message":{"id":"m1","usage":{"input_tokens":10,"output_tokens":5}}}`,
		}, 15, 0, true},
		{"lines without a message id share their request id", []string{
			`{"type":"assistant","requestId":"r1","message":{"usage":{"cache_read_input_tokens":7}}}`,
			`{"type":"assistant","requestId":"r1","message":{"usage":{"cache_read_input_tokens":7}}}`,
		}, 7, 0, true},
		{"zero top-level counts are read from usage.iterations", []string{
			`{"type":"assistant","message":{"id":"m2","usage":{"input_tokens":0,"output_tokens":0,` +
				`"iterations":[{"input_tokens":100,"output_tokens":20},{"cache_creation_input_tokens":30}]}}}`,
		}, 150, 0, true},
		{"an advisor's iterations count beside the executor's top level", []string{
			`{"type":"assistant","message":{"id":"m3","usage":{"input_tokens":4,"output_tokens":40,` +
				`"cache_read_input_tokens":300,"cache_creation_input_tokens":20,"iterations":[` +
				`{"input_tokens":2,"output_tokens":10,"cache_read_input_tokens":100,"cache_creation_input_tokens":20,"type":"message"},` +
				`{"input_tokens":177,"output_tokens":1422,"cache_creation_input_tokens":8000,"type":"advisor_message","model":"claude-opus-5-5"},` +
				`{"input_tokens":2,"output_tokens":30,"cache_read_input_tokens":200,"type":"message"}]}}}`,
		}, 4 + 40 + 300 + 20 + 177 + 1422 + 8000, 0, true},
		{"an advisor's iterations count beside an empty top level's executor iterations", []string{
			`{"type":"assistant","message":{"id":"m4","usage":{"input_tokens":0,"output_tokens":0,"iterations":[` +
				`{"input_tokens":100,"output_tokens":20,"type":"message"},` +
				`{"input_tokens":50,"output_tokens":5,"type":"advisor_message","model":"claude-opus-5-5"}]}}}`,
		}, 100 + 20 + 50 + 5, 0, true},
		{"a Codex token_count sets the lifetime total; an info-less one is ignored", []string{
			`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":900}}}}`,
			`{"type":"event_msg","payload":{"type":"token_count","info":null}}`,
		}, 0, 900, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := &tokenCacheEntry{}
			for _, line := range tc.lines {
				if err := applyTokenRecord(entry, []byte(line)); err != nil {
					t.Fatalf("applyTokenRecord(%s): %v", line, err)
				}
			}
			if entry.claudeTokens != tc.claude || entry.codexTokens != tc.cdx || entry.known != tc.wantKnownNow {
				t.Fatalf(
					"claude %d codex %d known %v; want %d %d %v",
					entry.claudeTokens,
					entry.codexTokens,
					entry.known,
					tc.claude,
					tc.cdx,
					tc.wantKnownNow,
				)
			}
		})
	}
}

func TestApplyTokenRecordPreservesCodexSegments(t *testing.T) {
	for _, tc := range []struct {
		name   string
		totals []int64
	}{
		{"reset", []int64{1000, 1500, 100, 200, 200}},
		{"reset to zero", []int64{1000, 1500, 0, 100, 200, 200}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lines []string
			for _, total := range tc.totals {
				lines = append(lines, fmt.Sprintf(
					`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":%d}}}}`,
					total,
				))
			}
			t.Run("direct", func(t *testing.T) {
				entry := &tokenCacheEntry{}
				for _, line := range lines {
					if err := applyTokenRecord(entry, []byte(line)); err != nil {
						t.Fatalf("applyTokenRecord: %v", err)
					}
				}
				if entry.codexTokens != 1700 {
					t.Fatalf("codexTokens = %d; want 1700", entry.codexTokens)
				}
			})
			t.Run("appended sampling passes", func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "rollout.jsonl")
				if err := os.WriteFile(path, []byte(strings.Join(lines[:2], "\n")+"\n"), 0o600); err != nil {
					t.Fatalf("write rollout: %v", err)
				}
				sampler := &Sampler{tokenCache: make(map[string]*tokenCacheEntry)}
				first, warnings := sampler.readTokenUsageLocked(path)
				if len(warnings) != 0 || first.total != 1500 {
					t.Fatalf("first sample = %+v, warnings %v; want 1500", first, warnings)
				}
				appendFile(t, path, strings.Join(lines[2:], "\n")+"\n")
				second, warnings := sampler.readTokenUsageLocked(path)
				if len(warnings) != 0 || second.total != 1700 {
					t.Fatalf("second sample = %+v, warnings %v; want 1700", second, warnings)
				}
			})
		})
	}
}

func TestApplyTokenRecordUpdatesRepeatedResponseUsage(t *testing.T) {
	for _, tc := range []struct {
		name     string
		identity string
	}{
		{"message id", `"message":{"id":"m1","usage":`},
		{"request id", `"requestId":"r1","message":{"usage":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := `{"type":"assistant",` + tc.identity + `{"input_tokens":100,"output_tokens":0}}}`
			last := `{"type":"assistant",` + tc.identity + `{"input_tokens":100,"output_tokens":20}}}`
			t.Run("direct", func(t *testing.T) {
				entry := &tokenCacheEntry{}
				lines := []string{first}
				for i := 0; i < countedMessageRing; i++ {
					lines = append(
						lines,
						fmt.Sprintf(`{"type":"assistant","message":{"id":"zero-%d","usage":{"input_tokens":0}}}`, i),
					)
				}
				for _, line := range append(lines, last, last) {
					if err := applyTokenRecord(entry, []byte(line)); err != nil {
						t.Fatalf("applyTokenRecord: %v", err)
					}
				}
				if entry.claudeTokens != 120 {
					t.Fatalf("claudeTokens = %d; want 120", entry.claudeTokens)
				}
				for _, usage := range []string{`{"input_tokens":80}`, `{"input_tokens":0}`, `null`} {
					if err := applyTokenRecord(
						entry,
						[]byte(`{"type":"assistant",`+tc.identity+usage+`}}`),
					); err != nil {
						t.Fatalf("apply revised usage %s: %v", usage, err)
					}
				}
				if entry.claudeTokens != 0 || !entry.known {
					t.Fatalf("revised claudeTokens = %d known %v; want 0 known", entry.claudeTokens, entry.known)
				}
			})
			t.Run("appended sampling passes", func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "session.jsonl")
				if err := os.WriteFile(path, []byte(first+"\n"), 0o600); err != nil {
					t.Fatalf("write transcript: %v", err)
				}
				sampler := &Sampler{tokenCache: make(map[string]*tokenCacheEntry)}
				initial, warnings := sampler.readTokenUsageLocked(path)
				if len(warnings) != 0 || initial.total != 100 {
					t.Fatalf("first sample = %+v, warnings %v; want 100", initial, warnings)
				}
				appendFile(t, path, last+"\n"+last+"\n")
				updated, warnings := sampler.readTokenUsageLocked(path)
				if len(warnings) != 0 || updated.total != 120 {
					t.Fatalf("second sample = %+v, warnings %v; want 120", updated, warnings)
				}
			})
		})
	}
}

// A negative count inside usage.iterations is as malformed as one at the top
// level: the fold would subtract it from the chat's tokens.
func TestApplyTokenRecordRefusesANegativeIterationCount(t *testing.T) {
	entry := &tokenCacheEntry{}
	line := `{"type":"assistant","message":{"id":"m1","usage":{"iterations":[{"input_tokens":-50},{"output_tokens":10}]}}}`
	if err := applyTokenRecord(entry, []byte(line)); err == nil || entry.claudeTokens != 0 {
		t.Fatalf("applyTokenRecord = %v with %d tokens; want the negative count refused", err, entry.claudeTokens)
	}
}
