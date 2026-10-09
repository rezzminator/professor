package codexmeta

import (
	"errors"
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

func TestDecodeReadsThePayloadIdentityTokensNeed(t *testing.T) {
	line := `{"timestamp":"2026-09-20T06:00:09.000Z","type":"session_meta","payload":{` +
		`"id":"child","session_id":"root","timestamp":"2026-09-20T06:00:01.250Z","cwd":"/work/demo",` +
		`"context_window":{"window_id":"win-7"},` +
		`"source":{"subagent":{"thread_spawn":{"parent_thread_id":"root","agent_path":"/root/fix_1a"}}},` +
		`"thread_source":"subagent"}}`
	got, err := Decode([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	want := [...]string{"child", "root", "/work/demo", "2026-09-20T06:00:01.250Z", "/root/fix_1a", "win-7", "root"}
	have := [...]string{
		got.ID, got.SessionID, got.CWD, got.Timestamp, got.AgentPath,
		got.ContextWindow.WindowID, got.ParentThreadID,
	}
	if have != want {
		t.Fatalf("fields = %q, want %q", have, want)
	}
	explicit, err := Decode([]byte(`{"type":"session_meta","payload":{"agent_path":"/root/own",` +
		`"source":{"subagent":{"thread_spawn":{"agent_path":"/root/spawn"}}}}}`))
	if err != nil || explicit.AgentPath != "/root/own" {
		t.Fatalf("agent path = %q, %v; want the payload's own /root/own", explicit.AgentPath, err)
	}
	// a member only pfm tokens reads, in a shape it does not expect, never
	// costs the index, kill or headless the thread's identity
	odd, err := Decode([]byte(`{"type":"session_meta","payload":{"id":"t1","thread_source":"subagent",` +
		`"cwd":7,"agent_path":null,"context_window":3,"timestamp":{}}}`))
	if err != nil || odd.ID != "t1" || odd.Kind != Subagent || odd.CWD != "" || odd.ContextWindow.WindowID != "" {
		t.Fatalf("odd-typed extras = %+v, %v; want the identity kept and the extras absent", odd, err)
	}
}

func TestFieldsRole(t *testing.T) {
	for _, tc := range []struct {
		name, payload, want string
	}{
		{"a string source", `{"source":"cli","thread_source":"user"}`, "cli"},
		{"a sub-agent role string", `{"source":{"subagent":"review"},"thread_source":"subagent"}`, "review"},
		{"a spawned sub-agent falls to thread_source", `{"source":{"subagent":{"thread_spawn":{}}},"thread_source":"subagent"}`, "subagent"},
		{"a null first member, subagent, gives its key", `{"source":{"subagent":null,"x":"y"}}`, "subagent"},
		{"the first member's value", `{"source":{"exec":"runner"}}`, "runner"},
		{"a null first member gives its key", `{"source":{"mcp":null}}`, "mcp"},
		{"integer keys come first", `{"source":{"b":"late","7":"seven","2":"two"}}`, "two"},
		{"an empty source falls to thread_source", `{"source":"","thread_source":"user"}`, "user"},
		{"an empty object", `{"source":{}}`, ""},
		{"nothing", `{}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header, err := Decode([]byte(`{"type":"session_meta","payload":` + tc.payload + `}`))
			if err != nil {
				t.Fatal(err)
			}
			if got := header.Role(); got != tc.want {
				t.Fatalf("Role() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadHeaderNamesARolloutWithoutSessionMeta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"turn_context"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHeader(path); !errors.Is(err, ErrNoSessionMeta) {
		t.Fatalf("ReadHeader err = %v, want ErrNoSessionMeta", err)
	}
	if _, err := ReadHeader(
		filepath.Join(t.TempDir(), "missing.jsonl"),
	); err == nil ||
		errors.Is(err, ErrNoSessionMeta) {
		t.Fatalf("a missing file read as %v; want an open error, never ErrNoSessionMeta", err)
	}
}
