package mockengine

import (
	"encoding/json"
	"testing"
)

// TestClaudeRecordKeysAreClaudeCodes pins the wire keys of the records only
// some beats write: the automatic title and the synthetic API-error reply.
func TestClaudeRecordKeysAreClaudeCodes(t *testing.T) {
	cases := []struct {
		record claudeRecord
		want   string
	}{
		{
			record: claudeRecord{Type: claudeAITitle, AITitle: "Fixing the hinge", SessionID: "s1"},
			want:   `{"type":"ai-title","sessionId":"s1","isSidechain":false,"aiTitle":"Fixing the hinge"}`,
		},
		{
			record: claudeRecord{Type: roleAssistant, SessionID: "s1", IsAPIErrorMessage: true, Error: "rate_limit"},
			want:   `{"type":"assistant","sessionId":"s1","isSidechain":false,"isApiErrorMessage":true,"error":"rate_limit"}`,
		},
	}
	for _, tc := range cases {
		encoded, err := json.Marshal(tc.record)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != tc.want {
			t.Errorf("record = %s, want %s", encoded, tc.want)
		}
	}
}
