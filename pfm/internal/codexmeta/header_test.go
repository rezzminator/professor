package codexmeta

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeNativeRootAndSubagentSources(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		kind          Kind
		parent        string
	}{
		{"legacy-cli", `{"id":"root","source":"cli"}`, User, ""},
		{"continued-root", `{"id":"next","session_id":"original","thread_source":"user"}`, User, "original"},
		{"nested-child", `{"id":"child","source":{"subagent":{"thread_spawn":{"parent_thread_id":"root"}}}}`, Subagent, "root"},
		{"explicit-child", `{"id":"child","thread_source":"subagent","parent_thread_id":"root"}`, Subagent, "root"},
		{"unknown", `{"id":"ambiguous"}`, Unknown, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode([]byte(`{"type":"session_meta","payload":` + tc.payload + `}`))
			if err != nil || got.Kind != tc.kind || got.LineageParent != tc.parent {
				t.Fatalf("header=%+v err=%v", got, err)
			}
		})
	}
}

func TestReadDoesNotSearchAnUnboundedTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	for _, content := range []string{
		strings.Repeat(`{"type":"response_item"}`+"\n", 20) + `{"type":"session_meta","source":"cli"}`,
		strings.Repeat("x", 1024*1024+1),
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadHeader(path); err == nil {
			t.Fatal("accepted metadata beyond bounded prefix")
		}
	}
}
