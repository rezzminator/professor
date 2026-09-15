package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hostops/pfm/internal/mcpserv"
	"hostops/pfm/internal/paths"
)

// callChatTool drives one tool of the production MCP surface — mcpRuntime over
// the jailed runtime, the bridge `pfm mcp` serves — through an in-memory
// protocol session, and decodes its structured answer.
func callChatTool[T any](t *testing.T, name string, arguments any) T {
	t.Helper()
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	service, err := mcpserv.NewConfigured("test", io.Discard, mcpRuntime(commandRuntime{Paths: resolved}, true))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := service.Server().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "pfm-test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("%s returned tool error: %#v", name, result.Content)
	}
	content, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var output T
	if err := json.Unmarshal(content, &output); err != nil {
		t.Fatalf("%s structured output %s: %v", name, content, err)
	}
	return output
}

const findDriftNeedle = "the release train leaves after the ratchet holds"

// seedFindDriftTranscripts writes count Claude transcripts that all hold
// findDriftNeedle, and returns their ids in path order.
func seedFindDriftTranscripts(t *testing.T, root string, count int) []string {
	t.Helper()
	directory := filepath.Join(root, "claude", "find-drift")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, count)
	for index := 0; index < count; index++ {
		id := fmt.Sprintf("f%07d-0000-4000-8000-000000000000", index)
		line := `{"type":"user","message":{"content":"` + findDriftNeedle + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(directory, id+".jsonl"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// TestMCPFindHonorsItsLimitPastFiveCandidates pins chat_find's advertised
// limit (default 10, maximum 50): the shared search used to hand MCP only the
// CLI's best match plus four runners-up, so no limit above 5 was ever honored.
func TestMCPFindHonorsItsLimitPastFiveCandidates(t *testing.T) {
	root := jailTest(t)
	seedFindDriftTranscripts(t, root, 7)
	output := callChatTool[mcpserv.FindOutput](t, "chat_find", mcpserv.FindInput{Excerpt: findDriftNeedle, Limit: 7})
	if output.Count != 7 {
		t.Fatalf("chat_find limit 7 over 7 matches = %d candidates", output.Count)
	}
}

// TestMCPFindIncludesTheAskingSessionOnlyWhenAsked pins include_self: the
// asking session's own transcript is left out by default and searched when the
// caller asks — the input used to be accepted and ignored.
func TestMCPFindIncludesTheAskingSessionOnlyWhenAsked(t *testing.T) {
	root := jailTest(t)
	ids := seedFindDriftTranscripts(t, root, 2)
	t.Setenv("CLAUDE_CODE_SESSION_ID", ids[0])
	excluded := callChatTool[mcpserv.FindOutput](t, "chat_find", mcpserv.FindInput{Excerpt: findDriftNeedle})
	if excluded.Count != 1 || excluded.Candidates[0].ID != ids[1] {
		t.Fatalf("chat_find default = %+v; want only the other session", excluded)
	}
	included := callChatTool[mcpserv.FindOutput](
		t,
		"chat_find",
		mcpserv.FindInput{Excerpt: findDriftNeedle, IncludeSelf: true},
	)
	if included.Count != 2 {
		t.Fatalf("chat_find include_self = %+v; want both sessions", included)
	}
}

// TestMCPFindNamesTheSessionItLeftOut pins self_id: an answer that skipped the
// asking session says which one, so it is never read as a search of everything.
func TestMCPFindNamesTheSessionItLeftOut(t *testing.T) {
	root := jailTest(t)
	ids := seedFindDriftTranscripts(t, root, 2)
	t.Setenv("CLAUDE_CODE_SESSION_ID", ids[0])
	excluded := callChatTool[mcpserv.FindOutput](t, "chat_find", mcpserv.FindInput{Excerpt: findDriftNeedle})
	if excluded.SelfID != ids[0] {
		t.Fatalf("chat_find default self_id = %q; want the excluded %q", excluded.SelfID, ids[0])
	}
	included := callChatTool[mcpserv.FindOutput](
		t,
		"chat_find",
		mcpserv.FindInput{Excerpt: findDriftNeedle, IncludeSelf: true},
	)
	if included.SelfID != "" {
		t.Fatalf("chat_find include_self self_id = %q; want none", included.SelfID)
	}
}

// TestMCPReadDefaultsToTwentyTurns pins chat_read's advertised default
// (last_n 20): the shared extraction used to default to the CLI's single turn.
func TestMCPReadDefaultsToTwentyTurns(t *testing.T) {
	root := jailTest(t)
	const id = "e5555555-5555-4555-8555-555555555555"
	project := filepath.Join(root, "work", "read-default")
	directory := filepath.Join(root, "claude", "read-default")
	for _, path := range []string{project, directory} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	lines := `{"type":"user","cwd":"` + project + `","message":{"content":"first question for the reader"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"first answer"}]}}` + "\n" +
		`{"type":"user","message":{"content":"second question for the reader"}}` + "\n"
	if err := os.WriteFile(filepath.Join(directory, id+".jsonl"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	output := callChatTool[mcpserv.ReadOutput](t, "chat_read", mcpserv.ReadInput{Source: id})
	if output.Count != 3 || output.Truncated {
		t.Fatalf("chat_read default = %+v; want all three turns, untruncated", output)
	}
}
