package stats

import "testing"

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

// A negative count inside usage.iterations is as malformed as one at the top
// level: the fold would subtract it from the chat's tokens.
func TestApplyTokenRecordRefusesANegativeIterationCount(t *testing.T) {
	entry := &tokenCacheEntry{}
	line := `{"type":"assistant","message":{"id":"m1","usage":{"iterations":[{"input_tokens":-50},{"output_tokens":10}]}}}`
	if err := applyTokenRecord(entry, []byte(line)); err == nil || entry.claudeTokens != 0 {
		t.Fatalf("applyTokenRecord = %v with %d tokens; want the negative count refused", err, entry.claudeTokens)
	}
}
