package mcpserv

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
)

// TestChatSaveRefusesATargetWithNoPathSeparator is the F7 regression:
// chat_save's target is a FILE PATH to append a transcript snapshot to, not a
// chat — but nothing stopped a caller from passing a bare chat name out of
// habit, which created a file of that name beside wherever the MCP server
// process happened to be running and appended a whole transcript to it (this
// is exactly how "mcpaudit-b" became a stray 142KB file in the operator's
// home directory). The fix refuses any target without a path separator
// before it ever reaches the dispatcher.
func TestChatSaveRefusesATargetWithNoPathSeparator(t *testing.T) {
	dispatched := false
	service := &Service{backend: &backend{
		dispatch: func(context.Context, []string, io.Writer, io.Writer) int {
			dispatched = true
			return 0
		},
	}}

	if _, _, err := service.chatSave(
		context.Background(), nil, SaveInput{Target: "mcpaudit-b"},
	); err == nil || !strings.Contains(err.Error(), "is not a file path") {
		t.Fatalf("chatSave error = %v, want a not-a-file-path refusal", err)
	}
	if dispatched {
		t.Fatal("chatSave dispatched a bare chat name instead of refusing it")
	}
}

// TestChatSaveDispatchesAPathShapedTarget is the companion positive case: a
// target that DOES contain a path separator is still accepted and reaches
// the dispatcher unchanged, transcript argument included when supplied.
func TestChatSaveDispatchesAPathShapedTarget(t *testing.T) {
	var calls [][]string
	service := &Service{backend: &backend{
		dispatch: func(_ context.Context, args []string, _, _ io.Writer) int {
			calls = append(calls, append([]string(nil), args...))
			return 0
		},
	}}

	if _, _, err := service.chatSave(
		context.Background(), nil,
		SaveInput{Target: "./notes/mcpaudit-b.md", Transcript: "/jailed/transcript.jsonl"},
	); err != nil {
		t.Fatalf("chatSave: %v", err)
	}
	want := [][]string{{"chat", "save", "./notes/mcpaudit-b.md", "/jailed/transcript.jsonl"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("dispatch calls = %q, want %q", calls, want)
	}
}
