package transcript

import (
	"encoding/json"
	"testing"
)

func TestResponseKeyNamespacesEachForm(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		message, request, uuid, at string
		want                       string
	}{
		{"the message id wins", "msg_1", "req_1", "u-1", "f:1", "message:msg_1"},
		{"else the request id", "", "req_1", "u-1", "f:1", "request:req_1"},
		{"else the uuid", "", "", "u-1", "f:1", "uuid:u-1"},
		{"else where it sits", "", "", "", "f:1", "at:f:1"},
		{"nothing to key on", "", "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResponseKey(tc.message, tc.request, tc.uuid, tc.at); got != tc.want {
				t.Fatalf("ResponseKey = %q, want %q", got, tc.want)
			}
		})
	}
	if ResponseKey("", "x", "", "") == ResponseKey("x", "", "", "") {
		t.Fatal("a request id keyed equal to the same string as a message id")
	}
}

func TestCodexTokenInfoDecodesBothUsagesAndTheirAbsence(t *testing.T) {
	var info CodexTokenInfo
	line := `{"total_token_usage":{"input_tokens":50,"cached_input_tokens":20,"output_tokens":9,"reasoning_output_tokens":4,"total_tokens":59},` +
		`"last_token_usage":{"input_tokens":30,"total_tokens":31},"model_context_window":272000}`
	if err := json.Unmarshal([]byte(line), &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := CodexTokenUsage{
		InputTokens:           50,
		CachedInputTokens:     20,
		OutputTokens:          9,
		ReasoningOutputTokens: 4,
		TotalTokens:           59,
	}
	if info.TotalTokenUsage == nil || *info.TotalTokenUsage != want || info.LastTokenUsage.InputTokens != 30 ||
		info.ModelContextWindow != 272000 {
		t.Fatalf("decoded %+v / %+v", info.TotalTokenUsage, info.LastTokenUsage)
	}
	var bare CodexTokenInfo
	if err := json.Unmarshal([]byte(`{"model_context_window":1}`), &bare); err != nil {
		t.Fatalf("decode bare: %v", err)
	}
	if bare.TotalTokenUsage != nil || bare.LastTokenUsage.Total() != 0 || info.LastTokenUsage.Total() != 31 {
		t.Fatalf("an absent usage must stay nil and total 0: %+v", bare)
	}
}
